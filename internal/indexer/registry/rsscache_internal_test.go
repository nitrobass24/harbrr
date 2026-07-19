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
}

func (d *countingDriver) Capabilities() *mapper.Capabilities { return d.caps }

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
