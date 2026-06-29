package registry

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func TestBuildSearchCacheKeyStability(t *testing.T) {
	t.Parallel()

	q := search.Query{Keywords: "the matrix", Categories: []string{"5", "1", "21"}}
	want := buildSearchCacheKey(7, q, false)

	// Same inputs => same key.
	if got := buildSearchCacheKey(7, q, false); got != want {
		t.Fatalf("key not stable: got %q want %q", got, want)
	}
	// SHA-256 hex is 64 chars.
	if len(want) != 64 {
		t.Fatalf("key length = %d, want 64", len(want))
	}
}

func TestBuildSearchCacheKeyCategoryCanonicalization(t *testing.T) {
	t.Parallel()

	base := buildSearchCacheKey(1, search.Query{Categories: []string{"1", "5", "21"}}, false)

	tests := []struct {
		name string
		cats []string
		same bool
	}{
		{name: "reordered", cats: []string{"21", "1", "5"}, same: true},
		{name: "duplicates", cats: []string{"5", "1", "1", "21", "5"}, same: true},
		{name: "different set", cats: []string{"1", "5", "22"}, same: false},
		{name: "subset", cats: []string{"1", "5"}, same: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildSearchCacheKey(1, search.Query{Categories: tt.cats}, false)
			if (got == base) != tt.same {
				t.Fatalf("cats %v: same=%v but key equality=%v (got %q base %q)", tt.cats, tt.same, got == base, got, base)
			}
		})
	}
}

func TestBuildSearchCacheKeyNilVsEmptyCategories(t *testing.T) {
	t.Parallel()

	nilKey := buildSearchCacheKey(3, search.Query{Categories: nil}, false)
	emptyKey := buildSearchCacheKey(3, search.Query{Categories: []string{}}, false)
	if nilKey != emptyKey {
		t.Fatalf("nil vs empty categories differ: %q != %q", nilKey, emptyKey)
	}
}

func TestBuildSearchCacheKeyCustomCategoryOrder(t *testing.T) {
	t.Parallel()

	// Numeric ids sort numerically (so "10" after "2"); non-numeric custom ids
	// sort lexically AFTER all numeric ones. Reordering must not change the key.
	a := buildSearchCacheKey(1, search.Query{Categories: []string{"2", "10", "custom-b", "custom-a"}}, false)
	b := buildSearchCacheKey(1, search.Query{Categories: []string{"custom-a", "10", "custom-b", "2"}}, false)
	if a != b {
		t.Fatalf("custom category order changed key: %q != %q", a, b)
	}
}

// TestBuildSearchCacheKeyEqualNumericCategories pins the tie-break for distinct
// strings that parse to the same number ("1" and "01"): without a string tie-break,
// sort.Slice (unstable) could order them either way, so the canonical form — and the
// key — would vary between runs. Both input orderings must hash identically, and the
// key must be stable across repeated builds.
func TestBuildSearchCacheKeyEqualNumericCategories(t *testing.T) {
	t.Parallel()

	forward := buildSearchCacheKey(1, search.Query{Categories: []string{"1", "01"}}, false)
	reverse := buildSearchCacheKey(1, search.Query{Categories: []string{"01", "1"}}, false)
	if forward != reverse {
		t.Fatalf("equal-numeric category order changed key: %q != %q", forward, reverse)
	}
	for i := 0; i < 20; i++ {
		if got := buildSearchCacheKey(1, search.Query{Categories: []string{"1", "01"}}, false); got != forward {
			t.Fatalf("key unstable across runs: %q != %q", got, forward)
		}
	}
}

func TestBuildSearchCacheKeyKeywordsCanonicalization(t *testing.T) {
	t.Parallel()

	want := buildSearchCacheKey(1, search.Query{Keywords: "the matrix"}, false)

	tests := []struct {
		name     string
		keywords string
	}{
		{name: "casefold", keywords: "The Matrix"},
		{name: "upper", keywords: "THE MATRIX"},
		{name: "trim", keywords: "  the matrix  "},
		{name: "trim+case", keywords: "  The MATRIX "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := buildSearchCacheKey(1, search.Query{Keywords: tt.keywords}, false); got != want {
				t.Fatalf("keywords %q: got %q want %q", tt.keywords, got, want)
			}
		})
	}

	// A genuinely different term must NOT collide.
	if got := buildSearchCacheKey(1, search.Query{Keywords: "the matrixx"}, false); got == want {
		t.Fatalf("distinct keywords collided")
	}
}

func TestBuildSearchCacheKeyEmptyVsMissing(t *testing.T) {
	t.Parallel()

	// An explicitly-blank field hashes identically to a missing one (omitempty).
	zero := buildSearchCacheKey(1, search.Query{}, false)
	blanks := buildSearchCacheKey(1, search.Query{
		Keywords: "", IMDBID: "", Season: "", Year: "", Author: "",
	}, false)
	if zero != blanks {
		t.Fatalf("empty vs missing fields differ: %q != %q", zero, blanks)
	}
}

func TestBuildSearchCacheKeyInstanceID(t *testing.T) {
	t.Parallel()

	a := buildSearchCacheKey(1, search.Query{Keywords: "x"}, false)
	b := buildSearchCacheKey(2, search.Query{Keywords: "x"}, false)
	if a == b {
		t.Fatalf("different instance ids produced the same key")
	}
}

func TestBuildSearchCacheKeyEachFieldChangesKey(t *testing.T) {
	t.Parallel()

	base := buildSearchCacheKey(1, search.Query{}, false)

	tests := []struct {
		name   string
		mutate func(*search.Query)
	}{
		{"keywords", func(q *search.Query) { q.Keywords = "v" }},
		{"imdbid", func(q *search.Query) { q.IMDBID = "tt1" }},
		{"tmdbid", func(q *search.Query) { q.TMDBID = "v" }},
		{"tvdbid", func(q *search.Query) { q.TVDBID = "v" }},
		{"tvmazeid", func(q *search.Query) { q.TVMazeID = "v" }},
		{"traktid", func(q *search.Query) { q.TraktID = "v" }},
		{"doubanid", func(q *search.Query) { q.DoubanID = "v" }},
		{"rageid", func(q *search.Query) { q.RageID = "v" }},
		{"season", func(q *search.Query) { q.Season = "1" }},
		{"ep", func(q *search.Query) { q.Ep = "2" }},
		{"year", func(q *search.Query) { q.Year = "2024" }},
		{"artist", func(q *search.Query) { q.Artist = "v" }},
		{"album", func(q *search.Query) { q.Album = "v" }},
		{"label", func(q *search.Query) { q.Label = "v" }},
		{"track", func(q *search.Query) { q.Track = "v" }},
		{"author", func(q *search.Query) { q.Author = "v" }},
		{"booktitle", func(q *search.Query) { q.BookTitle = "v" }},
		{"mode", func(q *search.Query) { q.Mode = "music-search" }},
		{"categories", func(q *search.Query) { q.Categories = []string{"1"} }},
	}
	seen := map[string]string{base: "base"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var q search.Query
			tt.mutate(&q)
			got := buildSearchCacheKey(1, q, false)
			if got == base {
				t.Fatalf("field %s did not change the key", tt.name)
			}
		})
		// Sequential collision check across distinct single-field mutations.
		var q search.Query
		tt.mutate(&q)
		k := buildSearchCacheKey(1, q, false)
		if prev, ok := seen[k]; ok {
			t.Fatalf("field %s collided with %s", tt.name, prev)
		}
		seen[k] = tt.name
	}
}

func TestBuildSearchCacheKeyDistinctIDFieldsDoNotCollide(t *testing.T) {
	t.Parallel()

	// The same value placed in different id fields must produce different keys.
	imdb := buildSearchCacheKey(1, search.Query{IMDBID: "123"}, false)
	tmdb := buildSearchCacheKey(1, search.Query{TMDBID: "123"}, false)
	if imdb == tmdb {
		t.Fatalf("same value in imdbid vs tmdbid collided")
	}
}

func TestBuildSearchCacheKeySchemaVersionBump(t *testing.T) {
	t.Parallel()

	q := search.Query{Keywords: "x", Categories: []string{"1"}}
	live := buildSearchCacheKey(5, q, false)

	// Recompute the key with a bumped schema version; it must differ from the
	// live key, proving a version bump invalidates every entry.
	bumped := keyWithSchemaVersion(searchCacheSchemaVersion+1, 5, q, false)
	if live == bumped {
		t.Fatalf("schema version bump did not change the key")
	}
	// Sanity: recomputing at the live version reproduces the live key.
	if same := keyWithSchemaVersion(searchCacheSchemaVersion, 5, q, false); same != live {
		t.Fatalf("recompute at live version mismatched: %q != %q", same, live)
	}
}

// TestBuildSearchCacheKeyPagingDistinguishesPages proves that, for a paging-capable
// instance, distinct offset/limit windows hash to DISTINCT keys — so a deep page is a
// separate cache entry and never serves another page's body. (The non-paging case is
// covered by TestBuildSearchCacheKeyNonPagingIgnoresOffsetLimit below.)
func TestBuildSearchCacheKeyPagingDistinguishesPages(t *testing.T) {
	t.Parallel()

	page0 := buildSearchCacheKey(1, search.Query{Keywords: "x", Offset: 0, Limit: 100}, true)
	page1 := buildSearchCacheKey(1, search.Query{Keywords: "x", Offset: 100, Limit: 100}, true)
	if page0 == page1 {
		t.Fatalf("paged offset=0 and offset=100 produced the same key")
	}
	// A different limit at the same offset is also a distinct outbound request.
	half := buildSearchCacheKey(1, search.Query{Keywords: "x", Offset: 0, Limit: 50}, true)
	if half == page0 {
		t.Fatalf("paged limit=50 and limit=100 produced the same key")
	}
}

// TestBuildSearchCacheKeyNonPagingIgnoresOffsetLimit pins the no-flush regression
// guard: for a non-paging instance the offset/limit are NOT folded into the key, so a
// query that differs only in its page window hashes identically to the page-free key,
// and — critically — that key is BYTE-IDENTICAL to the pre-paging v2 form. The literal
// below is the value buildSearchCacheKey(1, {Keywords:"x"}) produced before this change;
// if it ever changes, every cached entry would be silently invalidated.
func TestBuildSearchCacheKeyNonPagingIgnoresOffsetLimit(t *testing.T) {
	t.Parallel()

	// The literal is the value buildSearchCacheKey(1, {Keywords:"x"}) produced before
	// this change (schema v2, offset/limit absent). It must not move: a different digest
	// would silently invalidate every cached entry on upgrade.
	const preChange = "d8d1e80883cf03d483f4c0faec9c6a63b20f7675a84d3637d25bd7a0f0c0fe2a"
	pageFree := buildSearchCacheKey(1, search.Query{Keywords: "x"}, false)
	if pageFree != preChange {
		t.Fatalf("non-paging key drifted from the pre-paging v2 form: %q != %q", pageFree, preChange)
	}

	for _, q := range []search.Query{
		{Keywords: "x", Offset: 100, Limit: 100},
		{Keywords: "x", Offset: 0, Limit: 50},
	} {
		if got := buildSearchCacheKey(1, q, false); got != pageFree {
			t.Fatalf("non-paging key folded in offset/limit: %q != %q", got, pageFree)
		}
	}
}
