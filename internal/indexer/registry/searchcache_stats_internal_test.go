package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestStatsByInstanceMergesDurableAndMemory proves the per-instance stats fold the
// durable figures, the in-memory counters, and the live breaker open-state together.
func TestStatsByInstanceMergesDurableAndMemory(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, breakerTTL, 0)
	inner := &fakeInner{releases: relSet("A")}
	idx := sc.probe(inner, instID, nil)
	ctx := context.Background()
	q := search.Query{Keywords: "a"}

	if _, err := idx.Search(ctx, q); err != nil { // miss -> stores entry
		t.Fatal(err)
	}
	if _, err := idx.Search(ctx, q); err != nil { // hit -> bumps hit_count
		t.Fatal(err)
	}

	rows, err := sc.StatsByInstance(ctx)
	if err != nil {
		t.Fatalf("StatsByInstance: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.InstanceID != instID || r.Entries != 1 || r.HitsSaved != 1 {
		t.Errorf("durable figures = %+v, want entries=1 hitsSaved=1", r)
	}
	if r.Hits != 1 || r.Misses != 1 || r.HitRatio != 0.5 {
		t.Errorf("in-memory figures = %+v, want hits=1 misses=1 ratio=0.5", r)
	}
	if r.BreakerOpenUntil != nil {
		t.Errorf("BreakerOpenUntil = %v, want nil (breaker closed)", r.BreakerOpenUntil)
	}

	// Trip the breaker (a new query that errors) and suppress one follow-up.
	inner.mu.Lock()
	inner.err = errors.New("down")
	inner.mu.Unlock()
	if _, err := idx.Search(ctx, search.Query{Keywords: "z"}); err == nil {
		t.Fatal("want trip error")
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "z"}); err == nil {
		t.Fatal("want suppressed error")
	}

	if global, err := sc.Stats(ctx); err != nil || global.BreakerSuppressed != 1 {
		t.Fatalf("global BreakerSuppressed = %d (err %v), want 1", global.BreakerSuppressed, err)
	}
	rows, err = sc.StatsByInstance(ctx)
	if err != nil {
		t.Fatalf("StatsByInstance after trip: %v", err)
	}
	r = rows[0]
	if r.BreakerOpenUntil == nil {
		t.Error("BreakerOpenUntil = nil, want an open window after trip")
	}
	if r.BreakerSuppressed != 1 {
		t.Errorf("instance BreakerSuppressed = %d, want 1", r.BreakerSuppressed)
	}
}

// TestResetCountersClearsStats proves the stats reset starts the surface from a clean
// slate — in-memory, per-instance, persisted, and every rolling window — while
// leaving the CACHED ROWS alone (discarding those is Flush's job, #369 follow-up).
func TestResetCountersClearsStats(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, breakerTTL, 0)
	inner := &fakeInner{releases: relSet("A")}
	idx := sc.probe(inner, instID, nil)
	ctx := context.Background()

	if _, err := idx.Search(ctx, search.Query{Keywords: "a"}); err != nil { // miss
		t.Fatal(err)
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "a"}); err != nil { // hit
		t.Fatal(err)
	}
	sc.FlushCounters(ctx) // persist non-zero rows so the reset provably clears them

	cleared := sc.ResetCounters(ctx)
	if cleared.Hits != 1 || cleared.Misses != 1 {
		t.Errorf("cleared = %+v, want the 1 hit / 1 miss it discarded", cleared)
	}

	stats, err := sc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Hits != 0 || stats.Misses != 0 {
		t.Errorf("after reset: hits/misses = %d/%d, want 0/0", stats.Hits, stats.Misses)
	}
	for _, w := range stats.Windows {
		if w.Hits != 0 || w.Misses != 0 {
			t.Errorf("after reset: window %+v, want zeroed", w)
		}
	}
	// The reset discards STATISTICS, not RESULTS: the cached row must survive.
	if stats.Entries != 1 {
		t.Errorf("entries after reset = %d, want 1 (the reset must not purge cached rows)", stats.Entries)
	}
	rows, err := sc.StatsByInstance(ctx)
	if err != nil {
		t.Fatalf("StatsByInstance: %v", err)
	}
	for _, r := range rows {
		if r.InstanceID == instID && (r.Hits != 0 || r.Misses != 0) {
			t.Errorf("instance row after reset = %+v, want zeroed counters", r)
		}
	}
	persisted, err := sc.counterStore.AllCounters(ctx, sc.db)
	if err != nil {
		t.Fatalf("AllCounters: %v", err)
	}
	if len(persisted) != 0 {
		t.Errorf("persisted counter rows after reset = %+v, want none", persisted)
	}
}

// TestHitsMonotoneAcrossCleanup pins autobrr/harbrr#350: the cumulative,
// restart-persisted Hits counters (global and per-instance — what the API reports as
// trackerHitsSaved/hitsSaved) must NOT drop when a cleanup tick reaps the cache row
// that earned them, even though the durable row-derived TotalHits/HitsSaved
// legitimately falls to 0 once that row is gone. An operator FLUSH is held to the
// same rule (#369 follow-up): it discards cached results, never the statistics —
// only ResetCounters does that.
func TestHitsMonotoneAcrossCleanup(t *testing.T) {
	t.Parallel()
	sc, instID, clk := testCache(t, keywordTTL, 0)
	inner := &fakeInner{releases: relSet("A")}
	idx := sc.probe(inner, instID, nil)
	ctx := context.Background()
	q := search.Query{Keywords: "a"}

	if _, err := idx.Search(ctx, q); err != nil { // miss -> stores entry
		t.Fatalf("miss: %v", err)
	}
	if _, err := idx.Search(ctx, q); err != nil { // hit -> bumps hit_count + cumulative Hits
		t.Fatalf("hit: %v", err)
	}

	before, err := sc.Stats(ctx)
	if err != nil {
		t.Fatalf("stats before cleanup: %v", err)
	}
	if before.Hits != 1 || before.TotalHits != 1 {
		t.Fatalf("before cleanup: hits=%d totalHits=%d, want 1/1", before.Hits, before.TotalHits)
	}
	rowsBefore, err := sc.StatsByInstance(ctx)
	if err != nil {
		t.Fatalf("StatsByInstance before cleanup: %v", err)
	}
	if len(rowsBefore) != 1 || rowsBefore[0].Hits != 1 || rowsBefore[0].HitsSaved != 1 {
		t.Fatalf("byInstance before cleanup = %+v, want hits=1 hitsSaved=1", rowsBefore)
	}

	// Advance past the full keyword TTL (a safe upper bound regardless of which tier
	// applied) PLUS the cleanup tick's reap grace (#343 retains expired rows for
	// cacheReapGrace before deleting them), so the tick genuinely reaps the row.
	advance(clk, keywordTTL.keyword+cacheReapGrace+time.Minute)
	if _, err := sc.CleanupExpired(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	after, err := sc.Stats(ctx)
	if err != nil {
		t.Fatalf("stats after cleanup: %v", err)
	}
	if after.Hits != 1 {
		t.Errorf("Hits after cleanup = %d, want 1 (cumulative counter must survive the reap)", after.Hits)
	}
	if after.TotalHits != 0 {
		t.Errorf("TotalHits after cleanup = %d, want 0 (row-derived; its row is gone)", after.TotalHits)
	}

	rowsAfter, err := sc.StatsByInstance(ctx)
	if err != nil {
		t.Fatalf("StatsByInstance after cleanup: %v", err)
	}
	if len(rowsAfter) != 1 {
		t.Fatalf("byInstance after cleanup = %+v, want 1 row (in-memory counters keep it visible)", rowsAfter)
	}
	if rowsAfter[0].Hits != 1 {
		t.Errorf("byInstance Hits after cleanup = %d, want 1 (cumulative counter must survive the reap)", rowsAfter[0].Hits)
	}
	if rowsAfter[0].HitsSaved != 0 {
		t.Errorf("byInstance HitsSaved after cleanup = %d, want 0 (row-derived; its row is gone)", rowsAfter[0].HitsSaved)
	}

	// A flush is likewise result-only: the cumulative counters and the rolling
	// windows must survive it untouched.
	if _, err := sc.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	flushed, err := sc.Stats(ctx)
	if err != nil {
		t.Fatalf("stats after flush: %v", err)
	}
	if flushed.Entries != 0 {
		t.Errorf("Entries after flush = %d, want 0", flushed.Entries)
	}
	if flushed.Hits != 1 || flushed.Misses != 1 {
		t.Errorf("hits/misses after flush = %d/%d, want 1/1 (a flush must not reset the counters)",
			flushed.Hits, flushed.Misses)
	}
	// The clock has advanced past 24h by now, so read the 30d view — still resident,
	// and the point is that the flush did not empty it.
	if w := windowByHours(t, flushed.Windows, monthHours); w.Hits != 1 || w.Misses != 1 {
		t.Errorf("30d window after flush = %+v, want hits=1 misses=1 (a flush must not reset it)", w)
	}
}
