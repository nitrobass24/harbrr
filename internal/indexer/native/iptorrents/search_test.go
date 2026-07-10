package iptorrents

import (
	"context"
	"errors"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestBuildSearchURL is the parity gate for the request: it asserts the exact query
// IPTorrents emits against Prowlarr's IPTorrentsRequestGenerator contract — the `t`
// path, the per-category `<id>=` params, the Sphinx `+(term)` grouping, the imdb
// `qf=all`, and the freeleech `free=on`.
func TestBuildSearchURL(t *testing.T) {
	t.Parallel()
	const endpoint = "https://iptorrents.com/t"
	cases := []struct {
		name      string
		cfg       map[string]string
		query     search.Query
		wantQuery url.Values
	}{
		{
			name:      "basic keyword",
			query:     search.Query{Keywords: "the matrix"},
			wantQuery: url.Values{"q": {"+(the matrix)"}},
		},
		{
			name:      "single category param is the tracker id with empty value",
			query:     search.Query{Categories: []string{"72"}, Keywords: "dune"},
			wantQuery: url.Values{"72": {""}, "q": {"+(dune)"}},
		},
		{
			name:      "multiple categories deduplicated",
			query:     search.Query{Categories: []string{"72", "73", "72"}, Keywords: "foo"},
			wantQuery: url.Values{"72": {""}, "73": {""}, "q": {"+(foo)"}},
		},
		{
			name:      "imdb -> sphinx group + qf=all",
			query:     search.Query{Categories: []string{"72"}, IMDBID: "133093"},
			wantQuery: url.Values{"72": {""}, "q": {"+(tt0133093)"}, "qf": {"all"}},
		},
		{
			name:      "tv season+episode -> SxxExx term",
			query:     search.Query{Categories: []string{"73"}, Keywords: "some show", Season: "1", Ep: "2"},
			wantQuery: url.Values{"73": {""}, "q": {"+(some show S01E02)"}},
		},
		{
			name:      "tv season only -> trailing wildcard",
			query:     search.Query{Categories: []string{"73"}, Keywords: "some show", Season: "3"},
			wantQuery: url.Values{"73": {""}, "q": {"+(some show S03*)"}},
		},
		{
			name:      "freeleech_only -> free=on",
			cfg:       map[string]string{"cookie": credCookie, "user_agent": credUA, "freeleech_only": "True"},
			query:     search.Query{Categories: []string{"72"}, Keywords: "dune"},
			wantQuery: url.Values{"72": {""}, "free": {"on"}, "q": {"+(dune)"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := testDriver(nil, cfgOr(tc.cfg)).buildSearchURL(tc.query)
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parse %q: %v", raw, err)
			}
			if got := u.Scheme + "://" + u.Host + u.Path; got != endpoint {
				t.Errorf("endpoint = %q, want %q", got, endpoint)
			}
			if got := u.Query(); !equalValues(got, tc.wantQuery) {
				t.Errorf("query =\n  %v\nwant\n  %v", got, tc.wantQuery)
			}
		})
	}
}

// TestSearchIssuesCookieRequest proves Search drives the built URL with the Cookie and
// User-Agent headers attached, the HTML Accept header set, and that the served
// (recorded) URL never carries the cookie secret (only the header does).
func TestSearchIssuesCookieRequest(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return resp(stdhttp.StatusOK, `<table id="torrents"></table>`)
	}}
	d := testDriver(doer, nil)
	if _, err := d.Search(context.Background(), search.Query{Categories: []string{"72"}, Keywords: "dune"}); err != nil {
		t.Fatalf("Search: %v", err)
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
	if got.accept != "text/html" {
		t.Errorf("Accept = %q, want text/html", got.accept)
	}
	u, _ := url.Parse(got.url)
	if u.Path != "/t" || u.Query().Get("q") != "+(dune)" {
		t.Errorf("recorded request = %q", got.url)
	}
	// The secret rides only in the Cookie header, never the URL.
	assertNoSecret(t, got.url)
}

// TestSearchStatusDispatch proves Search maps the response status: 429/503 are
// rate-limit errors, 401/403 are auth failures.
func TestSearchStatusDispatch(t *testing.T) {
	t.Parallel()
	mk := func(status int) *driver {
		return testDriver(&scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
			return resp(status, "nope")
		}}, nil)
	}

	_, err := mk(stdhttp.StatusTooManyRequests).Search(context.Background(), search.Query{Keywords: "x"})
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Errorf("429: err = %v, want *search.RateLimitedError", err)
	}

	_, err = mk(stdhttp.StatusServiceUnavailable).Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.As(err, &rl) {
		t.Errorf("503: err = %v, want *search.RateLimitedError", err)
	}

	_, err = mk(stdhttp.StatusUnauthorized).Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("401: err = %v, want login.ErrLoginFailed", err)
	}

	_, err = mk(stdhttp.StatusForbidden).Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("403: err = %v, want login.ErrLoginFailed", err)
	}
}

// TestSearchTransportErrorHostOnly proves the changed get() transport wrap surfaces only
// the scheme://host of a failing request. The doer returns a real *url.Error whose URL
// hides a fabricated secret in BOTH a path segment and a query param; apphttp.SchemeHost
// / apphttp.RedactURLError rebuild it host-only, so the host survives (it is not a
// secret) while the token, the "/dl/<secret>" path, and "passkey=<secret>" are dropped.
func TestSearchTransportErrorHostOnly(t *testing.T) {
	t.Parallel()
	const secret = "S3CRETTOKEN"
	uerr := &url.Error{
		Op:  "Get",
		URL: "https://iptorrents.example/dl/" + secret + "?passkey=" + secret,
		Err: errors.New("dial tcp: connection refused"),
	}
	d := testDriver(nil, nil)
	d.doer = &errorDoer{err: uerr}

	_, err := d.Search(context.Background(), search.Query{Categories: []string{"72"}, Keywords: "dune"})
	if err == nil {
		t.Fatal("want a transport error")
	}
	if !strings.Contains(err.Error(), "https://iptorrents.example") {
		t.Errorf("host did not survive redaction: %v", err)
	}
	for _, leak := range []string{secret, "/dl/" + secret, "passkey=" + secret} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error leaks %q: %v", leak, err)
		}
	}
	// The configured cookie credential must not leak either.
	assertNoSecret(t, err.Error())
	assertNoSecret(t, apphttp.RedactError(err))
}

func TestFullIMDBID(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"133093", "tt0133093"},
		{"tt0084967", "tt0084967"},
		{"TT123", "tt0000123"},
		{"12345678", "tt12345678"},
		{"not-an-id", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := fullIMDBID(tc.in); got != tc.want {
			t.Errorf("fullIMDBID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

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

func TestFreeleechOnly(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"True", "true", "1", "on", "yes"} {
		if !freeleechOnly(map[string]string{"freeleech_only": v}) {
			t.Errorf("freeleechOnly(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "false", "0", "no"} {
		if freeleechOnly(map[string]string{"freeleech_only": v}) {
			t.Errorf("freeleechOnly(%q) = true, want false", v)
		}
	}
}

func cfgOr(cfg map[string]string) map[string]string {
	if cfg == nil {
		return map[string]string{"cookie": credCookie, "user_agent": credUA}
	}
	return cfg
}

func equalValues(a, b url.Values) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if av[i] != bv[i] {
				return false
			}
		}
	}
	return true
}
