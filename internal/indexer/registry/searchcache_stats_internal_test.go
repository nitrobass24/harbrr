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

// TestStatsByInstanceReportsFlushedInstance proves an instance with in-memory traffic
// but no remaining durable entries (cache flushed) still appears, with Entries=0.
func TestStatsByInstanceReportsFlushedInstance(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, breakerTTL, 0)
	inner := &fakeInner{releases: relSet("A")}
	idx := sc.probe(inner, instID, nil)
	ctx := context.Background()

	if _, err := idx.Search(ctx, search.Query{Keywords: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rows, err := sc.StatsByInstance(ctx)
	if err != nil {
		t.Fatalf("StatsByInstance: %v", err)
	}
	if len(rows) != 1 || rows[0].InstanceID != instID {
		t.Fatalf("rows = %+v, want the flushed instance from in-memory counters", rows)
	}
	if rows[0].Entries != 0 || rows[0].Misses != 1 {
		t.Errorf("flushed instance = %+v, want entries=0 misses=1", rows[0])
	}
}

// TestHitsMonotoneAcrossCleanup pins autobrr/harbrr#350: the cumulative,
// restart-persisted Hits counters (global and per-instance — what the API reports as
// trackerHitsSaved/hitsSaved) must NOT drop when a cleanup tick reaps the cache row
// that earned them, even though the durable row-derived TotalHits/HitsSaved
// legitimately falls to 0 once that row is gone.
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

	// Advance well past even the full keyword TTL (the thin-result clamp shortens it
	// further, so this is a safe upper bound regardless of which tier applied) and
	// reap the now-expired entry.
	advance(clk, keywordTTL.keyword+time.Minute)
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
}
