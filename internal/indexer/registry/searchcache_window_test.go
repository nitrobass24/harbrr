package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestHourWindowRolls proves the 24h ring counts the last 24 hours only: counts
// older than the window fall out, and a slot reused for a new hour is zeroed.
func TestHourWindowRolls(t *testing.T) {
	t.Parallel()
	var w hourWindow
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	w.hit(now)
	w.hit(now)
	w.miss(now)
	if h, m := w.totals(now); h != 2 || m != 1 {
		t.Fatalf("totals = %d/%d, want 2/1", h, m)
	}
	// Still inside the window 23h later.
	if h, m := w.totals(now.Add(23 * time.Hour)); h != 2 || m != 1 {
		t.Errorf("totals at +23h = %d/%d, want 2/1", h, m)
	}
	// Fully outside 25h later.
	if h, m := w.totals(now.Add(25 * time.Hour)); h != 0 || m != 0 {
		t.Errorf("totals at +25h = %d/%d, want 0/0", h, m)
	}
	// A hit exactly one ring-cycle later reuses the old slot and must not inherit
	// its counts.
	later := now.Add(time.Duration(len(w.buckets)) * time.Hour)
	w.hit(later)
	if h, m := w.totals(later); h != 1 || m != 0 {
		t.Errorf("totals after slot reuse = %d/%d, want 1/0", h, m)
	}
	w.reset()
	if h, m := w.totals(later); h != 0 || m != 0 {
		t.Errorf("totals after reset = %d/%d, want 0/0", h, m)
	}
}

// TestHourWindowKeepsPartialBoundaryBucket pins the window's boundary rule: an
// event still inside the trailing 24h must be counted even though its bucket only
// partially overlaps the window (hour granularity never undercounts — the view
// spans up to one extra partial hour instead).
func TestHourWindowKeepsPartialBoundaryBucket(t *testing.T) {
	t.Parallel()
	var w hourWindow
	event := time.Date(2026, 7, 24, 12, 59, 0, 0, time.UTC)
	w.hit(event)
	// 23h31m later (12:30 next day): the event is 23.5h old — inside the trailing
	// 24h — but its 12:00 bucket only partially overlaps the window.
	if h, _ := w.totals(event.Add(23*time.Hour + 31*time.Minute)); h != 1 {
		t.Errorf("hits 23.5h after a mid-hour event = %d, want 1", h)
	}
	// 24h31m later the whole bucket is outside the window.
	if h, _ := w.totals(event.Add(24*time.Hour + 31*time.Minute)); h != 0 {
		t.Errorf("hits 24.5h after a mid-hour event = %d, want 0", h)
	}
}

// TestFlushDuringInFlightMissNeverGoesNegative pins the serveMiss/Flush race: a
// flush landing while a miss is in flight sweeps the up-front miss increment to
// zero, so the request's own error rollback must be skipped — without the
// flush-epoch guard the counters would go negative.
func TestFlushDuringInFlightMissNeverGoesNegative(t *testing.T) {
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
	if _, err := sc.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	close(gate) // release the live search to fail
	if err := <-done; err == nil {
		t.Fatal("want search error")
	}

	stats, err := sc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Hits != 0 || stats.Misses != 0 {
		t.Errorf("counters after flush swept an in-flight miss = %d/%d, want 0/0 (never negative)",
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
	if stats.Hits24h != 0 || stats.Misses24h != 0 {
		t.Errorf("after failed search: 24h hits/misses = %d/%d, want 0/0", stats.Hits24h, stats.Misses24h)
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
	if stats.Hits24h != 1 || stats.Misses24h != 1 {
		t.Errorf("after recovery: 24h hits/misses = %d/%d, want 1/1", stats.Hits24h, stats.Misses24h)
	}
}
