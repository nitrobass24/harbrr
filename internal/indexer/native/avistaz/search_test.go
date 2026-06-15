package avistaz

import (
	"context"
	"errors"
	stdhttp "net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func fixedClock() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

// builderDriver is a credential-free driver for the pure query-string builder tests.
func builderDriver(site string, cfg map[string]string) *driver {
	if cfg == nil {
		cfg = map[string]string{}
	}
	return &driver{cfg: cfg, baseURL: "https://az.test/", profile: profileFor(site), clock: fixedClock}
}

// TestBuildSearchURL is the parity gate for the request: it asserts the exact query
// string harbrr emits per search type against Prowlarr's AvistazRequestGenerator
// contract (constant in=1, the single derived type, the id-preferred params, the
// episode term + the AvistaZ override, freeleech, and the FirstIfSingleOrDefault type).
func TestBuildSearchURL(t *testing.T) {
	t.Parallel()
	const endpoint = "https://az.test/api/v1/jackett/torrents"
	cases := []struct {
		name  string
		site  string
		cfg   map[string]string
		query search.Query
		want  url.Values
	}{
		{
			name:  "basic keyword, no category -> type 0",
			site:  "avistaz",
			query: search.Query{Keywords: "the matrix"},
			want:  url.Values{"in": {"1"}, "type": {"0"}, "limit": {"50"}, "search": {"the matrix"}},
		},
		{
			name:  "movie imdb -> full id, no search",
			site:  "avistaz",
			query: search.Query{Categories: []string{"1"}, IMDBID: "133093", Keywords: "the matrix"},
			want:  url.Values{"in": {"1"}, "type": {"1"}, "limit": {"50"}, "imdb": {"tt0133093"}},
		},
		{
			name:  "movie tmdb -> tmdb only",
			site:  "avistaz",
			query: search.Query{Categories: []string{"1"}, TMDBID: "603"},
			want:  url.Values{"in": {"1"}, "type": {"1"}, "limit": {"50"}, "tmdb": {"603"}},
		},
		{
			name:  "movie keyword -> search",
			site:  "avistaz",
			query: search.Query{Categories: []string{"1"}, Keywords: "the matrix"},
			want:  url.Values{"in": {"1"}, "type": {"1"}, "limit": {"50"}, "search": {"the matrix"}},
		},
		{
			name:  "tv imdb + season/ep -> imdb + SxxExx",
			site:  "avistaz",
			query: search.Query{Categories: []string{"2"}, IMDBID: "tt0944947", Season: "1", Ep: "2"},
			want:  url.Values{"in": {"1"}, "type": {"2"}, "limit": {"50"}, "imdb": {"tt0944947"}, "search": {"S01E02"}},
		},
		{
			name:  "tv tvdb + season/ep -> tvdb + SxxExx",
			site:  "avistaz",
			query: search.Query{Categories: []string{"2"}, TVDBID: "121361", Season: "1", Ep: "2"},
			want:  url.Values{"in": {"1"}, "type": {"2"}, "limit": {"50"}, "tvdb": {"121361"}, "search": {"S01E02"}},
		},
		{
			name:  "tv keyword + season/ep -> combined search",
			site:  "avistaz",
			query: search.Query{Categories: []string{"2"}, Keywords: "game of thrones", Season: "1", Ep: "2"},
			want:  url.Values{"in": {"1"}, "type": {"2"}, "limit": {"50"}, "search": {"game of thrones S01E02"}},
		},
		{
			name:  "avistaz seasonless episode override -> E{ep}",
			site:  "avistaz",
			query: search.Query{Categories: []string{"2"}, Keywords: "running man", Ep: "323"},
			want:  url.Values{"in": {"1"}, "type": {"2"}, "limit": {"50"}, "search": {"running man E323"}},
		},
		{
			name:  "privatehd seasonless episode -> no override (empty term)",
			site:  "privatehd",
			query: search.Query{Categories: []string{"2"}, Keywords: "running man", Ep: "323"},
			want:  url.Values{"in": {"1"}, "type": {"2"}, "limit": {"50"}, "search": {"running man"}},
		},
		{
			name:  "tv imdb, no season/ep -> empty search param present",
			site:  "avistaz",
			query: search.Query{Categories: []string{"2"}, IMDBID: "tt0944947"},
			want:  url.Values{"in": {"1"}, "type": {"2"}, "limit": {"50"}, "imdb": {"tt0944947"}, "search": {""}},
		},
		{
			name:  "freeleech_only -> discount[]=1",
			site:  "avistaz",
			cfg:   map[string]string{"freeleech_only": "True"},
			query: search.Query{Categories: []string{"1"}, Keywords: "dune"},
			want:  url.Values{"in": {"1"}, "type": {"1"}, "limit": {"50"}, "search": {"dune"}, "discount[]": {"1"}},
		},
		{
			name:  "two categories -> type 0 (FirstIfSingleOrDefault)",
			site:  "avistaz",
			query: search.Query{Categories: []string{"1", "2"}, Keywords: "foo"},
			want:  url.Values{"in": {"1"}, "type": {"0"}, "limit": {"50"}, "search": {"foo"}},
		},
		{
			name:  "exoticaz forces basic (xxx cat as type, search only)",
			site:  "exoticaz",
			query: search.Query{Categories: []string{"6"}, Keywords: "foo", IMDBID: "tt1", Season: "1", Ep: "2"},
			want:  url.Values{"in": {"1"}, "type": {"6"}, "limit": {"50"}, "search": {"foo"}},
		},
		{
			name:  "sanitize drops disallowed punctuation",
			site:  "avistaz",
			query: search.Query{Categories: []string{"1"}, Keywords: "Money$ Heist: 4!"},
			want:  url.Values{"in": {"1"}, "type": {"1"}, "limit": {"50"}, "search": {"Money Heist 4"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := builderDriver(tc.site, tc.cfg).buildSearchURL(tc.query)
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parse %q: %v", raw, err)
			}
			if got := u.Scheme + "://" + u.Host + u.Path; got != endpoint {
				t.Errorf("endpoint = %q, want %q", got, endpoint)
			}
			if got := u.Query(); !reflect.DeepEqual(map[string][]string(got), map[string][]string(tc.want)) {
				t.Errorf("query =\n  %v\nwant\n  %v", got, tc.want)
			}
		})
	}
}

// TestSearchStatusDispatch proves Search maps the response status the way Prowlarr
// does: 404 is no results (not an error), 429 is a rate-limit error.
func TestSearchStatusDispatch(t *testing.T) {
	t.Parallel()

	mk := func(status int, body string) *driver {
		doer := &scriptDoer{handler: func(req *stdhttp.Request, _ string) *stdhttp.Response {
			if strings.HasSuffix(req.URL.Path, "/"+authPath) {
				return resp(stdhttp.StatusOK, `{"token":"t"}`)
			}
			return resp(status, body)
		}}
		return &driver{
			cfg:     map[string]string{"username": "u", "password": "p", "pid": "x"},
			doer:    doer,
			baseURL: "https://az.test/",
			profile: profileFor("avistaz"),
			clock:   fixedClock,
		}
	}

	rel, err := mk(stdhttp.StatusNotFound, `{}`).Search(context.Background(), search.Query{Keywords: "x"})
	if err != nil || rel != nil {
		t.Errorf("404: rel=%v err=%v, want nil, nil", rel, err)
	}

	_, err = mk(stdhttp.StatusTooManyRequests, `{}`).Search(context.Background(), search.Query{Keywords: "x"})
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Errorf("429: err=%v, want *search.RateLimitedError", err)
	}

	// A 412 surviving get's reactive re-auth is an auth failure.
	_, err = mk(stdhttp.StatusPreconditionFailed, `{}`).Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("412: err=%v, want login.ErrLoginFailed", err)
	}
}

// TestSearchIssuesBearerRequest proves Search drives the built URL with the Bearer
// header attached and that the served (recorded) URL leaks no credential.
func TestSearchIssuesBearerRequest(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(req *stdhttp.Request, _ string) *stdhttp.Response {
		if strings.HasSuffix(req.URL.Path, "/"+authPath) {
			return resp(stdhttp.StatusOK, `{"token":"tok-9"}`)
		}
		return resp(stdhttp.StatusOK, `{"data":[]}`)
	}}
	d := &driver{
		cfg:     map[string]string{"username": credUser, "password": credPass, "pid": credPID},
		doer:    doer,
		baseURL: "https://az.test/",
		profile: profileFor("avistaz"),
		clock:   fixedClock,
	}
	if _, err := d.Search(context.Background(), search.Query{Categories: []string{"1"}, Keywords: "dune"}); err != nil {
		t.Fatalf("Search: %v", err)
	}

	var got *recordedReq
	for i := range doer.reqs {
		if strings.Contains(doer.reqs[i].url, "torrents") {
			got = &doer.reqs[i]
		}
	}
	if got == nil {
		t.Fatal("no torrents request recorded")
	}
	if got.method != stdhttp.MethodGet || got.auth != "Bearer tok-9" {
		t.Errorf("torrents request = %s auth=%q, want GET Bearer tok-9", got.method, got.auth)
	}
	if got.accept != "application/json" {
		t.Errorf("search Accept = %q, want application/json", got.accept)
	}
	u, _ := url.Parse(got.url)
	if u.Query().Get("type") != "1" || u.Query().Get("search") != "dune" {
		t.Errorf("recorded query = %v", u.Query())
	}
	assertNoSecret(t, got.url)
}

func TestSanitizeSearchTerm(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"the matrix", "the matrix"},
		{"Money$ Heist: 4!", "Money Heist 4"},
		{"Amélie", "Amélie"},                   // accented letters kept
		{"a — b", "a - b"},                     // em dash -> '-'
		{"a–-—b", "a-b"},                       // a run of dashes collapses to one '-'
		{"it’s", "it's"},                       // curly apostrophe normalized
		{"WALL[E]+ (2008)", "WALL[E]+ (2008)"}, // whitelisted punctuation survives
		{"a@b/c_d.e%f", "a@b/c_d.e%f"},
	}
	for _, tc := range cases {
		if got := sanitizeSearchTerm(tc.in); got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEpisodeSearchString(t *testing.T) {
	t.Parallel()
	cases := []struct{ season, ep, want string }{
		{"", "", ""},
		{"0", "5", ""},                  // seasonless (base) -> empty
		{"1", "", "S01"},                // season only
		{"1", "2", "S01E02"},            // standard
		{"12", "5", "S12E05"},           // two-digit season, padded episode
		{"2021", "05/13", "2021.05.13"}, // daily
	}
	for _, tc := range cases {
		if got := episodeSearchString(tc.season, tc.ep); got != tc.want {
			t.Errorf("episodeSearchString(%q,%q) = %q, want %q", tc.season, tc.ep, got, tc.want)
		}
	}
}

func TestEpisodeSearchTermOverride(t *testing.T) {
	t.Parallel()
	q := search.Query{Ep: "323"} // seasonless episode
	if got := builderDriver("avistaz", nil).episodeSearchTerm(q); got != "E323" {
		t.Errorf("avistaz seasonless = %q, want E323", got)
	}
	if got := builderDriver("privatehd", nil).episodeSearchTerm(q); got != "" {
		t.Errorf("privatehd seasonless = %q, want empty (no override)", got)
	}
}

func TestFullIMDBID(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"133093", "tt0133093"},
		{"tt0084967", "tt0084967"},
		{"TT123", "tt0000123"},
		{"12345678", "tt12345678"}, // 8 digits: 7 is the minimum width
		{"not-an-id", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := fullIMDBID(tc.in); got != tc.want {
			t.Errorf("fullIMDBID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDerivedType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   []string
		want string
	}{
		{nil, "0"},
		{[]string{"1"}, "1"},
		{[]string{"2", "2"}, "2"},
		{[]string{"1", "2"}, "0"},
	}
	for _, tc := range cases {
		if got := derivedType(tc.in); got != tc.want {
			t.Errorf("derivedType(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFreeleechOnly(t *testing.T) {
	t.Parallel()
	on := []string{"True", "true", "1", "on", "yes"}
	for _, v := range on {
		if !freeleechOnly(map[string]string{"freeleech_only": v}) {
			t.Errorf("freeleechOnly(%q) = false, want true", v)
		}
	}
	off := []string{"", "false", "0", "no"}
	for _, v := range off {
		if freeleechOnly(map[string]string{"freeleech_only": v}) {
			t.Errorf("freeleechOnly(%q) = true, want false", v)
		}
	}
}
