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

// escalationPolicy is how one health kind moves on the ladder: floor is the lowest
// rung a failure of that kind may land on (a first failure jumps straight there), step
// is how many rungs each subsequent failure climbs. step 0 pins the level where it is.
type escalationPolicy struct{ floor, step int }

// escalationPolicies is the per-kind curve (autobrr/harbrr#389). These are not the same
// failure, so they must not share one uniform climb — indices are circuitPeriods':
//   - auth_failure {5, 2} — 1h, then 6h, then the 24h top. Bad credentials do not fix
//     themselves on a minutes-scale retry, and re-presenting them is what earns an
//     account lock, so the first one already backs off for an hour.
//   - rate_limited {2, 1} — never race a limiter: the shortest window is 5m (an
//     explicit Retry-After still floors it higher below).
//   - anti_bot {3, 1} — 15m minimum: retrying a challenge blind burns solver budget
//     and reads as hostile to the tracker.
//
// Kinds absent from the table take defaultEscalation: parse_error (definition drift,
// but often transient markup) and a gateway outage recorded under the transport kind.
var escalationPolicies = map[string]escalationPolicy{
	domain.HealthAuthFailure: {floor: 5, step: 2},
	domain.HealthRateLimited: {floor: 2, step: 1},
	domain.HealthAntiBot:     {floor: 3, step: 1},
}

// defaultEscalation is the forgiving curve: one rung per failure from the bottom.
var defaultEscalation = escalationPolicy{floor: 0, step: 1}

// transportEscalation pins a non-gateway transport failure at level 1 forever (step 0):
// don't punish an indexer for the operator's dead network.
var transportEscalation = escalationPolicy{floor: 1, step: 0}

// escalationPolicyFor picks the curve for a failure. A gatewayOutage is recorded under
// the transport kind but is the indexer's OWN origin being down (a CDN answering
// 502/504/522), not the operator's network, so it climbs like everything else — a
// tracker down for days must not be re-polled every 60 seconds.
func escalationPolicyFor(kind string, gatewayOutage bool) escalationPolicy {
	if kind == domain.HealthTransport && !gatewayOutage {
		return transportEscalation
	}
	if p, ok := escalationPolicies[kind]; ok {
		return p
	}
	return defaultEscalation
}

// escalate advances state for a qualifying failure of kind along that kind's curve
// (see escalationPolicies), capped at maxCircuitLevel. On top of the rung:
//   - retryAfter (non-zero only for a rate-limited failure carrying Retry-After) is a
//     hard floor on the resulting disable window.
//   - a failure landing within startupGrace of the registry's boot is capped to
//     startupGraceCap regardless of rung.
func escalate(cur database.CircuitState, kind string, gatewayOutage bool, retryAfter time.Duration, now, startedAt time.Time) database.CircuitState {
	next := cur
	if next.InitialFailure.IsZero() {
		next.InitialFailure = now
	}
	p := escalationPolicyFor(kind, gatewayOutage)
	// max(cur+step, floor) deliberately NEVER lowers a level a harsher kind earned:
	// a transport blip on an indexer sitting at the auth rungs must not collapse its
	// backoff and resume presenting bad credentials on a 60s window. Levels descend
	// only on success (recoverCircuit), one rung at a time. For the step-0 transport
	// policy this is byte-identical to the old `if level < 1 { level = 1 }`.
	next.EscalationLevel = min(max(next.EscalationLevel+p.step, p.floor), maxCircuitLevel)
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
