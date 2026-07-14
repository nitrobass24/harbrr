package torznab

import (
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// urlDriver builds a driver with the synthetic apikey and a fixed base URL for
// request-URL assertions. No doer is needed (buildSearchURL makes no HTTP call).
func urlDriver(t *testing.T) *driver {
	t.Helper()
	d, err := New(native.Params{
		Def:     presetDefinition(presets[0]),
		Cfg:     map[string]string{"apikey": testAPIKey},
		BaseURL: "https://news.example.test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

func parseQuery(t *testing.T, rawurl string) url.Values {
	t.Helper()
	u, err := url.Parse(rawurl)
	if err != nil {
		t.Fatalf("parse built URL: %v", err)
	}
	return u.Query()
}

// TestBuildSearchURLModes is the parity gate for the outbound MoreThanTVAPI request:
// the exact t= function per mode (no fallback-to-search rule, unlike the newznab
// sibling), q sent as-is (no "+"-to-space rewrite), the cat comma-join, imdbid/tvdbid/
// ep presence, the season>0-only rule (no "00" quirk), extended=1 always, and a fixed
// limit=100.
func TestBuildSearchURLModes(t *testing.T) {
	t.Parallel()
	d := urlDriver(t)
	cases := []struct {
		name      string
		query     search.Query
		wantT     string
		wantQuery map[string]string
		absent    []string
	}{
		{
			name:      "empty browse",
			query:     search.Query{},
			wantT:     "search",
			wantQuery: map[string]string{"extended": "1", "limit": "100"},
			absent:    []string{"q", "cat", "imdbid", "tvdbid", "ep", "season"},
		},
		{
			name:      "basic keywords sent as-is (no + rewrite)",
			query:     search.Query{Keywords: "the+matrix reloaded"},
			wantT:     "search",
			wantQuery: map[string]string{"q": "the+matrix reloaded"},
		},
		{
			name:      "categories comma-joined dedup",
			query:     search.Query{Categories: []string{"2040", "2050", "2040"}},
			wantT:     "search",
			wantQuery: map[string]string{"cat": "2040,2050"},
		},
		{
			name:      "tv-search sent unconditionally even with no id params",
			query:     search.Query{Mode: "tv-search", Keywords: "show"},
			wantT:     "tvsearch",
			wantQuery: map[string]string{"q": "show"},
			absent:    []string{"season", "ep", "tvdbid", "imdbid"},
		},
		{
			name:      "tv-search with ids",
			query:     search.Query{Mode: "tv-search", Keywords: "show", TVDBID: "81189", Season: "1", Ep: "2"},
			wantT:     "tvsearch",
			wantQuery: map[string]string{"q": "show", "tvdbid": "81189", "season": "1", "ep": "2"},
		},
		{
			name:      "tv-search season zero is OMITTED (no newznab 00 quirk)",
			query:     search.Query{Mode: "tv-search", TVDBID: "1", Season: "0"},
			wantT:     "tvsearch",
			wantQuery: map[string]string{"tvdbid": "1"},
			absent:    []string{"season"},
		},
		{
			name:   "tv-search negative season omitted",
			query:  search.Query{Mode: "tv-search", Season: "-1"},
			wantT:  "tvsearch",
			absent: []string{"season"},
		},
		{
			name:      "movie-search with imdbid sent as-is (no tt-strip)",
			query:     search.Query{Mode: "movie-search", IMDBID: "tt0133093"},
			wantT:     "movie",
			wantQuery: map[string]string{"imdbid": "tt0133093"},
		},
		{
			name:      "movie-search no id params still emits t=movie",
			query:     search.Query{Mode: "movie-search", Keywords: "inception"},
			wantT:     "movie",
			wantQuery: map[string]string{"q": "inception"},
			absent:    []string{"imdbid"},
		},
		{
			name:   "unknown mode falls back to search",
			query:  search.Query{Mode: "music-search", Artist: "Daft Punk"},
			wantT:  "search",
			absent: []string{"artist"}, // torznab has no music-search param mapping
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := parseQuery(t, d.buildSearchURL(c.query))
			if got.Get("t") != c.wantT {
				t.Errorf("t = %q, want %q", got.Get("t"), c.wantT)
			}
			if got.Get("extended") != "1" {
				t.Errorf("extended = %q, want 1 (always)", got.Get("extended"))
			}
			if got.Get("limit") != "100" {
				t.Errorf("limit = %q, want 100 (fixed)", got.Get("limit"))
			}
			for k, want := range c.wantQuery {
				if got.Get(k) != want {
					t.Errorf("%s = %q, want %q", k, got.Get(k), want)
				}
			}
			for _, k := range c.absent {
				if got.Has(k) {
					t.Errorf("%s = %q, want absent", k, got.Get(k))
				}
			}
		})
	}
}

// TestBuildSearchURLBaseAndAPIPath proves the URL skeleton {base}{apiPath}?... uses the
// preset's fixed apiPath (/api/torznab for MoreThanTV), with the base URL right-trimmed
// of "/".
func TestBuildSearchURLBaseAndAPIPath(t *testing.T) {
	t.Parallel()
	d := urlDriver(t)
	got := d.buildSearchURL(search.Query{})
	prefix, _, _ := strings.Cut(got, "?")
	want := "https://news.example.test/api/torznab"
	if prefix != want {
		t.Errorf("URL prefix = %q, want %q", prefix, want)
	}
}

// TestBuildSearchURLPerPreset proves the sibling presets' wire skeletons: AnimeTosho
// hits the default /api path with NO apikey param at all (keyless public feed — a
// stray configured key is dropped at construction, never sent); Torrent Network hits
// its /api/torznab/api path with its (any-length) key.
func TestBuildSearchURLPerPreset(t *testing.T) {
	t.Parallel()

	at, _ := presetByID("animetosho")
	// A stray key is configured on purpose: keyNone must drop it before it can ride
	// a request to the keyless server.
	atDriver, err := New(native.Params{Def: presetDefinition(at), Cfg: map[string]string{"apikey": "stray-key"}})
	if err != nil {
		t.Fatalf("New(animetosho): %v", err)
	}
	atURL := atDriver.(*driver).buildSearchURL(search.Query{Keywords: "frame arms girl"})
	prefix, _, _ := strings.Cut(atURL, "?")
	if prefix != "https://feed.animetosho.org/api" {
		t.Errorf("animetosho URL prefix = %q, want https://feed.animetosho.org/api (default apiPath)", prefix)
	}
	if parseQuery(t, atURL).Has("apikey") {
		t.Errorf("animetosho request carries apikey=%q, want NO apikey param (keyless public feed)", parseQuery(t, atURL).Get("apikey"))
	}

	tn, _ := presetByID("torrentnetwork")
	tnDriver, err := New(native.Params{Def: presetDefinition(tn), Cfg: map[string]string{"apikey": "tn-key-of-any-length"}})
	if err != nil {
		t.Fatalf("New(torrentnetwork): %v", err)
	}
	tnURL := tnDriver.(*driver).buildSearchURL(search.Query{Keywords: "x"})
	prefix, _, _ = strings.Cut(tnURL, "?")
	if prefix != "https://tntracker.org/api/torznab/api" {
		t.Errorf("torrentnetwork URL prefix = %q, want https://tntracker.org/api/torznab/api", prefix)
	}
	if got := parseQuery(t, tnURL).Get("apikey"); got != "tn-key-of-any-length" {
		t.Errorf("torrentnetwork apikey on the wire = %q, want the configured key", got)
	}
}

// TestBuildSearchURLGeneric proves the generic entry's settings-driven construction:
// the user-supplied base URL, the apiPath setting (default /api, custom values
// normalized — trailing slash stripped, leading slash added), and the OPTIONAL apikey
// (present when configured, wholly absent when not).
func TestBuildSearchURLGeneric(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		apiCfg     string
		apikey     string
		wantPrefix string
		wantKey    bool
	}{
		{"default api path, no key", "", "", "https://tz.example.test/api", false},
		{"default api path, with key", "", "generic-key", "https://tz.example.test/api", true},
		{"custom path trailing slash trimmed", "/torznab/api/", "", "https://tz.example.test/torznab/api", false},
		{"custom path missing leading slash", "torznab/api", "", "https://tz.example.test/torznab/api", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d, err := New(native.Params{
				Def:     GenericDefinition(),
				Cfg:     map[string]string{"apikey": c.apikey, "apiPath": c.apiCfg},
				BaseURL: "https://tz.example.test",
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			raw := d.(*driver).buildSearchURL(search.Query{})
			prefix, _, _ := strings.Cut(raw, "?")
			if prefix != c.wantPrefix {
				t.Errorf("URL prefix = %q, want %q", prefix, c.wantPrefix)
			}
			q := parseQuery(t, raw)
			if c.wantKey && q.Get("apikey") != c.apikey {
				t.Errorf("apikey = %q, want %q", q.Get("apikey"), c.apikey)
			}
			if !c.wantKey && q.Has("apikey") {
				t.Errorf("apikey = %q, want absent (optional key not configured)", q.Get("apikey"))
			}
		})
	}
}

// TestBuildSearchURLCarriesAPIKeyButRedacts proves the built URL carries the apikey (so
// the remote server authenticates) but RedactURL — the chokepoint every log/error
// routes through — replaces it, so no log/error string can leak the apikey.
func TestBuildSearchURLCarriesAPIKeyButRedacts(t *testing.T) {
	t.Parallel()
	d := urlDriver(t)
	raw := d.buildSearchURL(search.Query{Keywords: "test"})
	if got := parseQuery(t, raw).Get("apikey"); got != testAPIKey {
		t.Fatalf("apikey on the wire = %q, want the configured apikey", got)
	}
	assertNoAPIKey(t, "redacted URL", redact(raw))
}

// TestBuildSearchURLAPIKeyLast proves the apikey param is emitted last (the
// redaction-stable param-order idiom shared with the newznab sibling).
func TestBuildSearchURLAPIKeyLast(t *testing.T) {
	t.Parallel()
	d := urlDriver(t)
	raw := d.buildSearchURL(search.Query{Keywords: "x"})
	ai := strings.Index(raw, "apikey=")
	if ai < 0 {
		t.Fatal("apikey missing from built URL")
	}
	if ai != strings.LastIndex(raw, "&")+1 {
		t.Errorf("apikey is not the last param in %q", redact(raw))
	}
}
