package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
)

// hookInner is a core.Indexer test double that runs onSearch synchronously
// inside its (single) Search call, before returning releases. It lets a test land a
// config-mutation purge DURING the live fetch — exactly the U8R-F4 window where a
// store from an old-config engine would otherwise resurrect a purged entry.
type hookInner struct {
	releases []*normalizer.Release
	onSearch func()
}

func (h *hookInner) Info() core.IndexerInfo             { return core.IndexerInfo{ID: "hook"} }
func (h *hookInner) Capabilities() *mapper.Capabilities { return &mapper.Capabilities{} }
func (h *hookInner) NeedsResolver() bool                { return false }
func (h *hookInner) DownloadNeedsAuth() bool            { return false }
func (h *hookInner) SupportsOffsetPaging() bool         { return false }

func (h *hookInner) Grab(context.Context, string) (*search.GrabResult, error) {
	return nil, errors.New("not implemented")
}

func (h *hookInner) Search(_ context.Context, _ search.Query) ([]*normalizer.Release, error) {
	if h.onSearch != nil {
		h.onSearch()
	}
	return h.releases, nil
}

// TestMissStoreSkippedWhenInstanceInvalidatedDuringFetch is the U8R-F4 regression for
// the in-flight MISS path. A decorator is built at epoch N; its live fetch triggers a
// config-mutation purge (bumping the instance epoch to N+1) mid-flight; when the miss
// completes, storeBestEffort must DROP the write-back so the purged cache stays empty
// rather than resurrecting the old-config result served until TTL.
//
// FAIL-BEFORE (unconditional store): storeBestEffort writes the entry after the purge,
// so Fetch finds it. PASS-AFTER (epoch gate): the store is skipped and Fetch misses.
func TestMissStoreSkippedWhenInstanceInvalidatedDuringFetch(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)

	// Six distinct releases so the entry takes the full keyword TTL (not the thin clamp).
	releases := relSet("a1", "a2", "a3", "a4", "a5", "a6")
	inner := &hookInner{
		releases: releases,
		onSearch: func() {
			// A config mutation lands DURING the live fetch: purge + epoch bump (0 -> 1).
			if _, err := sc.InvalidateByInstance(context.Background(), instID); err != nil {
				t.Errorf("invalidate: %v", err)
			}
		},
	}
	idx := sc.probe(inner, instID, nil) // captures builtEpoch = 0
	q := search.Query{Keywords: "matrix"}

	// The user's search still succeeds with the live result — degrade-open is intact.
	got, err := idx.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 6 || got[0].Title != "a1" {
		t.Fatalf("live search served %+v, want the 6 live releases", got)
	}

	// The write-back must have been SKIPPED: the decorator was built at epoch 0 and the
	// purge advanced the instance epoch to 1 before the store. No entry may remain.
	assertNoCacheEntry(t, sc, instID, q)
}

// TestSWRStoreSkippedWhenInstanceInvalidatedDuringRefresh is the U8R-F4 regression for
// the detached SWR refresh path. A stale hit fires a background refresh through an
// old-epoch decorator; a config purge lands while that refresh is in flight; the
// refresh's write-back must be dropped so it cannot re-populate the just-purged cache.
func TestSWRStoreSkippedWhenInstanceInvalidatedDuringRefresh(t *testing.T) {
	t.Parallel()
	sc, instID, clk := testCache(t, keywordTTL, 80) // refresh-ahead at 80% of the 30m TTL

	primeSet := relSet("p1", "p2", "p3", "p4", "p5", "p6")
	refreshSet := relSet("r1", "r2", "r3", "r4", "r5", "r6")

	// Prime the cache through a plain indexer (builtEpoch 0), storing a full-TTL entry.
	primer := &fakeInner{releases: primeSet}
	q := search.Query{Keywords: "swr"}
	if _, err := sc.probe(primer, instID, nil).Search(context.Background(), q); err != nil {
		t.Fatalf("prime: %v", err)
	}
	key := buildSearchCacheKey(instID, q, false)

	// Gated indexer for the refresh: its first (and only) Search blocks on the gate so
	// we can land the purge while the refresh is genuinely in flight.
	gate := make(chan struct{})
	refresher := &fakeInner{releases: refreshSet, gate: gate, firstSeen: make(chan struct{})}
	idx := sc.probe(refresher, instID, nil) // captures builtEpoch = 0

	// Advance past the 80% refresh threshold (but before expiry) and take a hit: it
	// serves the cached prime value and fires the gated SWR refresh in the background.
	advance(clk, 25*time.Minute)
	hit, err := idx.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("stale hit: %v", err)
	}
	if len(hit) != 6 || hit[0].Title != "p1" {
		t.Fatalf("stale hit served %+v, want cached prime", hit)
	}

	// Wait until the SWR refresh has entered the gated live Search (flight is registered).
	select {
	case <-refresher.firstSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("SWR refresh never started")
	}

	// A config mutation lands while the refresh is gated: purge the prime entry and bump
	// the epoch (0 -> 1). The decorator that owns the refresh was built at epoch 0.
	if _, err := sc.InvalidateByInstance(context.Background(), instID); err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	// Release the refresh; it fetches refreshSet and reaches storeBestEffort.
	close(gate)

	// Barrier: coalesce onto the SWR singleflight key so this returns only once the
	// refresh flight (and thus its store attempt) has completed — fully deterministic.
	_, _, _ = sc.sf.Do(swrKey(key), func() (any, error) { return struct{}{}, nil })

	// The refresh's write-back must have been SKIPPED (built at epoch 0, live epoch 1),
	// leaving the purged cache empty rather than resurrecting a stale-config entry.
	assertNoCacheEntry(t, sc, instID, q)
}

// TestStoreProceedsWhenEpochUnchanged is the happy-path guard: with no intervening
// invalidation the built epoch still matches, so the write-back proceeds and the entry
// is present. It fences the gate against a regression that skips every store.
func TestStoreProceedsWhenEpochUnchanged(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)

	inner := &fakeInner{releases: relSet("a1", "a2", "a3", "a4", "a5", "a6")}
	idx := sc.probe(inner, instID, nil)
	q := search.Query{Keywords: "matrix"}
	if _, err := idx.Search(context.Background(), q); err != nil {
		t.Fatalf("search: %v", err)
	}

	key := buildSearchCacheKey(instID, q, false)
	_, found, err := sc.store.Fetch(context.Background(), sc.db, key, sc.clock())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !found {
		t.Fatal("epoch unchanged: store should have written back, but no entry present")
	}
}

// assertNoCacheEntry fails if any (even expired) cache entry exists for q's key on the
// instance — proving the epoch-gated store was skipped and nothing was resurrected.
func assertNoCacheEntry(t *testing.T, sc *SearchCache, instID int64, q search.Query) {
	t.Helper()
	key := buildSearchCacheKey(instID, q, false)
	_, found, err := sc.store.Fetch(context.Background(), sc.db, key, sc.clock())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if found {
		t.Fatal("BUG: an old-config result was stored after the config purge (stale entry resurrected)")
	}
}
