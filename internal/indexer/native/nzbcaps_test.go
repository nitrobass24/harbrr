package native

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// capsDoc is a representative ?t=caps document: <limits>, the <searching> modes
// (including <audio-search> for music and an unavailable book-search), and a category
// tree carrying every resolution case the builder has to handle — a parent by name, a
// subcat by combined name, a subcat that only resolves to Parent/Other, a parent-only
// category, an unknown parent, and a site-specific custom id (100001) of the kind
// Jackett/Prowlarr-style servers emit.
const capsDoc = `<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <server version="1.1" title="Example" url="https://caps.example.test/"/>
  <limits max="100" default="75"/>
  <searching>
    <search available="yes" supportedParams="q"/>
    <tv-search available="yes" supportedParams="q,season,ep,imdbid,tvdbid,rid"/>
    <movie-search available="yes" supportedParams="q,imdbid,tmdbid"/>
    <audio-search available="yes" supportedParams="q,artist,album"/>
    <book-search available="no" supportedParams="q,author,title"/>
  </searching>
  <categories>
    <category id="2000" name="Movies">
      <subcat id="2040" name="HD"/>
      <subcat id="2999" name="Bollywood"/>
    </category>
    <category id="5000" name="TV">
      <subcat id="5040" name="HD"/>
      <subcat id="5070" name="Anime"/>
      <subcat id="100001" name="TV Packs"/>
    </category>
    <category id="3000" name="Audio"/>
    <category id="7777" name="Wibble"/>
  </categories>
</caps>`

// buildCapsDoc parses + builds a caps document for the named family.
func buildCapsDoc(t *testing.T, family, doc string) *mapper.Capabilities {
	t.Helper()
	root, err := parseNzbCaps([]byte(doc), family, "", 0)
	if err != nil {
		t.Fatalf("parseNzbCaps: %v", err)
	}
	caps, err := buildFromNzbCaps(root, family)
	if err != nil {
		t.Fatalf("buildFromNzbCaps: %v", err)
	}
	return caps
}

// TestNzbCapsModesAndIMDB proves the parsed caps map onto the right search modes:
// <audio-search> is stored under music-search, an unavailable mode (book-search
// available="no") is dropped, and AllowTVSearchIMDB is derived from the tv-search
// supportedParams carrying imdbid.
func TestNzbCapsModesAndIMDB(t *testing.T) {
	t.Parallel()
	caps := buildCapsDoc(t, "newznab", capsDoc)

	for _, mode := range []string{"search", "tv-search", "movie-search", "music-search"} {
		if caps.Modes[mode] == nil {
			t.Errorf("missing advertised mode %q", mode)
		}
	}
	if caps.Modes["book-search"] != nil {
		t.Error("book-search available=no must be dropped")
	}
	// <audio-search> -> music-search, params preserved verbatim.
	if got := caps.Modes["music-search"]; !slices.Equal(got, []string{"q", "artist", "album"}) {
		t.Errorf("music-search params = %v, want [q artist album] (from <audio-search>)", got)
	}
	if !caps.AllowTVSearchIMDB {
		t.Error("AllowTVSearchIMDB = false, want true (tv-search supportedParams has imdbid)")
	}
}

// TestNzbCapsLimits proves <limits max="100" default="75"/> parses into
// mapper.Capabilities.Limits, and that an absent <limits> element defaults to 100/100
// (Prowlarr's IndexerCapabilities convention, #250).
func TestNzbCapsLimits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		doc              string
		wantDef, wantMax int
	}{
		{name: "<limits max=100 default=75>", doc: capsDoc, wantDef: 75, wantMax: 100},
		{
			name:    "no <limits> element defaults 100/100",
			doc:     `<?xml version="1.0"?><caps><searching/><categories/></caps>`,
			wantDef: 100, wantMax: 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildCapsDoc(t, "newznab", tt.doc)
			if got.Limits.Default != tt.wantDef || got.Limits.Max != tt.wantMax {
				t.Errorf("Limits = %+v, want {Default:%d Max:%d}", got.Limits, tt.wantDef, tt.wantMax)
			}
		})
	}
}

// TestNzbCapsCategoryResolution is the parity gate for the category map: a parent by
// name, a subcat by combined name, a subcat that falls back to Parent/Other, a
// parent-only category, an unknown parent that falls back to Other, and a custom id —
// each keyed by its remote id, in BOTH directions (the response side resolves a remote
// id to newznab ids, the request side a requested newznab id back to remote ids).
func TestNzbCapsCategoryResolution(t *testing.T) {
	t.Parallel()
	caps := buildCapsDoc(t, "newznab", capsDoc)
	m := caps.CategoryMap

	cases := []struct {
		name      string
		remoteID  string
		wantNZBID int
	}{
		{"parent by name", "2000", 2000},               // Movies
		{"subcat combined name", "2040", 2040},         // Movies/HD
		{"subcat parent/other fallback", "2999", 2020}, // Bollywood -> Movies/Other
		{"tv child by name", "5040", 5040},             // TV/HD — the id #643 lost
		{"tv subcat by name", "5070", 5070},            // TV/Anime
		{"parent-only audio", "3000", 3000},            // Audio
		{"unknown parent -> Other", "7777", 8000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := m.MapTrackerCatToNewznab(c.remoteID)
			if !slices.Contains(got, c.wantNZBID) {
				t.Errorf("remote id %q -> %v, want it to include %d", c.remoteID, got, c.wantNZBID)
			}
			if back := caps.MapTorznabCapsToTrackers([]int{c.wantNZBID}); !slices.Contains(back, c.remoteID) {
				t.Errorf("newznab id %d -> %v, want it to include remote id %q", c.wantNZBID, back, c.remoteID)
			}
		})
	}

	// A site-specific custom id resolves through the fetched tree to its synthesised
	// custom category (>= mapper.CustomCategoryOffset), which the placeholder table
	// cannot express at all.
	custom := m.MapTrackerCatToNewznab("100001")
	if len(custom) == 0 {
		t.Fatal("custom remote id 100001 -> no newznab ids, want the synthesised custom category")
	}
	if !slices.ContainsFunc(custom, func(id int) bool { return id >= mapper.CustomCategoryOffset }) {
		t.Errorf("custom remote id 100001 -> %v, want a synthesised custom id >= %d", custom, mapper.CustomCategoryOffset)
	}
}

// TestNzbCapsDescRoundTrip proves the remote category name survives as the mapping desc
// (so a custom 1:1 category is synthesised and desc-based lookups work).
func TestNzbCapsDescRoundTrip(t *testing.T) {
	t.Parallel()
	m := buildCapsDoc(t, "newznab", capsDoc).CategoryMap
	// "Bollywood" collapsed onto Movies/Other but keeps its own desc + custom id.
	if got := m.MapTrackerCatDescToNewznab("Bollywood"); len(got) == 0 {
		t.Error("desc Bollywood -> no newznab ids, want the synthesised custom category")
	}
}

// TestNzbCapsParseErrors proves the family prefix rides every caps error, an <error>
// envelope is classified exactly like a search error (auth -> login.ErrLoginFailed),
// and a non-<caps> or malformed body is an ErrParseError.
func TestNzbCapsParseErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		body    string
		wantErr error
	}{
		{
			name:    "auth error envelope",
			body:    `<?xml version="1.0"?><error code="100" description="Incorrect user credentials"/>`,
			wantErr: login.ErrLoginFailed,
		},
		{
			name:    "wrong root element",
			body:    `<?xml version="1.0"?><html><body>login</body></html>`,
			wantErr: search.ErrParseError,
		},
		{
			name:    "malformed xml",
			body:    `<caps><searching>`,
			wantErr: search.ErrParseError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseNzbCaps([]byte(tt.body), "torznab", "", 0)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("parseNzbCaps err = %v, want errors.Is(%v)", err, tt.wantErr)
			}
			if !strings.HasPrefix(err.Error(), "torznab: ") {
				t.Errorf("err = %q, want the caller's family prefix", err.Error())
			}
		})
	}
}

// TestNzbAPIURL proves the shared API URL builder joins base + path correctly (the base
// carries a trailing slash after NewBase normalisation) and omits the apikey entirely
// when the site is keyless.
func TestNzbAPIURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		base   string
		path   string
		apikey string
		want   string
	}{
		{
			name: "keyed", base: "https://x.test/", path: "/api", apikey: "k",
			want: "https://x.test/api?apikey=k&t=caps",
		},
		{
			name: "keyless", base: "https://x.test", path: "/api/torznab", apikey: "",
			want: "https://x.test/api/torznab?t=caps",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NzbAPIURL(tt.base, tt.path, "caps", tt.apikey); got != tt.want {
				t.Errorf("NzbAPIURL = %q, want %q", got, tt.want)
			}
		})
	}
}
