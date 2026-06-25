package animebytes

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// credUser is the configured username (an identifier, carried in the query). credPass is
// the synthetic passkey defined in parse_test.go and reused here.
const credUser = "theuser"

func fixedClock() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

// recordedReq captures one issued request for assertions a black-box transport cannot
// make (the full URL, the Accept header, the method).
type recordedReq struct {
	method, url, accept string
}

// scriptDoer records every request and serves a scripted response.
type scriptDoer struct {
	handler func(req *stdhttp.Request) *stdhttp.Response
	reqs    []recordedReq
}

func (s *scriptDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	s.reqs = append(s.reqs, recordedReq{
		method: req.Method,
		url:    req.URL.String(),
		accept: req.Header.Get("Accept"),
	})
	return s.handler(req), nil
}

func httpResp(status int, body string) *stdhttp.Response {
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: stdhttp.Header{}}
}

// builderDriver is a credential-bearing driver for the pure query-string builder tests.
// The username + passkey are set so the builder emits them; the doer is nil (the builder
// never issues a request).
func builderDriver(cfg map[string]string) *driver {
	base := map[string]string{"username": credUser, "passkey": credPass}
	for k, v := range cfg {
		base[k] = v
	}
	return &driver{cfg: base, baseURL: "https://animebytes.tv/", clock: fixedClock}
}

// liveDriver wires a driver to a scriptDoer with the secret credentials, for the
// request-shape / redaction / status tests.
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
	drv.baseURL = "https://animebytes.tv/"
	return drv
}

// TestBuildSearchURL is the parity gate for the request: it asserts the exact query
// AnimeBytes emits per search type against Prowlarr's AnimeBytesRequestGenerator contract
// (username + torrent_pass auth, sort/way, type anime|music, the cleaned searchstr, the
// limit, music artist/album/year, the category flags, freeleech). The credentials ride in
// the query (so the URL is secret-bearing) — the redaction guarantee is asserted
// separately.
func TestBuildSearchURL(t *testing.T) {
	t.Parallel()
	const endpoint = "https://animebytes.tv/scrape.php"
	auth := url.Values{"username": {credUser}, "torrent_pass": {credPass}, "sort": {"grouptime"}, "way": {"desc"}}
	merge := func(extra url.Values) url.Values {
		out := url.Values{}
		for k, v := range auth {
			out[k] = append([]string(nil), v...)
		}
		for k, v := range extra {
			out[k] = append([]string(nil), v...)
		}
		return out
	}
	cases := []struct {
		name  string
		cfg   map[string]string
		query search.Query
		want  url.Values
	}{
		{
			name:  "empty -> anime probe, limit 20",
			query: search.Query{},
			want:  merge(url.Values{"type": {"anime"}, "searchstr": {""}, "limit": {"20"}}),
		},
		{
			name:  "keyword anime search, limit 50",
			query: search.Query{Keywords: "Cowboy Bebop"},
			want:  merge(url.Values{"type": {"anime"}, "searchstr": {"Cowboy Bebop"}, "limit": {"50"}}),
		},
		{
			name:  "trailing episode number stripped",
			query: search.Query{Keywords: "Naruto 5"},
			want:  merge(url.Values{"type": {"anime"}, "searchstr": {"Naruto"}, "limit": {"50"}}),
		},
		{
			name:  "music search -> artist/album",
			query: search.Query{Artist: "Yoko Kanno", Album: "Tank", Year: "1998"},
			want: merge(url.Values{
				"type": {"music"}, "searchstr": {""}, "limit": {"20"},
				"artistnames": {"Yoko Kanno"}, "groupname": {"Tank"}, "year": {"1998"},
			}),
		},
		{
			name:  "category flags set to 1, deduped",
			query: search.Query{Keywords: "x", Categories: []string{"anime[tv_series]", "anime[ova]", "anime[tv_series]"}},
			want: merge(url.Values{
				"type": {"anime"}, "searchstr": {"x"}, "limit": {"50"},
				"anime[tv_series]": {"1"}, "anime[ova]": {"1"},
			}),
		},
		{
			name:  "freeleech_only -> freeleech=1",
			cfg:   map[string]string{"freeleech_only": "True"},
			query: search.Query{Keywords: "dune"},
			want:  merge(url.Values{"type": {"anime"}, "searchstr": {"dune"}, "limit": {"50"}, "freeleech": {"1"}}),
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

// TestSearchSecretsNeverInRecordedURL proves that although the username + passkey must
// ride in the query (AnimeBytes auth), the passkey is stripped by apphttp.RedactURL — the
// chokepoint every log/error routes URLs through — so it never reaches a log/trace.
func TestSearchSecretsNeverInRecordedURL(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return httpResp(stdhttp.StatusOK, `{"Matches":0,"Groups":[]}`)
	}}
	d := liveDriver(doer)
	if _, err := d.Search(context.Background(), search.Query{Keywords: "dune"}); err != nil {
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
	// The raw URL carries the passkey (AB auth), but RedactURL must strip it.
	redacted := apphttp.RedactURL(r.url)
	if strings.Contains(redacted, credPass) {
		t.Errorf("RedactURL leaks the passkey: %q", redacted)
	}
	if u, _ := url.Parse(r.url); u.Query().Get("torrent_pass") != credPass {
		t.Error("torrent_pass should ride in the query (AnimeBytes auth)")
	}
}

// TestSearchPopulated proves a populated 200 response is parsed into releases (an
// end-to-end Search wiring check over the golden fixture).
func TestSearchPopulated(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/scrape_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return httpResp(stdhttp.StatusOK, string(body))
	}}
	got, err := liveDriver(doer).Search(context.Background(), search.Query{Keywords: "anime"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("releases = %d, want 2", len(got))
	}
}

// TestSearchStatusDispatch proves Search maps the response status the way the contract
// requires: 401/403 -> login.ErrLoginFailed, 429/503 -> rate-limit, other non-2xx ->
// parse error.
func TestSearchStatusDispatch(t *testing.T) {
	t.Parallel()
	mk := func(status int) *driver {
		return liveDriver(&scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
			return httpResp(status, "nope")
		}})
	}

	for _, status := range []int{stdhttp.StatusUnauthorized, stdhttp.StatusForbidden} {
		_, err := mk(status).Search(context.Background(), search.Query{Keywords: "x"})
		if !errors.Is(err, login.ErrLoginFailed) {
			t.Errorf("HTTP %d: err = %v, want login.ErrLoginFailed", status, err)
		}
	}

	for _, status := range []int{stdhttp.StatusTooManyRequests, stdhttp.StatusServiceUnavailable} {
		_, err := mk(status).Search(context.Background(), search.Query{Keywords: "x"})
		var rl *search.RateLimitedError
		if !errors.As(err, &rl) {
			t.Errorf("HTTP %d: err = %v, want *search.RateLimitedError", status, err)
		}
	}

	_, err := mk(stdhttp.StatusInternalServerError).Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.Is(err, search.ErrParseError) {
		t.Errorf("HTTP 500: err = %v, want search.ErrParseError", err)
	}
}

// TestSearchAuthErrorEnvelope proves the JSON {"error":…} envelope AnimeBytes returns with
// HTTP 200 surfaces as login.ErrLoginFailed for an auth-looking message, with the echoed
// passkey scrubbed from the error.
func TestSearchAuthErrorEnvelope(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return httpResp(stdhttp.StatusOK, `{"error":"Invalid passkey `+credPass+`"}`)
	}}
	_, err := liveDriver(doer).Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("err = %v, want login.ErrLoginFailed", err)
	}
	if strings.Contains(err.Error(), credPass) {
		t.Errorf("error leaks the passkey: %v", err)
	}
}

// TestCleanSearchTerm pins Prowlarr's CleanSearchTerm behavior: trailing episode/number
// tokens and a trailing "The Movie" are stripped.
func TestCleanSearchTerm(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"Cowboy Bebop", "Cowboy Bebop"},
		{"Naruto 5", "Naruto"},
		{"Show S01E05", "Show"},
		{"Show 5x05", "Show"},
		{"Bleach The Movie", "Bleach"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := cleanSearchTerm(tc.in); got != tc.want {
			t.Errorf("cleanSearchTerm(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
