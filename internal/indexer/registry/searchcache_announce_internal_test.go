package registry

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	tzn "github.com/autobrr/harbrr/internal/torznab"
)

func relWithGUID(guid string) *normalizer.Release {
	return &normalizer.Release{Title: guid, GUID: guid}
}

// TestAnnounceTap proves the cache write-back announce source: an RSS/empty-query fill
// announces only the genuinely-new GUIDs (diffed against the prior cached entry + the
// dedup window), a keyword search announces nothing, and a release seen before is not
// re-announced even under a different key.
func TestAnnounceTap(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)

	var got [][]string
	sc.SetAnnounceSink(func(_ context.Context, id int64, fresh []*normalizer.Release) {
		if id != instID {
			t.Errorf("instanceID = %d, want %d", id, instID)
		}
		guids := make([]string, 0, len(fresh))
		for _, r := range fresh {
			guids = append(guids, tzn.GUIDFor(r))
		}
		got = append(got, guids)
	})

	ctx := context.Background()
	cfg := map[string]string{}
	empty := search.Query{}
	keyword := search.Query{Keywords: "matrix"}

	// 1. first empty-query fill: every release is new.
	sc.storeBestEffort(ctx, instID, cfg, empty, "k1", []*normalizer.Release{relWithGUID("A"), relWithGUID("B")})
	// 2. same key gains C: only C is new (A, B are in the prior entry + the dedup window).
	sc.storeBestEffort(ctx, instID, cfg, empty, "k1", []*normalizer.Release{relWithGUID("A"), relWithGUID("B"), relWithGUID("C")})
	// 3. a keyword search is never announced (only what a consumer already RSS-polls).
	sc.storeBestEffort(ctx, instID, cfg, keyword, "k2", []*normalizer.Release{relWithGUID("D")})
	// 4. A reappears under a different RSS key: the dedup window suppresses the re-announce.
	sc.storeBestEffort(ctx, instID, cfg, empty, "k3", []*normalizer.Release{relWithGUID("A")})

	if len(got) != 2 {
		t.Fatalf("announce calls = %d, want 2 (fills 1 and 2 only): %v", len(got), got)
	}
	if !slices.Equal(got[0], []string{"A", "B"}) {
		t.Errorf("first announce = %v, want [A B]", got[0])
	}
	if !slices.Equal(got[1], []string{"C"}) {
		t.Errorf("second announce = %v, want [C]", got[1])
	}
}

// TestAnnounceTap_DiffsAcrossExpiry proves the prior-GUID diff still works after the prior
// cache entry has EXPIRED — the request miss path, where Fetch (which filters on expiry)
// would return nothing. priorGUIDs uses FetchAny, so the expired-but-present entry still
// suppresses already-seen releases (and, since that entry survives a restart, prevents a
// restart re-announce storm).
func TestAnnounceTap_DiffsAcrossExpiry(t *testing.T) {
	t.Parallel()
	sc, instID, clk := testCache(t, keywordTTL, 0) // rss TTL = 5m

	var got [][]string
	sc.SetAnnounceSink(func(_ context.Context, _ int64, fresh []*normalizer.Release) {
		guids := make([]string, 0, len(fresh))
		for _, r := range fresh {
			guids = append(guids, tzn.GUIDFor(r))
		}
		got = append(got, guids)
	})

	ctx := context.Background()
	cfg := map[string]string{}
	empty := search.Query{}

	sc.storeBestEffort(ctx, instID, cfg, empty, "k", []*normalizer.Release{relWithGUID("A"), relWithGUID("B")})

	// Advance past the rss TTL so the stored entry is EXPIRED, and past the dedup window so
	// the in-memory guard no longer suppresses A/B — only the FetchAny prior diff can.
	future := clk.Load().Add(7 * time.Hour)
	clk.Store(&future)

	sc.storeBestEffort(ctx, instID, cfg, empty, "k", []*normalizer.Release{relWithGUID("A"), relWithGUID("B"), relWithGUID("C")})

	if len(got) != 2 {
		t.Fatalf("announce calls = %d, want 2: %v", len(got), got)
	}
	if !slices.Equal(got[1], []string{"C"}) {
		t.Errorf("post-expiry announce = %v, want [C] (A,B suppressed by the expired prior entry)", got[1])
	}
}

// TestAnnounceTap_NilSinkNoPanic proves the tap is a no-op when no announce targets exist.
func TestAnnounceTap_NilSinkNoPanic(t *testing.T) {
	t.Parallel()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	sc.storeBestEffort(context.Background(), instID, map[string]string{}, search.Query{},
		"k", []*normalizer.Release{relWithGUID("A")})
}
