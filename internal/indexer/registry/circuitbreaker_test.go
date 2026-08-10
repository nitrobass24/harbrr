package registry

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbtest"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
)

var (
	circuitNow    = time.Date(2026, time.June, 14, 12, 0, 0, 0, time.UTC)
	longAgoBoot   = circuitNow.Add(-1 * time.Hour) // well outside startupGrace
	freshlyBooted = circuitNow.Add(-time.Minute)   // inside startupGrace
)

// TestEscalatePerKindCurve pins #389's per-kind policy: the rungs successive failures
// of one kind land on, and the disable window each rung implies.
func TestEscalatePerKindCurve(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		kind       string
		gateway    bool
		wantLevels []int
	}{
		// Auth jumps to 1h, then 6h, then the 24h top: never a minutes-scale retry of
		// credentials that are wrong.
		{name: "auth jumps hard", kind: domain.HealthAuthFailure, wantLevels: []int{5, 7, 9, 9}},
		// Rate-limited never races the limiter: 5m floor, then one rung at a time.
		{name: "rate limited floors at 5m", kind: domain.HealthRateLimited, wantLevels: []int{2, 3, 4}},
		// Anti-bot backs off meaningfully rather than burning solver budget.
		{name: "anti-bot floors at 15m", kind: domain.HealthAntiBot, wantLevels: []int{3, 4, 5}},
		// Parse errors stay forgiving: the old uniform single-rung climb.
		{name: "parse stays forgiving", kind: domain.HealthParseError, wantLevels: []int{1, 2, 3}},
		// A dead local network is not the indexer's fault: pinned at level 1 forever.
		{name: "transport pins at 1", kind: domain.HealthTransport, wantLevels: []int{1, 1, 1}},
		// A gateway outage IS the indexer's origin being down: it climbs.
		{name: "gateway outage climbs", kind: domain.HealthTransport, gateway: true, wantLevels: []int{1, 2, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := database.CircuitState{InstanceID: 1}
			for i, want := range tt.wantLevels {
				state = escalate(state, tt.kind, tt.gateway, 0, circuitNow, longAgoBoot)
				if state.EscalationLevel != want {
					t.Fatalf("failure %d: level = %d, want %d", i+1, state.EscalationLevel, want)
				}
				if wantTill := circuitNow.Add(circuitPeriods[want]); !state.DisabledTill.Equal(wantTill) {
					t.Errorf("failure %d: DisabledTill = %v, want %v", i+1, state.DisabledTill, wantTill)
				}
			}
		})
	}
}

// TestEscalateNeverPassesTopRung walks the forgiving curve past the top of the ladder.
func TestEscalateNeverPassesTopRung(t *testing.T) {
	t.Parallel()
	state := database.CircuitState{InstanceID: 1}
	for range maxCircuitLevel + 3 {
		state = escalate(state, domain.HealthParseError, false, 0, circuitNow, longAgoBoot)
	}
	if state.EscalationLevel != maxCircuitLevel {
		t.Errorf("level = %d, want capped at %d", state.EscalationLevel, maxCircuitLevel)
	}
}

// TestAuthCurveHoldsFailingStatus keeps the ladder and PR 1's tri-state derivation
// coherent: the rung an auth failure jumps to is the same level failingWindow reads, so
// the derived status stays "failing" for the full 24h ceiling rather than expiring to
// "unknown" while the circuit is still disabled.
func TestAuthCurveHoldsFailingStatus(t *testing.T) {
	t.Parallel()
	state := escalate(database.CircuitState{InstanceID: 1}, domain.HealthAuthFailure, false, 0, circuitNow, longAgoBoot)
	if got := failingWindow(state.EscalationLevel); got != failingWindowCap {
		t.Errorf("failingWindow(level %d) = %s, want the %s cap", state.EscalationLevel, got, failingWindowCap)
	}
}

// newCircuitTestAdapter builds the minimal adapter recordHealth needs — the health
// and circuit repositories over a real SQLite db plus the clock/boot-time fields the
// escalation ladder reads. Booted long ago, so the startup grace never caps.
func newCircuitTestAdapter(t *testing.T) (*indexerAdapter, *database.DB) {
	t.Helper()
	db := dbtest.OpenMigrated(t)
	return &indexerAdapter{
		instanceID:   insertTestInstance(t, db),
		db:           db,
		health:       database.Health{},
		circuit:      database.Circuit{},
		circuitLocks: &circuitLocks{},
		startedAt:    circuitNow.Add(-2 * time.Hour),
		clock:        func() time.Time { return circuitNow },
		log:          zerolog.Nop(),
	}, db
}

// TestRecordHealthEscalation exercises the adapter-level derivation in
// escalateCircuit: production failures arrive %w-wrapped, so errors.Is must see
// search.ErrGatewayStatus through the wrapping for a gateway outage to climb,
// while an ordinary wrapped transport failure stays pinned at level 1.
func TestRecordHealthEscalation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("wrapped gateway status climbs", func(t *testing.T) {
		t.Parallel()
		a, db := newCircuitTestAdapter(t)
		gateway := fmt.Errorf("registry: search %q: %w", "ptp",
			fmt.Errorf("passthepopcorn: search returned HTTP 502: %w", search.ErrGatewayStatus))
		for range 3 {
			a.recordHealth(ctx, gateway)
		}
		state, err := a.circuit.Get(ctx, db, a.instanceID)
		if err != nil {
			t.Fatalf("get circuit: %v", err)
		}
		if state.EscalationLevel != 3 {
			t.Errorf("level after 3 wrapped gateway failures = %d, want 3", state.EscalationLevel)
		}
	})

	t.Run("wrapped ordinary transport stays pinned", func(t *testing.T) {
		t.Parallel()
		a, db := newCircuitTestAdapter(t)
		transport := fmt.Errorf("registry: search %q: %w", "x",
			&url.Error{Op: "Get", URL: "https://tracker.example", Err: errors.New("connection reset by peer")})
		for range 3 {
			a.recordHealth(ctx, transport)
		}
		state, err := a.circuit.Get(ctx, db, a.instanceID)
		if err != nil {
			t.Fatalf("get circuit: %v", err)
		}
		if state.EscalationLevel != 1 {
			t.Errorf("level after 3 wrapped transport failures = %d, want pinned at 1", state.EscalationLevel)
		}
	})
}

func TestEscalateStartupGraceCapsWindow(t *testing.T) {
	t.Parallel()
	state := database.CircuitState{InstanceID: 1}
	// Climb straight to the top rung (24h) while still inside the grace window.
	for i := 0; i <= maxCircuitLevel; i++ {
		state = escalate(state, domain.HealthAuthFailure, false, 0, circuitNow, freshlyBooted)
	}
	if state.EscalationLevel != maxCircuitLevel {
		t.Fatalf("level = %d, want %d", state.EscalationLevel, maxCircuitLevel)
	}
	wantTill := circuitNow.Add(startupGraceCap)
	if !state.DisabledTill.Equal(wantTill) {
		t.Errorf("DisabledTill = %v, want capped at %v (startup grace)", state.DisabledTill, wantTill)
	}
}

func TestEscalateRetryAfterIsAFloor(t *testing.T) {
	t.Parallel()
	state := database.CircuitState{InstanceID: 1}
	// The rate-limited floor rung's own window (5m) is shorter than a 10-minute
	// Retry-After: the tracker's own instruction wins.
	state = escalate(state, domain.HealthRateLimited, false, 10*time.Minute, circuitNow, longAgoBoot)
	want := circuitNow.Add(10 * time.Minute)
	if !state.DisabledTill.Equal(want) {
		t.Errorf("DisabledTill = %v, want %v (Retry-After floor)", state.DisabledTill, want)
	}
	// A Retry-After shorter than the rung's own window never shortens it.
	state2 := escalate(database.CircuitState{InstanceID: 1}, domain.HealthRateLimited, false, time.Second, circuitNow, longAgoBoot)
	if want2 := circuitNow.Add(circuitPeriods[2]); !state2.DisabledTill.Equal(want2) {
		t.Errorf("DisabledTill = %v, want %v (ladder window, not the shorter floor)", state2.DisabledTill, want2)
	}
}

func TestRecoverCircuitDescendsOneRung(t *testing.T) {
	t.Parallel()
	state := database.CircuitState{
		InstanceID: 1, EscalationLevel: 3,
		InitialFailure: circuitNow.Add(-time.Hour), DisabledTill: circuitNow.Add(time.Hour),
	}
	state = recoverCircuit(state)
	if state.EscalationLevel != 2 {
		t.Errorf("level = %d, want 2 (descend one rung, not reset)", state.EscalationLevel)
	}
	if !state.DisabledTill.IsZero() {
		t.Error("a success must clear the current disable window")
	}
	if state.InitialFailure.IsZero() {
		t.Error("InitialFailure must stay set while the streak is still partially escalated")
	}

	// Descending from level 1 clears the failure streak marker too.
	state = database.CircuitState{InstanceID: 1, EscalationLevel: 1, InitialFailure: circuitNow}
	state = recoverCircuit(state)
	if state.EscalationLevel != 0 {
		t.Errorf("level = %d, want 0", state.EscalationLevel)
	}
	if !state.InitialFailure.IsZero() {
		t.Error("InitialFailure must clear once the ladder bottoms out")
	}

	// Already closed: a no-op.
	closed := recoverCircuit(database.CircuitState{InstanceID: 1})
	if closed.EscalationLevel != 0 || !closed.DisabledTill.IsZero() {
		t.Errorf("closed state must stay closed, got %+v", closed)
	}
}

func TestRetryAfterOf(t *testing.T) {
	t.Parallel()
	rle := &search.RateLimitedError{StatusCode: 429, RetryAfter: 7 * time.Second}
	if got := retryAfterOf(rle); got != 7*time.Second {
		t.Errorf("retryAfterOf(RateLimitedError) = %v, want 7s", got)
	}
	// Production wraps the RateLimitedError with %w (adapter.go liveSearch/Grab), so
	// retryAfterOf must extract Retry-After through the wrapping via errors.As.
	wrapped := fmt.Errorf("registry: search %q: %w", "x", rle)
	if got := retryAfterOf(wrapped); got != 7*time.Second {
		t.Errorf("retryAfterOf(%%w-wrapped RateLimitedError) = %v, want 7s", got)
	}
	// A plain error that merely stringifies the RLE (no wrap) carries no type to extract.
	flattened := errors.New("registry: search: " + rle.Error())
	if got := retryAfterOf(flattened); got != 0 {
		t.Errorf("retryAfterOf(plain error) = %v, want 0", got)
	}
	if got := retryAfterOf(nil); got != 0 {
		t.Errorf("retryAfterOf(nil) = %v, want 0", got)
	}
}

// recoverySpy captures the recovery half of the health sink. recordCircuitSuccess calls
// it synchronously, so a plain slice is enough.
type recoverySpy struct{ details []string }

func (s *recoverySpy) OnHealthEvent(context.Context, string, string, string) {}

func (s *recoverySpy) OnRecoveryEvent(_ context.Context, _, detail string) {
	s.details = append(s.details, detail)
}

// TestRecoveryNotifiesOnClearingTransitionOnly pins the recovery event to the one
// transition that means "was quarantined, now demonstrably working": the success that
// clears a disable window a failure actually set. Steady healthy traffic, a level-0
// no-op, and the rung-by-rung descent that follows must all stay silent.
func TestRecoveryNotifiesOnClearingTransitionOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, db := newCircuitTestAdapter(t)
	spy := &recoverySpy{}
	a.healthSink = spy
	a.info = core.IndexerInfo{ID: "tt"}

	// Steady healthy traffic: nothing recorded, nothing to notify.
	a.recordCircuitSuccess(ctx)
	if len(spy.details) != 0 {
		t.Fatalf("a success on a closed circuit notified %d times, want 0", len(spy.details))
	}

	// An escalated, disabled circuit whose window has just elapsed.
	escalated := database.CircuitState{
		InstanceID:      a.instanceID,
		EscalationLevel: 3,
		InitialFailure:  circuitNow.Add(-2 * time.Hour),
		DisabledTill:    circuitNow.Add(-time.Minute),
	}
	if err := a.circuit.Upsert(ctx, db, escalated); err != nil {
		t.Fatalf("upsert escalated state: %v", err)
	}

	a.recordCircuitSuccess(ctx)
	if len(spy.details) != 1 {
		t.Fatalf("the clearing success notified %d times, want 1", len(spy.details))
	}
	if !strings.Contains(spy.details[0], "2h0m0s") {
		t.Errorf("recovery detail = %q, want it to carry the 2h down-duration", spy.details[0])
	}

	// The descent continues (level 2, window already cleared) — no second message.
	a.recordCircuitSuccess(ctx)
	state, err := a.circuit.Get(ctx, db, a.instanceID)
	if err != nil {
		t.Fatalf("get circuit: %v", err)
	}
	if state.EscalationLevel != 1 {
		t.Errorf("level after two successes = %d, want 1", state.EscalationLevel)
	}
	if len(spy.details) != 1 {
		t.Errorf("the continuing descent notified %d times, want 1 in total", len(spy.details))
	}
}

func TestRecoveryDetail(t *testing.T) {
	t.Parallel()
	if got := recoveryDetail(circuitNow.Add(-90*time.Minute), circuitNow); !strings.Contains(got, "1h30m0s") {
		t.Errorf("recoveryDetail = %q, want the down-duration", got)
	}
	// No recorded start (or a clock that went backwards): still a usable message.
	if got := recoveryDetail(time.Time{}, circuitNow); got == "" || strings.Contains(got, "after") {
		t.Errorf("recoveryDetail with no InitialFailure = %q, want a duration-free message", got)
	}
}

// TestEscalateNeverDescendsOnFailure pins the deliberate asymmetry (review-confirmed
// on #419): a FAILURE never lowers a level a harsher kind earned — an indexer parked
// at the auth rungs that starts throwing ordinary transport errors must NOT collapse
// its backoff and resume presenting bad credentials on a 60s window. Levels descend
// only on success (recoverCircuit), one rung at a time.
func TestEscalateNeverDescendsOnFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	started := now.Add(-2 * time.Hour) // past the startup grace
	cur := database.CircuitState{EscalationLevel: 7, InitialFailure: now.Add(-time.Hour)}

	for _, kind := range []string{domain.HealthTransport, domain.HealthParseError, domain.HealthRateLimited} {
		next := escalate(cur, kind, false, 0, now, started)
		if next.EscalationLevel < 7 {
			t.Errorf("%s from level 7 descended to %d — failures must never lower a rung", kind, next.EscalationLevel)
		}
	}
}
