package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// windowByHours picks one view out of a Stats().Windows slice (0 hours = all-time),
// so assertions name the window they mean instead of indexing by position.
func windowByHours(t *testing.T, ws []StatsWindow, hours int) StatsWindow {
	t.Helper()
	for _, w := range ws {
		if w.Hours == hours {
			return w
		}
	}
	t.Fatalf("no %dh window in %+v", hours, ws)
	return StatsWindow{}
}

// TestHourWindowRolls proves the ring counts the last `hours` hours only: counts
// older than the window fall out, and a slot reused for a new hour is zeroed.
func TestHourWindowRolls(t *testing.T) {
	t.Parallel()
	var w hourWindow
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	w.hit(now)
	w.hit(now)
	w.miss(now)
	if h, m := w.totals(now, dayHours); h != 2 || m != 1 {
		t.Fatalf("totals = %d/%d, want 2/1", h, m)
	}
	// Still inside the 24h window 23h later.
	if h, m := w.totals(now.Add(23*time.Hour), dayHours); h != 2 || m != 1 {
		t.Errorf("totals at +23h = %d/%d, want 2/1", h, m)
	}
	// Fully outside the 24h window 25h later — but the wider views still hold it.
	if h, m := w.totals(now.Add(25*time.Hour), dayHours); h != 0 || m != 0 {
		t.Errorf("24h totals at +25h = %d/%d, want 0/0", h, m)
	}
	if h, m := w.totals(now.Add(25*time.Hour), weekHours); h != 2 || m != 1 {
		t.Errorf("7d totals at +25h = %d/%d, want 2/1 (still inside the wider view)", h, m)
	}
	// A hit exactly one ring-cycle later reuses the old slot and must not inherit
	// its counts.
	later := now.Add(time.Duration(len(w.buckets)) * time.Hour)
	w.hit(later)
	if h, m := w.totals(later, monthHours); h != 1 || m != 0 {
		t.Errorf("totals after slot reuse = %d/%d, want 1/0", h, m)
	}
	w.reset(later)
	if h, m := w.totals(later, monthHours); h != 0 || m != 0 {
		t.Errorf("totals after reset = %d/%d, want 0/0", h, m)
	}
	if !w.coverageSince().Equal(later) {
		t.Errorf("coverageSince after reset = %v, want %v", w.coverageSince(), later)
	}
}

// TestHourWindowSizes tables the three bucketed views over one seeded ring: an event
// lands in exactly the windows that still cover it, and in none of the narrower ones.
func TestHourWindowSizes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	var w hourWindow
	w.reset(now.Add(-31 * 24 * time.Hour)) // a long-running process: coverage is not the limit here
	w.hit(now.Add(-2 * time.Hour))         // inside 1d, 7d, 30d
	w.hit(now.Add(-3 * 24 * time.Hour))    // inside 7d, 30d
	w.hit(now.Add(-20 * 24 * time.Hour))   // inside 30d only
	w.hit(now.Add(-40 * 24 * time.Hour))   // outside every window

	for _, tc := range []struct {
		name  string
		hours int
		want  int64
	}{
		{"1d", dayHours, 1},
		{"7d", weekHours, 2},
		{"30d", monthHours, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if h, _ := w.totals(now, tc.hours); h != tc.want {
				t.Errorf("%s hits = %d, want %d", tc.name, h, tc.want)
			}
		})
	}
}

// TestHourWindowKeepsPartialBoundaryBucket pins the window's boundary rule at EVERY
// size: an event still inside the trailing window must be counted even though its
// bucket only partially overlaps it (hour granularity never undercounts — the view
// spans up to one extra partial hour instead).
func TestHourWindowKeepsPartialBoundaryBucket(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		hours int
	}{
		{"1d", dayHours},
		{"7d", weekHours},
		{"30d", monthHours},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var w hourWindow
			event := time.Date(2026, 7, 24, 12, 59, 0, 0, time.UTC)
			w.reset(event)
			w.hit(event)
			span := time.Duration(tc.hours) * time.Hour
			// 29 minutes shy of the window's end: the event is still inside it, but
			// its 12:00 bucket only partially overlaps — it must still count.
			if h, _ := w.totals(event.Add(span-29*time.Minute), tc.hours); h != 1 {
				t.Errorf("hits just inside the %s window = %d, want 1", tc.name, h)
			}
			// 31 minutes past the window's end the whole bucket is outside it.
			if h, _ := w.totals(event.Add(span+31*time.Minute), tc.hours); h != 0 {
				t.Errorf("hits just outside the %s window = %d, want 0", tc.name, h)
			}
		})
	}
}

// TestAllTimeWindowSurvivesBucketEviction pins the one view that is NOT a bucket
// sum: after the clock rolls past the widest window the bucketed views empty, but
// all-time still reads the cumulative counters (which is also why it, alone,
// survives a restart). It also pins the coverage honesty: a window longer than the
// process has been up must be reported as such, never presented as a full period.
func TestAllTimeWindowSurvivesBucketEviction(t *testing.T) {
	t.Parallel()
	sc, instID, clk := testCache(t, keywordTTL, 0)
	inner := &fakeInner{releases: relSet("A")}
	idx := sc.probe(inner, instID, nil)
	ctx := context.Background()
	q := search.Query{Keywords: "a"}

	start := sc.clock()
	for i := range 2 { // miss, then hit
		if _, err := idx.Search(ctx, q); err != nil {
			t.Fatalf("search %d: %v", i, err)
		}
	}

	fresh, err := sc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	// Coverage starts at construction, so right now every window is only minutes
	// deep — the surface must be able to say so rather than imply 30 days.
	if !fresh.WindowsSince.Equal(start) {
		t.Errorf("WindowsSince = %v, want the cache's construction instant %v", fresh.WindowsSince, start)
	}
	if covered := sc.clock().Sub(fresh.WindowsSince); covered >= monthHours*time.Hour {
		t.Errorf("coverage = %v on a just-built cache, want far less than the 30d window", covered)
	}

	advance(clk, (monthHours+2)*time.Hour) // roll past the widest bucketed window
	aged, err := sc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats after aging: %v", err)
	}
	for _, hours := range []int{dayHours, weekHours, monthHours} {
		if w := windowByHours(t, aged.Windows, hours); w.Hits != 0 || w.Misses != 0 {
			t.Errorf("bucketed window %+v after aging past 30d, want emptied", w)
		}
	}
	all := windowByHours(t, aged.Windows, 0)
	if all.Hits != 1 || all.Misses != 1 {
		t.Errorf("all-time window = %+v, want hits=1 misses=1 (cumulative, immune to eviction)", all)
	}
	if all.Hits != aged.Hits || all.Misses != aged.Misses {
		t.Errorf("all-time window %+v must mirror the cumulative counters %d/%d",
			all, aged.Hits, aged.Misses)
	}
	// The coverage clock does not move with the ring: it still points at construction.
	if !aged.WindowsSince.Equal(start) {
		t.Errorf("WindowsSince after aging = %v, want the unchanged %v", aged.WindowsSince, start)
	}
}

// TestResetRestartsWindowCoverage proves a stats reset restarts the coverage clock:
// after it, the bucketed views legitimately hold nothing and must not claim to reach
// back before the reset.
func TestResetRestartsWindowCoverage(t *testing.T) {
	t.Parallel()
	sc, instID, clk := testCache(t, keywordTTL, 0)
	inner := &fakeInner{releases: relSet("A")}
	idx := sc.probe(inner, instID, nil)
	ctx := context.Background()

	if _, err := idx.Search(ctx, search.Query{Keywords: "a"}); err != nil {
		t.Fatal(err)
	}
	advance(clk, 3*time.Hour)
	resetAt := sc.clock()
	sc.ResetCounters(ctx)

	stats, err := sc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if !stats.WindowsSince.Equal(resetAt) {
		t.Errorf("WindowsSince = %v, want the reset instant %v", stats.WindowsSince, resetAt)
	}
}

// TestResetDuringInFlightMissNeverGoesNegative pins the serveMiss/ResetCounters
// race: a reset landing while a miss is in flight sweeps the up-front miss increment
// to zero, so the request's own error rollback must be skipped — without the epoch
// guard the counters would go negative. (The reset moved off Flush in the #369
// follow-up; this test follows it to the new entry point.)
func TestResetDuringInFlightMissNeverGoesNegative(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	gate := make(chan struct{})
	firstSeen := make(chan struct{})
	inner := &fakeInner{err: errors.New("down"), gate: gate, firstSeen: firstSeen}
	idx := sc.probe(inner, instID, nil)
	ctx := context.Background()

	done := make(chan error, 1)
	go func() {
		_, err := idx.Search(ctx, search.Query{Keywords: "a"})
		done <- err
	}()
	<-firstSeen // the miss increment has landed; the live search is blocked
	sc.ResetCounters(ctx)
	close(gate) // release the live search to fail
	if err := <-done; err == nil {
		t.Fatal("want search error")
	}

	stats, err := sc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Hits != 0 || stats.Misses != 0 {
		t.Errorf("counters after a reset swept an in-flight miss = %d/%d, want 0/0 (never negative)",
			stats.Hits, stats.Misses)
	}
}

// TestFailedSearchCountsAsNeitherHitNorMiss proves a live search that errors leaves
// the hit/miss counters untouched: the ratio measures cache effectiveness, and a
// dead tracker (e.g. days of gateway 502s) must not drag it toward zero.
func TestFailedSearchCountsAsNeitherHitNorMiss(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	inner := &fakeInner{err: errors.New("origin unreachable")}
	idx := sc.probe(inner, instID, nil)
	ctx := context.Background()
	q := search.Query{Keywords: "a"}

	if _, err := idx.Search(ctx, q); err == nil {
		t.Fatal("want search error")
	}
	stats, err := sc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Hits != 0 || stats.Misses != 0 {
		t.Errorf("after failed search: hits/misses = %d/%d, want 0/0", stats.Hits, stats.Misses)
	}
	if w := windowByHours(t, stats.Windows, dayHours); w.Hits != 0 || w.Misses != 0 {
		t.Errorf("after failed search: 1d window = %+v, want 0/0", w)
	}

	// A recovered tracker counts normally again: one miss, then one hit.
	inner.mu.Lock()
	inner.err = nil
	inner.releases = relSet("A")
	inner.mu.Unlock()
	for i := range 2 {
		if _, err := idx.Search(ctx, q); err != nil {
			t.Fatalf("search %d: %v", i, err)
		}
	}
	stats, err = sc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Hits != 1 || stats.Misses != 1 {
		t.Errorf("after recovery: hits/misses = %d/%d, want 1/1", stats.Hits, stats.Misses)
	}
	if w := windowByHours(t, stats.Windows, dayHours); w.Hits != 1 || w.Misses != 1 {
		t.Errorf("after recovery: 1d window = %+v, want 1/1", w)
	}
}
