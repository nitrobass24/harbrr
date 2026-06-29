package torznabhttp

import (
	"net/url"
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

func scalar(s string) loader.Scalar { return loader.Scalar{Value: s, Set: true} }

// filterTestCaps maps Movies (2000), its Movies/HD child (2040), and TV (5000),
// so a queried Movies parent expands to include the advertised HD child.
func filterTestCaps(t *testing.T) *mapper.Capabilities {
	t.Helper()
	caps, err := mapper.Build(&loader.Definition{
		ID: "filt", Links: []string{"https://filt.test/"},
		Caps: loader.Caps{CategoryMappings: []loader.CategoryMapping{
			{ID: scalar("1"), Cat: "Movies"},
			{ID: scalar("2"), Cat: "Movies/HD"},
			{ID: scalar("3"), Cat: "TV"},
		}},
	})
	if err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}
	return caps
}

func rel(cats ...int) *normalizer.Release { return &normalizer.Release{Categories: cats} }

func catsOf(rs []*normalizer.Release) [][]int {
	out := make([][]int, len(rs))
	for i, r := range rs {
		if r != nil {
			out[i] = r.Categories
		}
	}
	return out
}

func TestFilterResults(t *testing.T) {
	t.Parallel()
	caps := filterTestCaps(t)
	movies := rel(2000)
	moviesHD := rel(2040)
	tv := rel(5000)
	none := rel() // a release the parser extracted no categories for
	var nilRel *normalizer.Release
	all := []*normalizer.Release{movies, moviesHD, tv, none, nilRel}

	tests := []struct {
		name      string
		requested []int
		want      []*normalizer.Release
	}{
		{"nil requested -> all pass", nil, all},
		{"empty requested -> all pass", []int{}, all},
		{
			"movies parent expands to advertised HD child; drops TV; keeps uncategorized + nil",
			[]int{2000},
			[]*normalizer.Release{movies, moviesHD, none, nilRel},
		},
		{
			"exact TV leaf keeps only TV + uncategorized + nil",
			[]int{5000},
			[]*normalizer.Release{tv, none, nilRel},
		},
		{
			"unmapped cat keeps only uncategorized + nil",
			[]int{9999},
			[]*normalizer.Release{none, nilRel},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := filterResults(all, tt.requested, caps)
			if !slices.Equal(got, tt.want) {
				t.Errorf("filterResults(%v) categories = %v, want %v", tt.requested, catsOf(got), catsOf(tt.want))
			}
		})
	}
}

func TestBuildQueryDefaultCategories(t *testing.T) {
	t.Parallel()
	yes := true
	caps, err := mapper.Build(&loader.Definition{
		ID: "def", Links: []string{"https://def.test/"},
		Caps: loader.Caps{
			Modes: loader.Modes{Search: []string{"q"}},
			CategoryMappings: []loader.CategoryMapping{
				{ID: scalar("1"), Cat: "Movies", Default: &yes},
				{ID: scalar("2"), Cat: "TV"},
			},
		},
	})
	if err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}
	if !slices.Equal(caps.DefaultCategories, []string{"1"}) {
		t.Fatalf("DefaultCategories = %v, want [1]", caps.DefaultCategories)
	}

	tests := []struct {
		name        string
		cat         string
		wantTracker []string
		wantReq     []int
	}{
		{"no cat falls back to defaults", "", []string{"1"}, nil},
		{"unmapped cat falls back to defaults", "9999", []string{"1"}, []int{9999}},
		{"mapped Movies cat resolves to tracker 1", "2000", []string{"1"}, []int{2000}},
		{"mapped TV cat resolves to tracker 2 (no default fallback)", "5000", []string{"2"}, []int{5000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := url.Values{}
			if tt.cat != "" {
				q.Set("cat", tt.cat)
			}
			query, requested := buildQuery(q, caps)
			if !slices.Equal(query.Categories, tt.wantTracker) {
				t.Errorf("tracker categories = %v, want %v", query.Categories, tt.wantTracker)
			}
			if !slices.Equal(requested, tt.wantReq) {
				t.Errorf("requested cats = %v, want %v", requested, tt.wantReq)
			}
		})
	}
}

// TestBuildQueryMode proves the Torznab t= param is resolved into search.Query.Mode
// (the caps key), and that an absent or unknown t= defaults to "search" — so the feed
// and the mode-less JSON endpoint produce the same mode for the same query.
func TestBuildQueryMode(t *testing.T) {
	t.Parallel()
	caps, err := mapper.Build(&loader.Definition{
		ID: "def", Links: []string{"https://def.test/"},
		Caps: loader.Caps{Modes: loader.Modes{Search: []string{"q"}, MusicSearch: []string{"q"}, TVSearch: []string{"q"}}},
	})
	if err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}
	cases := map[string]string{
		"":         mapper.ModeSearch,
		"search":   mapper.ModeSearch,
		"music":    mapper.ModeMusicSearch,
		"tvsearch": mapper.ModeTVSearch,
		"bogus":    mapper.ModeSearch,
	}
	for tParam, want := range cases {
		t.Run("t="+tParam, func(t *testing.T) {
			t.Parallel()
			q := url.Values{}
			if tParam != "" {
				q.Set("t", tParam)
			}
			query, _ := buildQuery(q, caps)
			if query.Mode != want {
				t.Errorf("Mode = %q, want %q", query.Mode, want)
			}
		})
	}
}
