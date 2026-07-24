package registry

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestForgetInstancesEvictsPerInstanceState is the registry-level fallback for the
// #346 backup-restore fix (see backup_handlers.go's importBackup): ForgetInstances
// must run the same per-instance eviction fan-out Manager.Delete performs, for every
// pre-restore id, so nothing keyed to a wiped-and-possibly-recycled id survives.
//
// This base does not (yet) clear the negative breaker on invalidate/forget — that
// landed separately (autobrr/harbrr#345/#352) outside this branch's ancestry — so
// this test only covers the four seams ForgetInstances actually runs here: the
// search-cache epoch bump (drops a stale write-back), cache counters, stats, and
// budget state.
func TestForgetInstancesEvictsPerInstanceState(t *testing.T) {
	t.Parallel()
	sc, instID, clk := testCache(t, keywordTTL, 0)
	stats := newIndexerStats(sc.db, sc.clock, zerolog.Nop())
	budget := newRequestBudget(sc.db, sc.clock, zerolog.Nop())
	r := &Resolver{searchCache: sc, stats: stats, budget: budget, log: zerolog.Nop()}
	ctx := context.Background()

	// Prime: one live search through a probe that snapshots builtEpoch (0) BEFORE
	// Forget runs — the same probe replays the write-back attempt below. It also
	// stores a row (epoch matches at this point) and bumps the cache-miss counter.
	inner := &fakeInner{releases: relSet("Alpha")}
	probe := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "x"}
	if _, err := probe.Search(ctx, q); err != nil {
		t.Fatalf("prime search: %v", err)
	}
	if ic := sc.counters(instID); ic.misses.Load() != 1 {
		t.Fatalf("prime miss count = %d, want 1", ic.misses.Load())
	}
	key := buildSearchCacheKey(instID, q, false)
	if _, found, err := sc.store.Fetch(ctx, sc.db, key, sc.clock()); err != nil || !found {
		t.Fatalf("prime search did not store: found=%v err=%v", found, err)
	}

	// Prime stats + budget counters for the same instance.
	stats.RecordQuery(instID, 5*time.Millisecond)
	budget.ReserveQuery(ctx, instID, nil, *clk.Load())
	if queries, _, _, _, _ := stats.snapshot(instID); queries != 1 {
		t.Fatalf("prime stats queries = %d, want 1", queries)
	}
	if _, ok := budget.states.Load(instID); !ok {
		t.Fatal("prime budget reserve did not create in-memory state")
	}

	r.ForgetInstances(ctx, instID)

	// Cache counters, stats, and budget state no longer report the id.
	if ic := sc.counters(instID); ic.hits.Load() != 0 || ic.misses.Load() != 0 || ic.suppressed.Load() != 0 {
		t.Errorf("cache counters survived Forget: hits=%d misses=%d suppressed=%d",
			ic.hits.Load(), ic.misses.Load(), ic.suppressed.Load())
	}
	if queries, _, _, _, _ := stats.snapshot(instID); queries != 0 {
		t.Errorf("stats snapshot survived Forget: queries = %d, want 0", queries)
	}
	if _, ok := budget.states.Load(instID); ok {
		t.Error("budget state survived Forget")
	}
	// The prior row is gone (InvalidateByInstance purges on top of the epoch bump).
	if _, found, err := sc.store.Fetch(ctx, sc.db, key, sc.clock()); err != nil || found {
		t.Errorf("cache row survived Forget: found=%v err=%v", found, err)
	}

	// A storeBestEffort under the OLD builtEpoch (the probe captured epoch 0 above,
	// before Forget bumped it) is dropped rather than resurrecting a stale-config
	// entry — proving a write-back from an adapter built before a restore/delete can
	// never land under the id it forgot.
	if _, err := probe.Search(ctx, q); err != nil {
		t.Fatalf("post-forget search (old adapter): %v", err)
	}
	if _, found, err := sc.store.Fetch(ctx, sc.db, key, sc.clock()); err != nil || found {
		t.Fatal("BUG: a write-back from an adapter built before Forget was stored after it")
	}

	// Fence: a FRESH adapter built after Forget (current epoch) still caches
	// normally — eviction does not permanently wedge the instance.
	fresh := sc.probe(&fakeInner{releases: relSet("Beta")}, instID, nil)
	if _, err := fresh.Search(ctx, q); err != nil {
		t.Fatalf("post-forget search (fresh adapter): %v", err)
	}
	if _, found, err := sc.store.Fetch(ctx, sc.db, key, sc.clock()); err != nil || !found {
		t.Fatalf("fresh adapter failed to cache after Forget: found=%v err=%v", found, err)
	}
}
