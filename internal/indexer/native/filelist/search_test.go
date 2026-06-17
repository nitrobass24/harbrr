package filelist

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	stdhttp "net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// distinctive credential values so a redaction check can prove they never escape.
const (
	credUser = "theuser"
	credPass = "PASSKEY-SECRET-9f8e"
)

func fixedClock() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

// recordedReq captures one issued request for assertions a black-box transport cannot
// make (the Basic header, the Accept header, the full URL).
type recordedReq struct {
	method, url, body, auth, accept string
}

// scriptDoer records every request and serves a scripted response.
type scriptDoer struct {
	handler func(req *stdhttp.Request, body string) *stdhttp.Response
	reqs    []recordedReq
}

func (s *scriptDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	var body string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	s.reqs = append(s.reqs, recordedReq{
		method: req.Method,
		url:    req.URL.String(),
		body:   body,
		auth:   req.Header.Get("Authorization"),
		accept: req.Header.Get("Accept"),
	})
	return s.handler(req, body), nil
}

func resp(status int, body string) *stdhttp.Response {
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: stdhttp.Header{}}
}

// builderDriver is a credential-free driver for the pure query-string builder tests.
func builderDriver(cfg map[string]string) *driver {
	if cfg == nil {
		cfg = map[string]string{}
	}
	return &driver{cfg: cfg, baseURL: "https://filelist.test/", clock: fixedClock}
}

// liveDriver is a driver wired to a scriptDoer with the secret credentials, for the
// request-shape / redaction tests. doer may be nil for the transport-error case (the
// caller sets its own doer afterwards).
func liveDriver(doer *scriptDoer) *driver {
	def := Families()[0].Definition
	d, err := New(native.Params{
		Def:   def,
		Cfg:   map[string]string{"username": credUser, "passkey": credPass},
		Doer:  doer,
		Clock: fixedClock,
	})
	if err != nil {
		panic(err)
	}
	drv := d.(*driver)
	drv.baseURL = "https://filelist.test/"
	if doer != nil {
		drv.doer = doer
	}
	return drv
}

// TestBuildSearchURL is the parity gate for the request: it asserts the exact query
// string harbrr emits per search type against Prowlarr's FileListRequestGenerator
// contract (action, type imdb/name, query, season/episode, category csv, freeleech).
func TestBuildSearchURL(t *testing.T) {
	t.Parallel()
	const endpoint = "https://filelist.test/api.php"
	cases := []struct {
		name  string
		cfg   map[string]string
		query search.Query
		want  url.Values
	}{
		{
			name:  "no criteria -> latest-torrents",
			query: search.Query{},
			want:  url.Values{"action": {"latest-torrents"}},
		},
		{
			name:  "name search",
			query: search.Query{Keywords: "the matrix", Categories: []string{"4"}},
			want:  url.Values{"action": {"search-torrents"}, "type": {"name"}, "query": {"the matrix"}, "category": {"4"}},
		},
		{
			name:  "imdb search -> full id, type imdb",
			query: search.Query{IMDBID: "133093", Categories: []string{"4"}},
			want:  url.Values{"action": {"search-torrents"}, "type": {"imdb"}, "query": {"tt0133093"}, "category": {"4"}},
		},
		{
			name:  "tv name + season/episode",
			query: search.Query{Keywords: "some show", Season: "1", Ep: "2", Categories: []string{"21"}},
			want:  url.Values{"action": {"search-torrents"}, "type": {"name"}, "query": {"some show"}, "season": {"1"}, "episode": {"2"}, "category": {"21"}},
		},
		{
			name:  "tv imdb + season/episode",
			query: search.Query{IMDBID: "tt0944947", Season: "1", Ep: "2"},
			want:  url.Values{"action": {"search-torrents"}, "type": {"imdb"}, "query": {"tt0944947"}, "season": {"1"}, "episode": {"2"}},
		},
		{
			name:  "multiple categories -> csv, deduped",
			query: search.Query{Keywords: "foo", Categories: []string{"4", "21", "4"}},
			want:  url.Values{"action": {"search-torrents"}, "type": {"name"}, "query": {"foo"}, "category": {"4,21"}},
		},
		{
			name:  "freeleech_only -> freeleech=1",
			cfg:   map[string]string{"freeleech_only": "True"},
			query: search.Query{Keywords: "dune"},
			want:  url.Values{"action": {"search-torrents"}, "type": {"name"}, "query": {"dune"}, "freeleech": {"1"}},
		},
		{
			name:  "daily name search -> date appended, no season/episode",
			query: search.Query{Keywords: "the daily show", Season: "2024", Ep: "01/15"},
			want:  url.Values{"action": {"search-torrents"}, "type": {"name"}, "query": {"the daily show 2024.01.15"}},
		},
		{
			name:  "daily imdb search -> skipped (latest-torrents)",
			query: search.Query{IMDBID: "tt0944947", Season: "2024", Ep: "01/15"},
			want:  url.Values{"action": {"latest-torrents"}},
		},
		{
			name:  "sanitize drops disallowed punctuation",
			query: search.Query{Keywords: "Money$ Heist: 4!"},
			want:  url.Values{"action": {"search-torrents"}, "type": {"name"}, "query": {"Money Heist 4"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := builderDriver(tc.cfg).buildSearchURL(tc.query)
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

// TestSearchIssuesBasicRequest proves Search drives the built URL with the Basic
// header attached, that the search forces JSON, and that the served (recorded) URL
// leaks no passkey (the raw passkey never enters the query — only the Basic header,
// where it is base64).
func TestSearchIssuesBasicRequest(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return resp(stdhttp.StatusOK, `[]`)
	}}
	d := liveDriver(doer)
	if _, err := d.Search(context.Background(), search.Query{Keywords: "dune", Categories: []string{"4"}}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.reqs))
	}
	r := doer.reqs[0]
	if r.method != stdhttp.MethodGet {
		t.Errorf("method = %s, want GET", r.method)
	}
	if r.accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", r.accept)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(credUser+":"+credPass))
	if r.auth != wantAuth {
		t.Errorf("Authorization = %q, want the Basic header", r.auth)
	}
	u, _ := url.Parse(r.url)
	if u.Query().Get("type") != "name" || u.Query().Get("query") != "dune" {
		t.Errorf("recorded query = %v", u.Query())
	}
	// Redaction: the raw passkey must not appear anywhere in the URL/query.
	assertNoPasskeyInURL(t, r.url)
}

// TestSearchStatusDispatch proves Search maps the response status the way the contract
// requires: 403 is an auth failure, 429 is a rate-limit error.
func TestSearchStatusDispatch(t *testing.T) {
	t.Parallel()
	mk := func(status int, body string) *driver {
		return liveDriver(&scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
			return resp(status, body)
		}})
	}

	_, err := mk(stdhttp.StatusForbidden, "nope").Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("403: err = %v, want login.ErrLoginFailed", err)
	}

	_, err = mk(stdhttp.StatusUnauthorized, "nope").Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("401: err = %v, want login.ErrLoginFailed", err)
	}

	_, err = mk(stdhttp.StatusTooManyRequests, "nope").Search(context.Background(), search.Query{Keywords: "x"})
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Errorf("429: err = %v, want *search.RateLimitedError", err)
	}
}

// TestTestAction proves Test() returns nil on a good (200) probe and an auth failure
// on a 403.
func TestTestAction(t *testing.T) {
	t.Parallel()
	ok := liveDriver(&scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return resp(stdhttp.StatusOK, `[]`)
	}})
	if err := ok.Test(context.Background()); err != nil {
		t.Errorf("Test on good creds = %v, want nil", err)
	}
	bad := liveDriver(&scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return resp(stdhttp.StatusForbidden, "nope")
	}})
	if err := bad.Test(context.Background()); !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("Test on bad creds = %v, want login.ErrLoginFailed", err)
	}
}

func TestSanitizeSearchTerm(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"the matrix", "the matrix"},
		{"Money$ Heist: 4!", "Money Heist 4"},
		{"Amélie", "Amélie"},
		{"a — b", "a - b"},
		{"a–-—b", "a-b"},
		{"it’s", "it's"},
		{"WALL[E]+ (2008)", "WALL[E]+ (2008)"},
	}
	for _, tc := range cases {
		if got := sanitizeSearchTerm(tc.in); got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
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

// assertNoPasskeyInURL proves the raw passkey value never appears in a URL (the search
// passkey rides as a header; a download URL carries it as a query param but that URL
// only goes to /dl, never a recorded search request). It also confirms the redactors
// keep it out of derived strings.
func assertNoPasskeyInURL(t *testing.T, raw string) {
	t.Helper()
	if strings.Contains(raw, credPass) {
		t.Errorf("URL leaks the raw passkey: %q", raw)
	}
	if strings.Contains(apphttp.RedactURL(raw), credPass) {
		t.Errorf("RedactURL leaks the raw passkey: %q", apphttp.RedactURL(raw))
	}
}
