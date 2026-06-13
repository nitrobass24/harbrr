package torznab

import (
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
)

// scalar is a terse loader.Scalar constructor for in-memory definitions.
func scalar(v string) loader.Scalar { return loader.Scalar{Value: v, Set: true} }

func mustBuild(t *testing.T, def *loader.Definition) *mapper.Capabilities {
	t.Helper()
	caps, err := mapper.Build(def)
	if err != nil {
		t.Fatalf("mapper.Build(%q): %v", def.ID, err)
	}
	return caps
}

// jackettCategoriesDef ports the 2nd definition of Jackett's
// CardigannIndexerTests.TestCardigannTorznabCategories (commit b4140c7): a
// category tree exercising parent/child/custom cats and a duplicate standard cat
// mapped twice. The expected sorted tree and the custom ids (100044, 137107,
// 100045) are Jackett's own asserted values — this is the jackett-port oracle.
func jackettCategoriesDef() *loader.Definition {
	return &loader.Definition{
		ID:    "jackett-categories-oracle",
		Links: []string{"https://example.com"},
		Caps: loader.Caps{
			Categories: map[string]string{
				"1":      "Movies",       // integer cat (has children)
				"mov_sd": "Movies/SD",    // string cat (child cat)
				"33":     "Books/Comics", // integer cat (child cat)
			},
			CategoryMappings: []loader.CategoryMapping{
				{ID: scalar("44"), Cat: "Console/XBox", Desc: "Console/Xbox_c"},    // -> custom 100044
				{ID: scalar("con_wii"), Cat: "Console/Wii", Desc: "Console/Wii_c"}, // -> custom 137107
				{ID: scalar("45"), Cat: "Console/XBox", Desc: "Console/Xbox_c2"},   // -> custom 100045
			},
			Modes: loader.Modes{Search: []string{"q"}},
		},
	}
}

// jackettModesDef ports the 3rd definition (search modes) of the same Jackett
// test. The expected supported-param strings (notably tv-search dropping imdbid)
// are Jackett's asserted enum-derived values.
func jackettModesDef() *loader.Definition {
	return &loader.Definition{
		ID:    "jackett-modes-oracle",
		Links: []string{"https://example.com"},
		Caps: loader.Caps{
			Categories: map[string]string{},
			Modes: loader.Modes{
				Search:      []string{"q"},
				TVSearch:    []string{"q", "season", "ep", "imdbid", "tvdbid", "rid"},
				MovieSearch: []string{"q", "imdbid", "tmdbid"},
				MusicSearch: []string{"q", "album", "artist", "label", "year"},
				BookSearch:  []string{"q", "title", "author"},
			},
		},
	}
}

func TestMarshalCapsGolden(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		golden string
		def    *loader.Definition
	}{
		{
			name:   "jackett-categories",
			golden: "caps/jackett-categories.xml",
			def:    jackettCategoriesDef(),
		},
		{
			name:   "jackett-modes",
			golden: "caps/jackett-modes.xml",
			def:    jackettModesDef(),
		},
		{
			name:   "allowrawsearch-and-tvimdb",
			golden: "caps/allowrawsearch.xml",
			def: &loader.Definition{
				ID:    "rawsearch",
				Links: []string{"https://example.com"},
				Caps: loader.Caps{
					Categories:        map[string]string{"7": "Movies/HD"},
					AllowRawSearch:    boolPtr(true),
					AllowTVSearchIMDB: boolPtr(true),
					Modes: loader.Modes{
						Search:      []string{"q"},
						TVSearch:    []string{"q", "season", "imdbid"},
						MovieSearch: []string{"q", "imdbid"},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := MarshalCaps(mustBuild(t, tt.def))
			if err != nil {
				t.Fatalf("MarshalCaps: %v", err)
			}
			assertGolden(t, tt.golden, got)
		})
	}
}

// TestCapsCategoryTreeOracle is the jackett-port category oracle: it pins the
// custom-id hash facts and the reconstructed/sorted tree against Jackett's
// TestCardigannTorznabCategories expectations, independent of XML whitespace.
func TestCapsCategoryTreeOracle(t *testing.T) {
	t.Parallel()
	caps := mustBuild(t, jackettCategoriesDef())

	// Custom ids are Jackett's asserted values (137107 is the BitConverter.
	// ToUInt16(SHA1("con_wii")) + 100000 hash — a byte-for-byte parity anchor).
	wantCustom := map[string]int{
		"Console/Xbox_c":  100044,
		"Console/Wii_c":   137107,
		"Console/Xbox_c2": 100045,
	}
	tree := buildCategoryTree(caps.Categories)

	type node struct {
		id     int
		name   string
		subcat []capsSubcat
	}
	got := make([]node, len(tree))
	for i, c := range tree {
		got[i] = node{c.ID, c.Name, c.Subcats}
	}

	// Expected sorted top-level order: standard parents ascending by id (Console
	// 1000, Movies 2000, Books 7000), then custom cats by name (Wii_c, Xbox_c,
	// Xbox_c2). Subcats ascending by id.
	want := []node{
		{1000, "Console", []capsSubcat{{1030, "Console/Wii"}, {1040, "Console/XBox"}}},
		{2000, "Movies", []capsSubcat{{2030, "Movies/SD"}}},
		{7000, "Books", []capsSubcat{{7030, "Books/Comics"}}},
		{wantCustom["Console/Wii_c"], "Console/Wii_c", nil},
		{wantCustom["Console/Xbox_c"], "Console/Xbox_c", nil},
		{wantCustom["Console/Xbox_c2"], "Console/Xbox_c2", nil},
	}
	if len(got) != len(want) {
		t.Fatalf("tree length = %d, want %d (%+v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i].id != want[i].id || got[i].name != want[i].name {
			t.Errorf("node %d = {%d %q}, want {%d %q}", i, got[i].id, got[i].name, want[i].id, want[i].name)
		}
		if !equalSubcats(got[i].subcat, want[i].subcat) {
			t.Errorf("node %d %q subcats = %+v, want %+v", i, got[i].name, got[i].subcat, want[i].subcat)
		}
	}
}

// TestCapsSupportedParamsOracle pins the re-derived supportedParams strings
// (Jackett's enum-order output, including the dropped tv-search imdbid).
func TestCapsSupportedParamsOracle(t *testing.T) {
	t.Parallel()
	caps := mustBuild(t, jackettModesDef())
	want := map[string]string{
		"search":       "q",
		"tv-search":    "q,season,ep,tvdbid,rid", // imdbid dropped: AllowTVSearchIMDB false
		"movie-search": "q,imdbid,tmdbid",
		"music-search": "q,album,artist,label,year",
		"audio-search": "q,album,artist,label,year",
		"book-search":  "q,title,author",
	}
	for _, m := range searchModes {
		got := m.supportedParams(caps)
		if got != want[m.xmlElem] {
			t.Errorf("%s supportedParams = %q, want %q", m.xmlElem, got, want[m.xmlElem])
		}
		if !m.available(caps) {
			t.Errorf("%s should be available for the modes oracle def", m.xmlElem)
		}
	}
}

// TestCapsTVImdbGate confirms the AllowTVSearchIMDB flag governs tv-search
// imdbid, independent of the param list (Jackett's documented quirk).
func TestCapsTVImdbGate(t *testing.T) {
	t.Parallel()
	tvMode, _ := modeForRequest(ReqTVSearch)

	// imdbid declared but flag off -> dropped.
	off := mustBuild(t, &loader.Definition{
		ID: "tvimdb-off", Links: []string{"https://e.com"},
		Caps: loader.Caps{Modes: loader.Modes{Search: []string{"q"}, TVSearch: []string{"q", "imdbid"}}},
	})
	if strings.Contains(tvMode.supportedParams(off), "imdbid") {
		t.Error("tv-search advertised imdbid with AllowTVSearchIMDB off")
	}

	// imdbid NOT declared but flag on -> advertised (flag governs, not the list).
	on := mustBuild(t, &loader.Definition{
		ID: "tvimdb-on", Links: []string{"https://e.com"},
		Caps: loader.Caps{AllowTVSearchIMDB: boolPtr(true), Modes: loader.Modes{Search: []string{"q"}, TVSearch: []string{"q", "season"}}},
	})
	if !strings.Contains(tvMode.supportedParams(on), "imdbid") {
		t.Error("tv-search omitted imdbid with AllowTVSearchIMDB on")
	}
}

func boolPtr(b bool) *bool { return &b }

func equalSubcats(a, b []capsSubcat) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
