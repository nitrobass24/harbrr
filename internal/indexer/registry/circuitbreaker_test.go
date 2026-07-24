package registry

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

var (
	circuitNow    = time.Date(2026, time.June, 14, 12, 0, 0, 0, time.UTC)
	longAgoBoot   = circuitNow.Add(-1 * time.Hour) // well outside startupGrace
	freshlyBooted = circuitNow.Add(-time.Minute)   // inside startupGrace
)

func TestEscalateClimbsLadder(t *testing.T) {
	t.Parallel()
	state := database.CircuitState{InstanceID: 1}
	for level := 1; level <= maxCircuitLevel; level++ {
		state = escalate(state, domain.HealthAuthFailure, false, 0, circuitNow, longAgoBoot)
		if state.EscalationLevel != level {
			t.Fatalf("after %d failures, level = %d, want %d", level, state.EscalationLevel, level)
		}
		wantTill := circuitNow.Add(circuitPeriods[level])
		if !state.DisabledTill.Equal(wantTill) {
			t.Errorf("level %d: DisabledTill = %v, want %v", level, state.DisabledTill, wantTill)
		}
	}
	// One more qualifying failure must not climb past the top rung.
	state = escalate(state, domain.HealthAuthFailure, false, 0, circuitNow, longAgoBoot)
	if state.EscalationLevel != maxCircuitLevel {
		t.Errorf("level past top = %d, want capped at %d", state.EscalationLevel, maxCircuitLevel)
	}
}

func TestEscalateTransportDoesNotClimb(t *testing.T) {
	t.Parallel()
	state := database.CircuitState{InstanceID: 1}
	for i := 0; i < 5; i++ {
		state = escalate(state, domain.HealthTransport, false, 0, circuitNow, longAgoBoot)
		if state.EscalationLevel != 1 {
			t.Fatalf("iteration %d: transport level = %d, want pinned at 1", i, state.EscalationLevel)
		}
	}
	if want := circuitNow.Add(circuitPeriods[1]); !state.DisabledTill.Equal(want) {
		t.Errorf("DisabledTill = %v, want %v", state.DisabledTill, want)
	}
}

func TestEscalateGatewayOutageClimbs(t *testing.T) {
	t.Parallel()
	state := database.CircuitState{InstanceID: 1}
	// A gateway status is still recorded under the transport kind, but it climbs the
	// full ladder: the CDN answering 502 means the indexer's origin is down, and a
	// week-long outage must not be re-polled every 60 seconds.
	for level := 1; level <= maxCircuitLevel; level++ {
		state = escalate(state, domain.HealthTransport, true, 0, circuitNow, longAgoBoot)
		if state.EscalationLevel != level {
			t.Fatalf("after %d gateway failures, level = %d, want %d", level, state.EscalationLevel, level)
		}
	}
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
	// Level 1's own window (60s) is shorter than a 10-minute Retry-After: the floor wins.
	state = escalate(state, domain.HealthRateLimited, false, 10*time.Minute, circuitNow, longAgoBoot)
	want := circuitNow.Add(10 * time.Minute)
	if !state.DisabledTill.Equal(want) {
		t.Errorf("DisabledTill = %v, want %v (Retry-After floor)", state.DisabledTill, want)
	}
	// A Retry-After shorter than the rung's own window never shortens it.
	state2 := escalate(database.CircuitState{InstanceID: 1}, domain.HealthRateLimited, false, time.Second, circuitNow, longAgoBoot)
	if want2 := circuitNow.Add(circuitPeriods[1]); !state2.DisabledTill.Equal(want2) {
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
