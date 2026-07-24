package registry

import (
	"context"
	"net/url"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// countingDriver is a fakeDriver that also counts Search calls and carries a REAL
// mapper.Capabilities (not the freeleech fixture's zero-value one), so
// core.buildQuery's MapTorznabCapsToTrackers has a working category map to walk
// instead of nil-dereferencing on the zero value.
type countingDriver struct {
	fakeDriver
	caps  *mapper.Capabilities
	calls atomic.Int64
	// consumesMode overrides the embedded fakeDriver's always-false ConsumesSearchMode,
	// for the #341 accuracy-guard case (a Mode-consuming driver keeps a per-mode key).
	consumesMode bool
}

func (d *countingDriver) Capabilities() *mapper.Capabilities { return d.caps }

func (d *countingDriver) ConsumesSearchMode() bool { return d.consumesMode }

func (d *countingDriver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	d.calls.Add(1)
	return d.fakeDriver.Search(ctx, q)
}

// rssCacheCaps builds a real Capabilities with TV/SD (5030) and Movies/SD (2030)
// category mappings — the same standard ids rssCacheFixture's releases carry — so
// MapTorznabCapsToTrackers/ExpandQueryCategories have real entries to match against.
func rssCacheCaps(t *testing.T) *mapper.Capabilities {
	t.Helper()
	isDefault := true
	def := &loader.Definition{
		ID:    "rsscachetest",
		Links: []string{"https://example.com"},
		Caps: loader.Caps{
			CategoryMappings: []loader.CategoryMapping{
				{ID: loader.Scalar{Value: "10", Set: true}, Cat: "TV/SD", Default: &isDefault},
				{ID: loader.Scalar{Value: "20", Set: true}, Cat: "Movies/SD", Default: &isDefault},
			},
			Modes: loader.Modes{Search: []string{"q"}},
		},
	}
	caps, err := mapper.Build(def)
	if err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}
	return caps
}

// rssCacheFixture is three releases distinguishing the categories a request can ask
// for: a TV-only release, a movie-only release, and a category-less release (which
// Jackett's FilterResults — reproduced by core.filterResults — always keeps
// regardless of the requested cat=, since an uncategorized release can't be excluded
// by a filter it doesn't participate in).
func rssCacheFixture() []*normalizer.Release {
	return []*normalizer.Release{
		{GUID: "tv-1", Title: "tv-item", Categories: []int{5030}},
		{GUID: "movie-1", Title: "movie-item", Categories: []int{2030}},
		{GUID: "none-1", Title: "no-cat-item"},
	}
}

// newRSSCacheAdapter builds a real indexerAdapter wired to a real SearchCache (not the
// cacheProbe test scaffold, which bypasses indexerAdapter.Search entirely) so the
// category-canonicalization fix under test — which lives in indexerAdapter.Search — is
// actually exercised.
func newRSSCacheAdapter(t *testing.T, inner native.Driver) *indexerAdapter {
	t.Helper()
	sc, instID, _ := testCache(t, keywordTTL, 0)
	return &indexerAdapter{
		info:       core.IndexerInfo{ID: "fake"},
		inner:      inner,
		instanceID: instID,
		cache:      sc,
		builtEpoch: sc.instanceEpoch(instID),
		stats:      newIndexerStats(nil, time.Now, zerolog.Nop()),
		budget:     newRequestBudget(nil, time.Now, zerolog.Nop()),
		clock:      time.Now,
		log:        zerolog.Nop(),
	}
}

// TestRSSEmptyQueryCollapsesCacheKeyAndFiltersOnServe is the #249 regression: three
// RSS/empty polls for the SAME instance with three different cat= narrowings (Sonarr
// TV-only, Radarr movies-only, qui/no filter) must drive the tracker exactly ONCE (one
// canonical cache entry), while each poll still receives only the categories it asked
// for — proving the category narrowing moved from the outbound fetch/cache key to a
// client-side filter on serve, without changing what any consumer is served.
func TestRSSEmptyQueryCollapsesCacheKeyAndFiltersOnServe(t *testing.T) {
	t.Parallel()
	inner := &countingDriver{fakeDriver: fakeDriver{releases: rssCacheFixture()}, caps: rssCacheCaps(t)}
	idx := newRSSCacheAdapter(t, inner)
	ctx := context.Background()

	tests := []struct {
		name       string
		cat        string // "" omits the cat= param entirely
		wantTitles []string
	}{
		{name: "sonarr TV-only", cat: "5030", wantTitles: []string{"tv-item", "no-cat-item"}},
		{name: "radarr movies-only", cat: "2030", wantTitles: []string{"movie-item", "no-cat-item"}},
		{name: "qui no category filter", cat: "", wantTitles: []string{"tv-item", "movie-item", "no-cat-item"}},
	}

	for _, tt := range tests {
		q := url.Values{}
		if tt.cat != "" {
			q.Set("cat", tt.cat)
		}
		got, err := core.SearchReleases(ctx, idx, q)
		if err != nil {
			t.Fatalf("%s: SearchReleases: %v", tt.name, err)
		}
		gotTitles := make([]string, 0, len(got.Releases))
		for _, r := range got.Releases {
			gotTitles = append(gotTitles, r.Title)
		}
		if !slices.Equal(gotTitles, tt.wantTitles) {
			t.Errorf("%s: titles = %v, want %v", tt.name, gotTitles, tt.wantTitles)
		}
	}

	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("tracker (inner) called %d times across 3 differently-categorized RSS polls, want 1 (one canonical cache entry)", got)
	}
}

// TestKeywordSearchStillKeysByCategory proves the fix is scoped to RSS/empty polls
// only: a real keyword search with different cat= narrowings still drives the tracker
// once PER category set (unchanged behavior), because a keyword search is not
// isEmptyQuery and so its categories are never canonicalized away.
func TestKeywordSearchStillKeysByCategory(t *testing.T) {
	t.Parallel()
	inner := &countingDriver{fakeDriver: fakeDriver{releases: rssCacheFixture()}, caps: rssCacheCaps(t)}
	idx := newRSSCacheAdapter(t, inner)
	ctx := context.Background()

	q1 := url.Values{"q": {"foo"}, "cat": {"5030"}}
	q2 := url.Values{"q": {"foo"}, "cat": {"2030"}}
	if _, err := core.SearchReleases(ctx, idx, q1); err != nil {
		t.Fatalf("search 1: %v", err)
	}
	if _, err := core.SearchReleases(ctx, idx, q2); err != nil {
		t.Fatalf("search 2: %v", err)
	}
	if got := inner.calls.Load(); got != 2 {
		t.Fatalf("tracker called %d times for 2 differently-categorized keyword searches, want 2 (keyword path unchanged)", got)
	}
}

// TestRSSEmptyQueryCollapsesAcrossMode is the #341 regression: an RSS/empty poll's
// Mode (the Torznab t= a consumer arrives under — tv-search/movie-search/search/none)
// collapses onto ONE cache key for a driver that never reads Mode, exactly like the
// #249 category collapse above. A Mode-CONSUMING driver (newznab/torznab/animebytes'
// shape) keeps a per-mode key instead — the accuracy guard proving the collapse is
// scoped to drivers that actually ignore Mode.
func TestRSSEmptyQueryCollapsesAcrossMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	modes := []string{"tv-search", "movie-search", "search", ""}

	t.Run("non-consuming driver collapses every mode onto one key", func(t *testing.T) {
		t.Parallel()
		inner := &countingDriver{fakeDriver: fakeDriver{releases: rssCacheFixture()}, caps: rssCacheCaps(t)}
		idx := newRSSCacheAdapter(t, inner)

		for _, mode := range modes {
			if _, err := idx.Search(ctx, search.Query{Mode: mode}); err != nil {
				t.Fatalf("mode %q: Search: %v", mode, err)
			}
		}
		if got := inner.calls.Load(); got != 1 {
			t.Fatalf("tracker (inner) called %d times across %d differently-mode RSS polls on a non-consuming driver, want 1 (one canonical cache entry)", got, len(modes))
		}
	})

	t.Run("mode-consuming driver keeps a per-mode key", func(t *testing.T) {
		t.Parallel()
		inner := &countingDriver{fakeDriver: fakeDriver{releases: rssCacheFixture()}, caps: rssCacheCaps(t), consumesMode: true}
		idx := newRSSCacheAdapter(t, inner)

		for _, mode := range modes {
			if _, err := idx.Search(ctx, search.Query{Mode: mode}); err != nil {
				t.Fatalf("mode %q: Search: %v", mode, err)
			}
		}
		if got := inner.calls.Load(); got != int64(len(modes)) {
			t.Fatalf("tracker (inner) called %d times across %d differently-mode RSS polls on a MODE-CONSUMING driver, want %d (per-mode keys preserved)", got, len(modes), len(modes))
		}
	})
}

// TestRSSWarmPrimesTheKeyAConsumerPollReads is the warm round-trip headline
// regression (#341): the warmer's exact call shape — adapter.Search(core.
// WithCacheBypass(ctx), search.Query{}) — must prime the SAME cache key a real
// consumer's RSS poll (arriving under some Torznab Mode, e.g. tv-search) reads, on a
// driver that does not consume Mode. Before this fix the warm's Mode ("") never
// matched a consumer's Mode ("tv-search") in the cache key, so the warm was dead
// weight: every consumer poll still missed and re-hit the tracker.
func TestRSSWarmPrimesTheKeyAConsumerPollReads(t *testing.T) {
	t.Parallel()
	inner := &countingDriver{fakeDriver: fakeDriver{releases: rssCacheFixture()}, caps: rssCacheCaps(t)}
	idx := newRSSCacheAdapter(t, inner)
	ctx := context.Background()

	// The warmer's exact call: cache-bypass forces a live fetch + store.
	if _, err := idx.Search(core.WithCacheBypass(ctx), search.Query{}); err != nil {
		t.Fatalf("warm: Search: %v", err)
	}
	// A real consumer poll, arriving under a Torznab mode, with no cache bypass.
	if _, err := idx.Search(ctx, search.Query{Mode: "tv-search"}); err != nil {
		t.Fatalf("consumer poll: Search: %v", err)
	}

	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("tracker (inner) called %d times across 1 warm + 1 consumer poll, want 1 (the warm must prime the key the poll reads)", got)
	}
}
