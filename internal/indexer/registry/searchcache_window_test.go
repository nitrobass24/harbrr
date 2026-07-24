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
