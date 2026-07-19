package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
)

// TestWarmInterval covers warmInterval's parse+clamp table: absent/invalid/
// non-positive disables (opt-in default-off), a valid value clamps into
// [warmMinInterval, warmMaxInterval].
func TestWarmInterval(t *testing.T) {
	t.Parallel()
	setting := func(v string) []domain.IndexerSetting {
		return []domain.IndexerSetting{{Name: warmIntervalSetting, Value: v}}
	}
	tests := []struct {
		name     string
		settings []domain.IndexerSetting
		want     time.Duration
		wantOK   bool
	}{
		{name: "absent", settings: nil, want: 0, wantOK: false},
		{name: "zero", settings: setting("0"), want: 0, wantOK: false},
		{name: "unparseable", settings: setting("abc"), want: 0, wantOK: false},
		{name: "negative", settings: setting("-1m"), want: 0, wantOK: false},
		{name: "below floor clamps up", settings: setting("5m"), want: warmMinInterval, wantOK: true},
		{name: "above ceiling clamps down", settings: setting("200m"), want: warmMaxInterval, wantOK: true},
		{name: "at floor as-is", settings: setting("10m"), want: 10 * time.Minute, wantOK: true},
		{name: "mid-range as-is", settings: setting("15m"), want: 15 * time.Minute, wantOK: true},
		{name: "at ceiling as-is", settings: setting("120m"), want: 120 * time.Minute, wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := warmInterval(tt.settings)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("warmInterval(%+v) = (%v, %v), want (%v, %v)", tt.settings, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestWarmerSchedule exercises the schedule-math table via a Warmer literal (no
// clock injection needed — schedule takes now explicitly), all with an injected
// clock via literal `now` values so nothing depends on wall time.
func TestWarmerSchedule(t *testing.T) {
	t.Parallel()

	t.Run("warmPhase bounds and stability", func(t *testing.T) {
		t.Parallel()
		interval := 15 * time.Minute
		seen := make(map[time.Duration]bool)
		for id := int64(1); id <= 30; id++ {
			p := warmPhase(id, interval)
			if p < 0 || p >= interval {
				t.Fatalf("warmPhase(%d, %s) = %s, want in [0, %s)", id, interval, p, interval)
			}
			if again := warmPhase(id, interval); again != p {
				t.Fatalf("warmPhase(%d, %s) = %s then %s, want stable across calls", id, interval, p, again)
			}
			seen[p] = true
		}
		if len(seen) < 2 {
			t.Fatalf("warmPhase produced only %d distinct phase(s) across 30 instance IDs, want a spread", len(seen))
		}
	})

	t.Run("first sight defers, no boot-time warm", func(t *testing.T) {
		t.Parallel()
		w := &Warmer{nextDue: make(map[int64]time.Time)}
		now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
		interval := 15 * time.Minute
		targets := []warmTarget{{instanceID: 1, slug: "alpha", interval: interval}}

		if due := w.schedule(now, targets); len(due) != 0 {
			t.Fatalf("first sight: due = %v, want none", due)
		}
		next, ok := w.nextDue[1]
		if !ok {
			t.Fatalf("first sight: nextDue not seeded")
		}
		if next.Before(now) || next.After(now.Add(interval)) {
			t.Fatalf("first sight: nextDue = %v, want within [now, now+interval] = [%v, %v]", next, now, now.Add(interval))
		}
	})

	t.Run("stagger spreads first-due times, no all-N herd", func(t *testing.T) {
		t.Parallel()
		w := &Warmer{nextDue: make(map[int64]time.Time)}
		now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
		interval := 15 * time.Minute
		targets := []warmTarget{
			{instanceID: 1, slug: "alpha", interval: interval},
			{instanceID: 7, slug: "bravo", interval: interval},
		}
		w.schedule(now, targets)
		d1, d7 := w.nextDue[1], w.nextDue[7]
		if d1.Equal(d7) {
			t.Fatalf("instances 1 and 7 got the SAME first-due time %v, want distinct (stagger failed)", d1)
		}

		// Drive ONE warmer forward minute-by-minute (consuming due state as a real
		// reap-driven tick sequence would) across several intervals' worth of ticks:
		// no single tick should ever fire both targets at once — the herd this
		// stagger exists to prevent, at the first boundary and every later one.
		for m := 1; m <= 3*int(interval/time.Minute); m++ {
			tick := now.Add(time.Duration(m) * time.Minute)
			if due := w.schedule(tick, targets); len(due) > 1 {
				t.Fatalf("tick at +%dm fired %d targets at once (%v), want at most 1", m, len(due), due)
			}
		}
	})

	t.Run("due at phase, phase preserved across later cycles", func(t *testing.T) {
		t.Parallel()
		w := &Warmer{nextDue: make(map[int64]time.Time)}
		now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
		interval := 15 * time.Minute
		targets := []warmTarget{{instanceID: 3, slug: "alpha", interval: interval}}

		w.schedule(now, targets) // seed
		firstDue := w.nextDue[3]

		if due := w.schedule(firstDue.Add(-time.Minute), targets); len(due) != 0 {
			t.Fatalf("fired before its scheduled time: due = %v", due)
		}

		due := w.schedule(firstDue, targets)
		if len(due) != 1 || due[0] != "alpha" {
			t.Fatalf("schedule at nextDue = %v, want [alpha]", due)
		}
		secondDue := w.nextDue[3]
		if !secondDue.Equal(firstDue.Add(interval)) {
			t.Fatalf("nextDue after 1st fire = %v, want firstDue+interval = %v (phase must be preserved, no drift)",
				secondDue, firstDue.Add(interval))
		}

		// A tick landing slightly past the boundary (simulating the 1-minute grid)
		// still advances from the SCHEDULED time, not from "now" — the phase survives
		// indefinitely rather than drifting forward tick by tick.
		due = w.schedule(secondDue.Add(30*time.Second), targets)
		if len(due) != 1 {
			t.Fatalf("2nd fire: due = %v, want [alpha]", due)
		}
		thirdDue := w.nextDue[3]
		if !thirdDue.Equal(secondDue.Add(interval)) {
			t.Fatalf("nextDue after 2nd fire = %v, want secondDue+interval = %v", thirdDue, secondDue.Add(interval))
		}
	})

	t.Run("suspend catch-up: fires exactly once, skips the missed backlog", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
		interval := 15 * time.Minute
		targets := []warmTarget{{instanceID: 3, slug: "alpha", interval: interval}}
		// Seed a scheduled time several intervals in the past — simulating a process
		// (laptop/VM) that was suspended past its due time, as if 6+ missed cycles had
		// piled up while nothing ticked.
		seedPhase := 3 * time.Minute // warmPhase(3, 15m) = 3 % 15 = 3
		staleDue := now.Add(-6*interval + seedPhase)
		w := &Warmer{nextDue: map[int64]time.Time{3: staleDue}}

		due := w.schedule(now, targets)
		if len(due) != 1 || due[0] != "alpha" {
			t.Fatalf("catch-up tick: due = %v, want exactly [alpha] once, not a replay of the missed backlog", due)
		}
		next := w.nextDue[3]
		if !next.After(now) {
			t.Fatalf("nextDue after catch-up = %v, want strictly after now = %v", next, now)
		}
		// Phase preserved: next must land exactly on the original staleDue lattice
		// (staleDue + k*interval for some integer k), not at an arbitrary offset.
		if got := next.Sub(staleDue) % interval; got != 0 {
			t.Fatalf("nextDue after catch-up = %v does not preserve the original phase (offset from staleDue mod interval = %v, want 0)", next, got)
		}

		// A second tick immediately after must NOT fire again — the backlog is
		// consumed in one warm, not replayed one-per-missed-interval.
		if due := w.schedule(now.Add(time.Minute), targets); len(due) != 0 {
			t.Fatalf("tick right after catch-up: due = %v, want none (no backlog replay)", due)
		}
	})

	t.Run("not due mid-window", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
		interval := 15 * time.Minute
		targets := []warmTarget{{instanceID: 1, slug: "alpha", interval: interval}}
		// A known future nextDue, set directly (independent of warmPhase's exact
		// value) so a probe well before it is unambiguously not-due.
		w := &Warmer{nextDue: map[int64]time.Time{1: now.Add(10 * time.Minute)}}

		if due := w.schedule(now.Add(5*time.Minute), targets); len(due) != 0 {
			t.Fatalf("mid-window (5m into a 10m-out due time): due = %v, want none", due)
		}
	})

	t.Run("independent per indexer", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
		targets := []warmTarget{
			{instanceID: 1, slug: "alpha", interval: 10 * time.Minute},
			{instanceID: 2, slug: "bravo", interval: 10 * time.Minute},
		}
		w := &Warmer{nextDue: map[int64]time.Time{
			1: now,                      // alpha due now
			2: now.Add(5 * time.Minute), // bravo not due yet
		}}

		due := w.schedule(now, targets)
		if len(due) != 1 || due[0] != "alpha" {
			t.Fatalf("due = %v, want only [alpha] (bravo must stay independently not-due)", due)
		}
	})

	t.Run("prunes an instance that disappeared from targets", func(t *testing.T) {
		t.Parallel()
		w := &Warmer{nextDue: make(map[int64]time.Time)}
		now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
		targets := []warmTarget{{instanceID: 1, slug: "alpha", interval: 10 * time.Minute}}
		w.schedule(now, targets)
		if _, ok := w.nextDue[1]; !ok {
			t.Fatalf("setup: instance 1 not seeded")
		}

		w.schedule(now, nil) // disabled/removed/interval cleared this tick
		if _, ok := w.nextDue[1]; ok {
			t.Fatalf("instance 1 still present in nextDue after disappearing from targets")
		}
	})

	t.Run("interval change is picked up on the next cycle", func(t *testing.T) {
		t.Parallel()
		w := &Warmer{nextDue: make(map[int64]time.Time)}
		now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
		target := warmTarget{instanceID: 1, slug: "alpha", interval: 10 * time.Minute}

		w.schedule(now, []warmTarget{target})
		firstDue := w.nextDue[1]
		w.schedule(firstDue, []warmTarget{target}) // fire once at the original 10m interval
		before := w.nextDue[1]

		target.interval = 20 * time.Minute // operator widens the interval before the next fire
		w.schedule(before, []warmTarget{target})
		after := w.nextDue[1]
		if !after.Equal(before.Add(20 * time.Minute)) {
			t.Fatalf("nextDue after interval change = %v, want before+20m = %v", after, before.Add(20*time.Minute))
		}
	})
}

// fakeWarmIndexer is a minimal core.Indexer double for warmOne: only Search is
// exercised, so every other method is a fixed stub.
type fakeWarmIndexer struct {
	searchFn func(ctx context.Context, q search.Query) ([]*normalizer.Release, error)
}

func (f *fakeWarmIndexer) Info() core.IndexerInfo             { return core.IndexerInfo{ID: "fake"} }
func (f *fakeWarmIndexer) Capabilities() *mapper.Capabilities { return &mapper.Capabilities{} }
func (f *fakeWarmIndexer) NeedsResolver() bool                { return false }
func (f *fakeWarmIndexer) DownloadNeedsAuth() bool            { return false }
func (f *fakeWarmIndexer) SupportsOffsetPaging() bool         { return false }

func (f *fakeWarmIndexer) Grab(context.Context, string) (*search.GrabResult, error) {
	return &search.GrabResult{}, nil
}

func (f *fakeWarmIndexer) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	return f.searchFn(ctx, q)
}

// TestWarmerWarmOne covers warmOne's skip/success matrix: a disabled/unresolvable
// instance never calls Search; a budget-exhausted, circuit-open, or transport
// error is a swallowed skip (no panic); a success calls Search exactly once with
// CacheBypass set and an empty query, matching the served RSS path.
func TestWarmerWarmOne(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		resolveOK  bool
		searchErr  error
		wantCalled bool
	}{
		{name: "disabled or unresolvable instance", resolveOK: false, wantCalled: false},
		{name: "budget exhausted", resolveOK: true, searchErr: errBudgetExhausted, wantCalled: true},
		{name: "circuit open", resolveOK: true, searchErr: errCircuitOpen, wantCalled: true},
		{name: "transport error", resolveOK: true, searchErr: errors.New("registry: search \"fake\": connection refused"), wantCalled: true},
		{name: "success", resolveOK: true, searchErr: nil, wantCalled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var (
				called    bool
				gotBypass bool
				gotEmpty  bool
			)
			idx := &fakeWarmIndexer{searchFn: func(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
				called = true
				gotBypass = core.CacheBypass(ctx)
				gotEmpty = isEmptyQuery(q)
				return nil, tt.searchErr
			}}
			w := &Warmer{
				resolve: func(context.Context, string) (core.Indexer, bool) {
					if !tt.resolveOK {
						return nil, false
					}
					return idx, true
				},
				log: zerolog.Nop(),
			}

			w.warmOne(context.Background(), "fake") // must never panic regardless of the error kind

			if called != tt.wantCalled {
				t.Fatalf("Search called = %v, want %v", called, tt.wantCalled)
			}
			if !tt.wantCalled {
				return
			}
			if !gotBypass {
				t.Fatalf("Search ctx did not carry core.CacheBypass — the warmer must force a live fetch+store")
			}
			if !gotEmpty {
				t.Fatalf("Search query was not classified empty (isEmptyQuery) — the warmer must drive an RSS/empty poll")
			}
		})
	}
}
