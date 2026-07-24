package registry

import (
	"errors"
	"sync"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// circuitPeriods is Prowlarr's escalation ladder (EscalationBackOff.Periods,
// verified against develop): index i is how long DisabledTill extends past now once
// the circuit climbs to level i. Level 0 means "closed" (never disabled).
var circuitPeriods = []time.Duration{
	0,
	60 * time.Second,
	5 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	1 * time.Hour,
	3 * time.Hour,
	6 * time.Hour,
	12 * time.Hour,
	24 * time.Hour,
}

// maxCircuitLevel is the top rung of circuitPeriods.
var maxCircuitLevel = len(circuitPeriods) - 1

// circuitLocks serializes a circuit's read-modify-write per instance id so two
// concurrent failures (or a failure racing a recovery) on the same indexer can't both
// read the same level and clobber each other's escalation update. harbrr is single
// process, so an in-memory per-instance mutex is sufficient; a shared set (not a mutex
// on the adapter) so it survives an adapter rebuild for the same instance.
type circuitLocks struct{ m sync.Map }

// lock acquires the per-instance mutex and returns its unlock.
func (l *circuitLocks) lock(instanceID int64) func() {
	v, _ := l.m.LoadOrStore(instanceID, &sync.Mutex{})
	mu, _ := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// startupGrace is how long after the registry boots a qualifying failure's disable
// window is capped, so a rocky first poll of every configured indexer doesn't nuke
// the whole fleet to a multi-hour disable (Prowlarr: 15-minute grace).
const startupGrace = 15 * time.Minute

// startupGraceCap is the disable-window ceiling applied inside startupGrace.
const startupGraceCap = 5 * time.Minute

// errCircuitOpen is returned by the dispatch gate in place of hitting the tracker.
// It is deliberately outside classifyHealth's kinds: a skipped call is not a new
// failure and must not feed back into the escalation it is itself enforcing.
var errCircuitOpen = errors.New("registry: circuit open")

// escalate advances state one rung for a qualifying failure of kind, applying
// Prowlarr's rules sourced from ProviderStatusServiceBase/EscalationBackOff:
//   - a transport failure (connection refused/reset, DNS, TLS, EOF — see
//     isTransportError) sets level 1 once and never climbs further: don't punish
//     an indexer for the operator's dead network. Exception: gatewayOutage — a
//     CDN answering 502/504/522 is the indexer's own origin being down, not the
//     operator's network, so it climbs like every other kind; a tracker down for
//     days must not be re-polled every 60 seconds.
//   - every other classified kind (auth, anti-bot, rate-limited, parse) climbs one
//     rung, capped at maxCircuitLevel.
//   - retryAfter (non-zero only for a rate-limited failure carrying Retry-After) is a
//     hard floor on the resulting disable window.
//   - a failure landing within startupGrace of the registry's boot is capped to
//     startupGraceCap regardless of rung.
func escalate(cur database.CircuitState, kind string, gatewayOutage bool, retryAfter time.Duration, now, startedAt time.Time) database.CircuitState {
	next := cur
	if next.InitialFailure.IsZero() {
		next.InitialFailure = now
	}
	if kind == domain.HealthTransport && !gatewayOutage {
		if next.EscalationLevel < 1 {
			next.EscalationLevel = 1
		}
	} else if next.EscalationLevel < maxCircuitLevel {
		next.EscalationLevel++
	}
	// The startup grace caps only the LADDER-derived period — an explicit Retry-After
	// is the tracker's own instruction and must remain the absolute floor, so it is
	// applied AFTER the cap. (Capping first, then honouring Retry-After, means a
	// "Retry-After: 1h" during the first 15 minutes still holds the full hour rather
	// than being clamped to 5m and re-hammering the tracker.)
	window := circuitPeriods[next.EscalationLevel]
	if now.Sub(startedAt) < startupGrace && window > startupGraceCap {
		window = startupGraceCap
	}
	if retryAfter > window {
		window = retryAfter
	}
	next.DisabledTill = now.Add(window)
	return next
}

// recover descends state one rung on a qualifying success (search.Search/Grab
// returning no classified error), clearing the current disable window — a success
// proves the indexer is reachable right now, even if the ladder stays partly
// climbed so a subsequent failure escalates from where it left off rather than from
// scratch. The failure streak (InitialFailure) clears once the ladder bottoms out.
// Mirrors Prowlarr's RecordSuccess: one rung at a time, never a full reset, so a
// flaky indexer hovers rather than thrashing between fully open and fully disabled.
func recoverCircuit(cur database.CircuitState) database.CircuitState {
	next := cur
	if next.EscalationLevel > 0 {
		next.EscalationLevel--
	}
	if next.EscalationLevel == 0 {
		next.InitialFailure = time.Time{}
	}
	next.DisabledTill = time.Time{}
	return next
}

// retryAfterOf returns the Retry-After the tracker asked for, when err carries one
// (a search.RateLimitedError) — zero otherwise. It is the hard floor escalate applies
// to the computed disable window, matching Prowlarr's TooManyRequestsException
// handling.
func retryAfterOf(err error) time.Duration {
	var rle *search.RateLimitedError
	if errors.As(err, &rle) {
		return rle.RetryAfter
	}
	return 0
}
