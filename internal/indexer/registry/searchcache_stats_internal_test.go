package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestStatsByInstanceMergesDurableAndMemory proves the per-instance stats fold the
// durable figures, the in-memory counters, and the live breaker open-state together.
func TestStatsByInstanceMergesDurableAndMemory(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, breakerTTL, 0)
	inner := &fakeInner{releases: relSet("A")}
	idx := sc.wrap(inner, instID, nil)
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
	idx := sc.wrap(inner, instID, nil)
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
