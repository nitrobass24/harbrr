package hdbits

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

// recordedReq captures one issued request for assertions a black-box transport cannot
// make (the body — which carries the username + passkey — the headers, the URL).
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
// torrentQuery harbrr emits per search type against Prowlarr's HDBitsRequestGenerator
// contract (username/passkey top-level, the limit, the movie-term [\W]+ sanitization, the
// verbatim TV/id term, imdb.id, tvdb.id+season+episode, the daily date string, and the
// category int array).
func TestBuildRequest(t *testing.T) {
	t.Parallel()
	d := &driver{cfg: creds()}
	cred := `"username":"` + credUser + `","passkey":"` + credPass + `"`
	cases := []struct {
		name  string
		query search.Query
		want  string
	}{
		{"empty/browse", search.Query{}, `{` + cred + `,"limit":100}`},
		{"keyword movie sanitized", search.Query{Keywords: "The.Matrix (1999)"}, `{` + cred + `,"search":"The Matrix 1999","limit":100}`},
		{"keyword already clean", search.Query{Keywords: "the wire"}, `{` + cred + `,"search":"the wire","limit":100}`},
		{"imdbid", search.Query{IMDBID: "tt0133093", Keywords: "the matrix"}, `{` + cred + `,"search":"the matrix","imdb":{"id":133093},"limit":100}`},
		{"imdbid bare numeric", search.Query{IMDBID: "133093"}, `{` + cred + `,"imdb":{"id":133093},"limit":100}`},
		{"tvdb id+season+episode (no extra search)", search.Query{TVDBID: "81189", Keywords: "some.show", Season: "1", Ep: "2"}, `{` + cred + `,"tvdb":{"id":81189,"season":1,"episode":"2"},"limit":100}`},
		{"tvdb season only", search.Query{TVDBID: "81189", Season: "3"}, `{` + cred + `,"tvdb":{"id":81189,"season":3},"limit":100}`},
		{"tvdb daily date", search.Query{TVDBID: "81189", Season: "2024", Ep: "01/15"}, `{` + cred + `,"search":"2024-01-15","tvdb":{"id":81189},"limit":100}`},
		{"season+episode without id appends SxxExx", search.Query{Keywords: "some.show", Season: "1", Ep: "2"}, `{` + cred + `,"search":"some.show S01E02","limit":100}`},
		{"season only without id appends Sxx", search.Query{Keywords: "some.show", Season: "1"}, `{` + cred + `,"search":"some.show S01","limit":100}`},
		{"daily without id appends date", search.Query{Keywords: "some.show", Season: "2024", Ep: "01/15"}, `{` + cred + `,"search":"some.show 2024.01.15","limit":100}`},
		{"categories", search.Query{Keywords: "x", Categories: []string{"1", "2", "1"}}, `{` + cred + `,"search":"x","category":[1,2],"limit":100}`},
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

// TestSearchIssuesJSONPost proves Search drives a JSON POST to the api/torrents endpoint
// with the username + passkey inside the body (never the URL), Content-Type and Accept
// application/json, and that the recorded URL leaks neither secret.
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
	if len(got) != 3 {
		t.Fatalf("releases = %d, want 3", len(got))
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
	if !strings.HasSuffix(r.url, "/api/torrents") {
		t.Errorf("url = %q, want the api/torrents endpoint", r.url)
	}
	// Both secrets MUST be in the body but NEVER in the URL (nor after redaction).
	if !strings.Contains(r.body, credUser) || !strings.Contains(r.body, credPass) {
		t.Errorf("body does not carry both credentials as top-level fields")
	}
	for _, secret := range []string{credUser, credPass} {
		if strings.Contains(r.url, secret) {
			t.Errorf("URL leaks a secret: %q", r.url)
		}
		if strings.Contains(apphttp.RedactURL(r.url), secret) {
			t.Errorf("RedactURL leaks a secret")
		}
	}
	// Confirm the on-wire body carries username + passkey as top-level fields (decode raw
	// so the check is independent of the Go type that produced it).
	var req struct {
		Username string `json:"username"`
		Passkey  string `json:"passkey"`
	}
	if err := json.Unmarshal([]byte(r.body), &req); err != nil {
		t.Fatalf("decode recorded body: %v", err)
	}
	if req.Username != credUser || req.Passkey != credPass {
		t.Errorf("body credentials = %q/%q, want the username/passkey", req.Username, req.Passkey)
	}
}

// TestSearchPopulatedResponse proves a populated 200 envelope (status==0) returns the
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

// TestSearchStatusDispatch proves Search maps the HTTP status the contract requires: 401 is
// an auth failure (login.ErrLoginFailed); 403 is HDBits' query/rate-limit so it (alongside
// 429/503) is a RateLimitedError, not an auth failure; any other non-2xx is a plain error.
func TestSearchStatusDispatch(t *testing.T) {
	t.Parallel()
	mk := func(status int) *driver {
		return liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
			return mkResp(status, "nope")
		}})
	}

	_, err := mk(stdhttp.StatusUnauthorized).Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("HTTP 401: err = %v, want login.ErrLoginFailed", err)
	}

	// 403 must classify as a rate-limit (Prowlarr's RequestLimitReached), never an auth
	// failure, so working creds are not misreported when the per-query budget is exhausted.
	for _, status := range []int{stdhttp.StatusForbidden, stdhttp.StatusTooManyRequests, stdhttp.StatusServiceUnavailable} {
		_, err := mk(status).Search(context.Background(), search.Query{Keywords: "x"})
		var rl *search.RateLimitedError
		if !errors.As(err, &rl) {
			t.Errorf("HTTP %d: err = %v, want *search.RateLimitedError", status, err)
		}
		if errors.Is(err, login.ErrLoginFailed) {
			t.Errorf("HTTP %d: err = %v, must NOT be login.ErrLoginFailed", status, err)
		}
	}

	if _, err := mk(stdhttp.StatusInternalServerError).Search(context.Background(), search.Query{}); err == nil {
		t.Errorf("HTTP 500: err = nil, want an error")
	}
}

// TestSearchAuthFailureEnvelope proves a 200 response carrying the status-5 auth envelope
// surfaces as login.ErrLoginFailed (the in-body credential signal), and that neither
// secret leaks into the error string.
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
	for _, secret := range []string{credUser, credPass} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaks a secret: %v", err)
		}
	}
}

// TestTestAction proves Test() returns nil on a good (200, status==0) probe and an auth
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

func TestDailyDate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		season, ep, want string
		ok               bool
	}{
		{"2024", "01/15", "2024-01-15", true},
		{"1", "2", "", false},        // a normal season, not a year
		{"2024", "13/40", "", false}, // invalid month/day
		{"", "", "", false},
	}
	for _, c := range cases {
		got, ok := dailyDate(c.season, c.ep)
		if ok != c.ok || got != c.want {
			t.Errorf("dailyDate(%q,%q) = (%q,%v), want (%q,%v)", c.season, c.ep, got, ok, c.want, c.ok)
		}
	}
}

func TestSanitizeMovieTerm(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"The.Matrix (1999)", "The Matrix 1999"},
		{"the wire", "the wire"},
		{"  Trimmed--Me  ", "Trimmed Me"},
		{"a+b/c", "a b c"},
	}
	for _, c := range cases {
		if got := sanitizeMovieTerm(c.in); got != c.want {
			t.Errorf("sanitizeMovieTerm(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
