package torrentday

import (
	"context"
	"errors"
	stdhttp "net/http"
	"os"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestBuildSearchURL is the parity gate for the request: it asserts the exact /t.json
// query TorrentDay emits against Prowlarr's TorrentDayRequestGenerator contract — the
// ';'-joined category ids placed path-style after '?', the optional ';free' token, and
// the always-present trailing ';q=<term>' (URL-encoded, empty for a raw browse). The
// secret cookie never appears in any built URL (it rides only the Cookie header).
func TestBuildSearchURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		cfg   map[string]string
		query search.Query
		want  string
	}{
		{
			name:  "empty browse -> bare q",
			query: search.Query{},
			want:  base + "t.json?q=",
		},
		{
			name:  "basic keyword",
			query: search.Query{Keywords: "the matrix"},
			want:  base + "t.json?q=the+matrix",
		},
		{
			name:  "single category before q",
			query: search.Query{Categories: []string{"29"}, Keywords: "dune"},
			want:  base + "t.json?29;q=dune",
		},
		{
			name:  "multiple categories deduplicated, order preserved",
			query: search.Query{Categories: []string{"29", "28", "29"}, Keywords: "foo"},
			want:  base + "t.json?29;28;q=foo",
		},
		{
			name:  "imdb -> full tt id as the term (no keyword)",
			query: search.Query{Categories: []string{"96"}, IMDBID: "1234567"},
			want:  base + "t.json?96;q=tt1234567",
		},
		{
			name:  "tv season+episode -> SxxExx term",
			query: search.Query{Categories: []string{"7"}, Keywords: "some show", Season: "1", Ep: "2"},
			want:  base + "t.json?7;q=some+show+S01E02",
		},
		{
			name:  "freeleech_only -> ;free before q",
			cfg:   map[string]string{"cookie": credCookie, "freeleech_only": "True"},
			query: search.Query{Categories: []string{"29"}, Keywords: "dune"},
			want:  base + "t.json?29;free;q=dune",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := tc.cfg
			if cfg == nil {
				cfg = map[string]string{"cookie": credCookie, "user_agent": credUA}
			}
			got := testDriver(t, nil, cfg).buildSearchURL(tc.query)
			if got != tc.want {
				t.Errorf("buildSearchURL = %q, want %q", got, tc.want)
			}
			assertNoSecret(t, got)
		})
	}
}

// TestSearchIssuesCookieRequest proves Search drives the built URL with the Cookie and
// User-Agent headers attached, the JSON Accept header set, and that the served
// (recorded) URL never carries the cookie secret (only the header does).
func TestSearchIssuesCookieRequest(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search_results.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return resp(stdhttp.StatusOK, string(body))
	}}
	d := testDriver(t, doer, nil)
	releases, err := d.Search(context.Background(), search.Query{Categories: []string{"29"}, Keywords: "dune"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("parsed %d releases, want 2", len(releases))
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.reqs))
	}
	got := doer.reqs[0]
	if got.method != stdhttp.MethodGet {
		t.Errorf("method = %s, want GET", got.method)
	}
	if got.cookie != credCookie {
		t.Errorf("Cookie header = %q, want the configured cookie", got.cookie)
	}
	if got.userAgent != credUA {
		t.Errorf("User-Agent header = %q, want the configured UA", got.userAgent)
	}
	if got.accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", got.accept)
	}
	if !strings.HasPrefix(got.url, base+"t.json?29;q=dune") {
		t.Errorf("recorded request = %q", got.url)
	}
	// The secret rides only in the Cookie header, never the URL.
	assertNoSecret(t, got.url)
}

// TestSearchStatusDispatch proves Search maps the response status: 429/503 are
// rate-limit errors; 401/403 and a 3xx redirect to /login.php are auth failures.
func TestSearchStatusDispatch(t *testing.T) {
	t.Parallel()
	mkStatus := func(status int) *driver {
		return testDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
			return resp(status, "nope")
		}}, nil)
	}
	mkRedirect := func(status int, loc string) *driver {
		return testDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
			return redirectResp(status, loc)
		}}, nil)
	}

	var rl *search.RateLimitedError
	for _, status := range []int{stdhttp.StatusTooManyRequests, stdhttp.StatusServiceUnavailable} {
		_, err := mkStatus(status).Search(context.Background(), search.Query{Keywords: "x"})
		if !errors.As(err, &rl) {
			t.Errorf("%d: err = %v, want *search.RateLimitedError", status, err)
		}
	}

	for _, status := range []int{stdhttp.StatusUnauthorized, stdhttp.StatusForbidden} {
		_, err := mkStatus(status).Search(context.Background(), search.Query{Keywords: "x"})
		if !errors.Is(err, login.ErrLoginFailed) {
			t.Errorf("%d: err = %v, want login.ErrLoginFailed", status, err)
		}
	}

	// A 302 redirect to /login.php is the TorrentDay stale-cookie auth failure.
	_, err := mkRedirect(stdhttp.StatusFound, base+"login.php").Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("login redirect: err = %v, want login.ErrLoginFailed", err)
	}

	// A non-login redirect is not an auth failure (it is a generic non-2xx error).
	_, err = mkRedirect(stdhttp.StatusFound, base+"somewhere.php").Search(context.Background(), search.Query{Keywords: "x"})
	if err == nil || errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("non-login redirect: err = %v, want a non-auth error", err)
	}
}

// TestSearchAuthFailureScrubsCookie proves an auth failure (login redirect) never leaks
// the session cookie into the returned error string.
func TestSearchAuthFailureScrubsCookie(t *testing.T) {
	t.Parallel()
	d := testDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return redirectResp(stdhttp.StatusFound, base+"login.php")
	}}, nil)
	_, err := d.Search(context.Background(), search.Query{Keywords: "x"})
	if err == nil {
		t.Fatal("Search: want an auth error, got nil")
	}
	assertNoSecret(t, err.Error())
}

// TestSearchEmptyResult proves the literal `[]` body yields zero releases and no error.
func TestSearchEmptyResult(t *testing.T) {
	t.Parallel()
	d := testDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return resp(stdhttp.StatusOK, "[]")
	}}, nil)
	releases, err := d.Search(context.Background(), search.Query{Keywords: "nothing"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 0 {
		t.Errorf("got %d releases, want 0", len(releases))
	}
}

// TestEpisodeSearchString covers the SxxExx rendering used in the search term.
func TestEpisodeSearchString(t *testing.T) {
	t.Parallel()
	cases := []struct{ season, ep, want string }{
		{"", "", ""},
		{"0", "5", ""},
		{"1", "", "S01"},
		{"1", "2", "S01E02"},
		{"12", "5", "S12E05"},
	}
	for _, tc := range cases {
		if got := episodeSearchString(tc.season, tc.ep); got != tc.want {
			t.Errorf("episodeSearchString(%q,%q) = %q, want %q", tc.season, tc.ep, got, tc.want)
		}
	}
}
