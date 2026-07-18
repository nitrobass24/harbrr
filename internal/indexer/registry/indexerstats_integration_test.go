package registry_test

import (
	"context"
	stdhttp "net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// TestStatsCountsQueryAndFailure proves the adapter instrumentation: a search that
// reaches the tracker increments the query counter (even when it fails), records a
// latency sample, and — for a classified failure — folds the health failure count into
// Stats. A 503 classifies as rate_limited.
func TestStatsCountsQueryAndFailure(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t, statusDoer{status: stdhttp.StatusServiceUnavailable})
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	// Two calls: the first reaches the tracker and counts as a query attempt (a
	// classified 503 -> rate_limited failure). The second is gated by the #253
	// circuit breaker, which the first failure just escalated — it must NOT reach
	// the tracker again, so both counters stay at exactly one.
	for i := 0; i < 2; i++ {
		if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err == nil {
			t.Fatal("Search unexpectedly succeeded")
		}
	}

	st, err := reg.Stats(ctx, "tt")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Queries != 1 {
		t.Errorf("queries = %d, want 1 (a failed search still counts, but the second search was gated)", st.Queries)
	}
	if st.Grabs != 0 {
		t.Errorf("grabs = %d, want 0", st.Grabs)
	}
	if st.Failures.RateLimited != 1 {
		t.Errorf("failures.rateLimited = %d, want 1 (the gated second search records no new event)", st.Failures.RateLimited)
	}
	if st.LastFailureAt.IsZero() {
		t.Error("lastFailureAt is zero, want the recorded failure time")
	}
	if st.LastQueryAt.IsZero() {
		t.Error("lastQueryAt is zero, want the recorded query time")
	}
}

// TestStatsUnknownSlug: Stats for a missing indexer is ErrNotFound (404 at the API).
func TestStatsUnknownSlug(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t, statusDoer{status: stdhttp.StatusOK})
	if _, err := reg.Stats(context.Background(), "nope"); err == nil {
		t.Fatal("Stats(nope) succeeded, want ErrNotFound")
	}
}

// TestAllStatsCoversEveryInstance proves AllStats returns a row per configured instance,
// including one that has never been queried (zeroed counters, no timestamps).
func TestAllStatsCoversEveryInstance(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t, statusDoer{status: stdhttp.StatusServiceUnavailable})
	ctx := context.Background()
	for _, slug := range []string{"a", "b"} {
		if _, err := reg.Add(ctx, registry.AddParams{
			Slug: slug, DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
		}); err != nil {
			t.Fatalf("Add %q: %v", slug, err)
		}
	}
	// Query only "a"; "b" stays never-queried.
	idx, _ := reg.Indexer(ctx, "a")
	_, _ = idx.Search(ctx, search.Query{Keywords: "x"})

	all, err := reg.AllStats(ctx)
	if err != nil {
		t.Fatalf("AllStats: %v", err)
	}
	byslug := map[string]registry.IndexerStat{}
	for _, st := range all {
		byslug[st.Slug] = st
	}
	if len(byslug) != 2 {
		t.Fatalf("AllStats returned %d instances, want 2", len(byslug))
	}
	if byslug["a"].Queries != 1 {
		t.Errorf("a queries = %d, want 1", byslug["a"].Queries)
	}
	if b := byslug["b"]; b.Queries != 0 || !b.LastQueryAt.IsZero() {
		t.Errorf("b = queries %d / lastQuery %v, want 0 / zero (never queried)", b.Queries, b.LastQueryAt)
	}
}
