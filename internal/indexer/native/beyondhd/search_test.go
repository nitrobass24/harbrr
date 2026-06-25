package beyondhd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	stdhttp "net/http"
	"os"
	"strings"
	"testing"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

func fixedClock() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

// recordedReq captures one issued request for assertions a black-box transport cannot make
// (the body — which carries the rsskey — the headers, and the URL — which carries the
// api_key in its path).
type recordedReq struct {
	method, url, body, contentType, accept string
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
		method:      req.Method,
		url:         req.URL.String(),
		body:        body,
		contentType: req.Header.Get("Content-Type"),
		accept:      req.Header.Get("Accept"),
	})
	return s.handler(req, body), nil
}

func mkResp(status int, body string) *stdhttp.Response {
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: stdhttp.Header{}}
}

// liveDriver wires a driver to a scriptDoer with the synthetic credentials, for the
// request-shape / redaction / status tests.
func liveDriver(t *testing.T, doer *scriptDoer) *driver {
	t.Helper()
	def := Families()[0].Definition
	d, err := New(native.Params{Def: def, Cfg: creds(), Doer: doer, Clock: fixedClock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

// TestBuildRequest is the parity gate for the api/torrents JSON body: it asserts the exact
// bhdRequest harbrr emits per search type against Prowlarr's BeyondHDRequestGenerator
// contract (action+rsskey always present, the verbatim/TV search term, the full tt-prefixed
// imdb_id, the "movie/<id>" tmdb_id, imdb winning over tmdb, and the categories int array).
func TestBuildRequest(t *testing.T) {
	t.Parallel()
	d := &driver{cfg: creds()}
	rss := `"action":"search","rsskey":"` + credRSSKey + `"`
	cases := []struct {
		name  string
		query search.Query
		want  string
	}{
		{"empty/browse", search.Query{}, `{` + rss + `}`},
		{"keyword", search.Query{Keywords: "the matrix"}, `{` + rss + `,"search":"the matrix"}`},
		{"keyword+categories", search.Query{Keywords: "the matrix", Categories: []string{"1", "2", "1"}}, `{` + rss + `,"search":"the matrix","categories":[1,2]}`},
		{"imdbid full", search.Query{IMDBID: "tt0133093"}, `{` + rss + `,"imdb_id":"tt0133093"}`},
		{"imdbid bare numeric gets tt", search.Query{IMDBID: "133093"}, `{` + rss + `,"imdb_id":"tt0133093"}`},
		{"imdbid wins over tmdb", search.Query{IMDBID: "tt0133093", TMDBID: "603"}, `{` + rss + `,"imdb_id":"tt0133093"}`},
		{"imdbid keeps keyword", search.Query{IMDBID: "tt0133093", Keywords: "the matrix"}, `{` + rss + `,"search":"the matrix","imdb_id":"tt0133093"}`},
		{"imdbid keeps season+episode qualifier", search.Query{IMDBID: "tt0944947", Keywords: "some show", Season: "1", Ep: "2"}, `{` + rss + `,"search":"some show S01E02","imdb_id":"tt0944947"}`},
		{"imdbid keeps season-only qualifier", search.Query{IMDBID: "tt0944947", Season: "1"}, `{` + rss + `,"search":"S01","imdb_id":"tt0944947"}`},
		{"tmdbid keeps season+episode qualifier", search.Query{TMDBID: "603", Keywords: "some show", Season: "1", Ep: "2"}, `{` + rss + `,"search":"some show S01E02","tmdb_id":"movie/603"}`},
		{"tmdbid as movie/id", search.Query{TMDBID: "603"}, `{` + rss + `,"tmdb_id":"movie/603"}`},
		{"season+episode appends SxxExx", search.Query{Keywords: "some show", Season: "1", Ep: "2"}, `{` + rss + `,"search":"some show S01E02"}`},
		{"season only appends Sxx", search.Query{Keywords: "some show", Season: "1"}, `{` + rss + `,"search":"some show S01"}`},
		{"daily appends date", search.Query{Keywords: "some show", Season: "2024", Ep: "01/15"}, `{` + rss + `,"search":"some show 2024-01-15"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body, err := d.buildRequest(tc.query)
			if err != nil {
				t.Fatalf("buildRequest: %v", err)
			}
			if string(body) != tc.want {
				t.Errorf("body =\n  %s\nwant\n  %s", body, tc.want)
			}
		})
	}
}

// TestSearchIssuesJSONPost proves Search drives a JSON POST to the api/torrents/{api_key}
// endpoint with the api_key in the URL PATH and the rsskey inside the body, Content-Type
// and Accept application/json, and that the api_key appears only in the URL path (never the
// body) while the rsskey appears only in the body (never the URL).
func TestSearchIssuesJSONPost(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doer := &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusOK, string(body))
	}}
	d := liveDriver(t, doer)
	got, err := d.Search(context.Background(), search.Query{Keywords: "the matrix"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("releases = %d, want 2", len(got))
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.reqs))
	}
	r := doer.reqs[0]
	if r.method != stdhttp.MethodPost {
		t.Errorf("method = %s, want POST", r.method)
	}
	if r.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", r.contentType)
	}
	if r.accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", r.accept)
	}
	if !strings.HasSuffix(r.url, "/api/torrents/"+credAPIKey) {
		t.Errorf("url = %q, want the api/torrents/<api_key> endpoint", r.url)
	}
	// The api_key rides in the URL path (NEVER the body); the rsskey rides in the body
	// (NEVER the URL).
	if strings.Contains(r.body, credAPIKey) {
		t.Errorf("body leaks the api_key (it belongs only in the URL path)")
	}
	if !strings.Contains(r.body, credRSSKey) {
		t.Errorf("body does not carry the rsskey")
	}
	if strings.Contains(r.url, credRSSKey) {
		t.Errorf("URL leaks the rsskey (it belongs only in the body): %q", r.url)
	}
	// Confirm the on-wire body carries the rsskey as a top-level field (decode raw so the
	// check is independent of the Go type that produced it).
	var req struct {
		Action string `json:"action"`
		RSSKey string `json:"rsskey"`
	}
	if err := json.Unmarshal([]byte(r.body), &req); err != nil {
		t.Fatalf("decode recorded body: %v", err)
	}
	if req.Action != "search" || req.RSSKey != credRSSKey {
		t.Errorf("body action/rsskey = %q/%q, want search/<rsskey>", req.Action, req.RSSKey)
	}
}

// TestSearchPopulatedResponse proves a populated 200 envelope (status_code!=0) returns the
// parsed releases.
func TestSearchPopulatedResponse(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	d := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusOK, string(body))
	}})
	got, err := d.Search(context.Background(), search.Query{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("releases = 0, want > 0")
	}
}

// TestSearchStatusDispatch proves Search maps the HTTP status the contract requires: 401 and
// 403 are auth failures (login.ErrLoginFailed); 429/503 are rate-limits (RateLimitedError);
// any other non-2xx is a plain error.
func TestSearchStatusDispatch(t *testing.T) {
	t.Parallel()
	mk := func(status int) *driver {
		return liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
			return mkResp(status, "nope")
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

	if _, err := mk(stdhttp.StatusInternalServerError).Search(context.Background(), search.Query{}); err == nil {
		t.Errorf("HTTP 500: err = nil, want an error")
	}
}

// TestSearchAuthFailureEnvelope proves a 200 response carrying the "Invalid API Key" body
// marker surfaces as login.ErrLoginFailed, and that neither secret leaks into the error
// string.
func TestSearchAuthFailureEnvelope(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/auth_failed.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	d := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusOK, string(body))
	}})
	_, err = d.Search(context.Background(), search.Query{})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("err = %v, want login.ErrLoginFailed", err)
	}
	for _, secret := range []string{credAPIKey, credRSSKey} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaks a secret: %v", err)
		}
	}
}

// TestSearchTransportErrorScrubbed proves a transport (network) error never echoes the
// api_key (which sits in the URL path, where apphttp.RedactURL would NOT redact it) nor the
// rsskey.
func TestSearchTransportErrorScrubbed(t *testing.T) {
	t.Parallel()
	def := Families()[0].Definition
	doer := errDoer{err: errors.New("dial tcp beyond-hd.me/api/torrents/" + credAPIKey + ": connection refused")}
	d, err := New(native.Params{Def: def, Cfg: creds(), Doer: doer, Clock: fixedClock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = d.Search(context.Background(), search.Query{})
	if err == nil {
		t.Fatal("Search: err = nil, want a transport error")
	}
	for _, secret := range []string{credAPIKey, credRSSKey} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("transport error leaks a secret: %v", err)
		}
	}
	// Sanity: apphttp.RedactURL alone does NOT scrub a path-embedded api_key (it only
	// redacts query params), so the driver's own scrubSecrets is what protects the URL
	// surfaced in a transport error.
	if !strings.Contains(apphttp.RedactURL("https://beyond-hd.me/api/torrents/"+credAPIKey), credAPIKey) {
		t.Error("RedactURL unexpectedly scrubbed a path-embedded api_key; the scrub assertion above is no longer load-bearing")
	}
}

// errDoer always fails the request with a fixed transport error.
type errDoer struct{ err error }

func (e errDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

// TestTestAction proves Test() returns nil on a good (200, status_code!=0) probe and an auth
// failure on a 401.
func TestTestAction(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search_empty.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	ok := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusOK, string(body))
	}})
	if err := ok.Test(context.Background()); err != nil {
		t.Errorf("Test on good creds = %v, want nil", err)
	}
	bad := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusUnauthorized, "nope")
	}})
	if err := bad.Test(context.Background()); !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("Test on bad creds = %v, want login.ErrLoginFailed", err)
	}
}
