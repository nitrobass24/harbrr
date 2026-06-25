package newznab

import (
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// testAPIKey is a synthetic apikey that exists only to prove redaction — it never reaches a
// real server. It must never appear in a log/error string the driver surfaces.
const testAPIKey = "SECRETapikey1234567890"

// urlDriver builds a driver with the synthetic apikey and a fixed base URL for request-URL
// assertions. No doer is needed (buildSearchURL makes no HTTP call).
func urlDriver(t *testing.T) *driver {
	t.Helper()
	d, err := New(native.Params{
		Def:     GenericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey, "apiPath": "/api"},
		BaseURL: "https://news.example.test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

// parseQuery returns the query params of a built search URL for per-param assertions.
func parseQuery(t *testing.T, rawurl string) url.Values {
	t.Helper()
	u, err := url.Parse(rawurl)
	if err != nil {
		t.Fatalf("parse built URL: %v", err)
	}
	return u.Query()
}

// TestBuildSearchURLModes is the parity gate for the outbound Newznab request: it asserts
// the exact t= function per mode (including the fallback-to-search rule), the per-mode id
// params, the q +->space rule, the cat comma-join, the season 0->"00" quirk, the imdbid
// digits-only strip, extended=1 always, and limit=100.
func TestBuildSearchURLModes(t *testing.T) {
	t.Parallel()
	d := urlDriver(t)
	cases := []struct {
		name      string
		query     search.Query
		wantT     string
		wantQuery map[string]string // exact expected values (checked present + equal)
		absent    []string          // params that must NOT be present
	}{
		{
			name:      "empty browse",
			query:     search.Query{},
			wantT:     "search",
			wantQuery: map[string]string{"extended": "1", "limit": "100"},
			absent:    []string{"q", "cat", "imdbid"},
		},
		{
			name:      "basic keywords plus replace",
			query:     search.Query{Keywords: "the+matrix reloaded"},
			wantT:     "search",
			wantQuery: map[string]string{"q": "the matrix reloaded"},
		},
		{
			name:      "categories comma-joined dedup",
			query:     search.Query{Categories: []string{"2000", "2010", "2000"}},
			wantT:     "search",
			wantQuery: map[string]string{"cat": "2000,2010"},
		},
		{
			name:      "tv-search with ids",
			query:     search.Query{Mode: "tv-search", Keywords: "show", TVDBID: "81189", Season: "1", Ep: "2"},
			wantT:     "tvsearch",
			wantQuery: map[string]string{"q": "show", "tvdbid": "81189", "season": "1", "ep": "2"},
		},
		{
			name:      "tv-search traktid is movie-only and dropped",
			query:     search.Query{Mode: "tv-search", TVDBID: "81189", TraktID: "1390"},
			wantT:     "tvsearch",
			wantQuery: map[string]string{"tvdbid": "81189"},
			absent:    []string{"traktid"},
		},
		{
			name:      "tv-search season zero becomes 00",
			query:     search.Query{Mode: "tv-search", TVDBID: "1", Season: "0"},
			wantT:     "tvsearch",
			wantQuery: map[string]string{"season": "00"},
		},
		{
			name:      "tv-search no mode params falls back to search",
			query:     search.Query{Mode: "tv-search", Keywords: "only keywords"},
			wantT:     "search",
			wantQuery: map[string]string{"q": "only keywords"},
			absent:    []string{"season", "ep", "tvdbid"},
		},
		{
			name:      "movie-search imdbid strips tt",
			query:     search.Query{Mode: "movie-search", IMDBID: "tt0133093", TMDBID: "603"},
			wantT:     "movie",
			wantQuery: map[string]string{"imdbid": "0133093", "tmdbid": "603"},
		},
		{
			name:      "movie-search no mode params falls back to search",
			query:     search.Query{Mode: "movie-search", Keywords: "inception"},
			wantT:     "search",
			wantQuery: map[string]string{"q": "inception"},
			absent:    []string{"imdbid", "tmdbid"},
		},
		{
			name:      "music-search",
			query:     search.Query{Mode: "music-search", Artist: "Daft Punk", Album: "Discovery"},
			wantT:     "music",
			wantQuery: map[string]string{"artist": "Daft Punk", "album": "Discovery"},
		},
		{
			name:      "book-search uses title param not q",
			query:     search.Query{Mode: "book-search", Author: "Asimov", BookTitle: "Foundation"},
			wantT:     "book",
			wantQuery: map[string]string{"author": "Asimov", "title": "Foundation"},
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
				t.Errorf("limit = %q, want 100 (default)", got.Get("limit"))
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

// TestBuildSearchURLBaseAndPath proves the URL skeleton: {base}{apiPath}?... with both the
// base URL and apiPath right-trimmed of "/" and the apiPath default applied.
func TestBuildSearchURLBaseAndPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		base   string
		apiCfg string
		want   string // the prefix before "?"
	}{
		{"default api path", "https://news.example.test", "", "https://news.example.test/api"},
		{"trailing slashes trimmed", "https://news.example.test/", "/api/", "https://news.example.test/api"},
		{"custom path missing leading slash", "https://news.example.test", "newznab/api", "https://news.example.test/newznab/api"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d, err := New(native.Params{
				Def:     GenericDefinition(),
				Cfg:     map[string]string{"apikey": testAPIKey, "apiPath": c.apiCfg},
				BaseURL: c.base,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got := d.(*driver).buildSearchURL(search.Query{})
			prefix, _, _ := strings.Cut(got, "?")
			if prefix != c.want {
				t.Errorf("URL prefix = %q, want %q", prefix, c.want)
			}
		})
	}
}

// TestBuildSearchURLCarriesApikeyButRedacts proves the built URL carries the apikey (so the
// remote server authenticates) but RedactURL — the chokepoint every log/error routes through
// — replaces it, so no log/error string can leak the apikey.
func TestBuildSearchURLCarriesApikeyButRedacts(t *testing.T) {
	t.Parallel()
	d := urlDriver(t)
	raw := d.buildSearchURL(search.Query{Keywords: "test"})
	if got := parseQuery(t, raw).Get("apikey"); got != testAPIKey {
		t.Fatalf("apikey on the wire = %q, want the configured apikey", got)
	}
	assertNoApikey(t, "redacted URL", redact(raw))
}
