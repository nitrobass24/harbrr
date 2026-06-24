package passthepopcorn

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// Synthetic credentials used only to prove redaction and header placement. Both PTP
// credentials are secrets; these test-only values never touch a real tracker.
const (
	credAPIUser = "SYNTHETICAPIUSER"
	credAPIKey  = "SYNTHETICAPIKEY"
)

var fixedClock = time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

// recordedReq captures one issued request for assertions a black-box transport cannot
// otherwise make: the URL (which must NEVER carry the apiuser/apikey) and the two
// credential headers (which DO carry the secrets).
type recordedReq struct {
	method, url, apiUser, apiKey, accept string
}

// scriptDoer records every request and serves a single scripted response.
type scriptDoer struct {
	resp *stdhttp.Response
	reqs []recordedReq
}

func (s *scriptDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	s.reqs = append(s.reqs, recordedReq{
		method:  req.Method,
		url:     req.URL.String(),
		apiUser: req.Header.Get(headerAPIUser),
		apiKey:  req.Header.Get(headerAPIKey),
		accept:  req.Header.Get("Accept"),
	})
	if s.resp != nil {
		return s.resp, nil
	}
	return jsonResp(stdhttp.StatusOK, `{"TotalResults":"0","Movies":[]}`), nil
}

// jsonResp builds a response with an application/json Content-Type (PTP requires JSON).
func jsonResp(status int, body string) *stdhttp.Response {
	h := stdhttp.Header{}
	h.Set("Content-Type", "application/json; charset=utf-8")
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: h}
}

// rawResp builds a response with the given Content-Type (for the non-JSON rejection test).
func rawResp(status int, contentType, body string) *stdhttp.Response {
	h := stdhttp.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: h}
}

// searchDriver wires a driver to a scriptDoer with the synthetic credentials.
func searchDriver(t *testing.T, doer search.Doer) *driver {
	t.Helper()
	def := Families()[0].Definition
	d, err := New(native.Params{
		Def:   def,
		Cfg:   map[string]string{"apiuser": credAPIUser, "apikey": credAPIKey},
		Doer:  doer,
		Clock: func() time.Time { return fixedClock },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

// TestBuildSearchURL pins the exact request URL across the browse/keyword/imdb shapes: the
// five fixed params are always present; a keyword fills searchstr; an imdb id fills
// searchstr with the full "tt"-prefixed id (no separate imdb param); an empty query omits
// searchstr (browse/RSS). The URL must NEVER carry a credential.
func TestBuildSearchURL(t *testing.T) {
	t.Parallel()
	base := "https://passthepopcorn.me/torrents.php?"
	fixed := "action=advanced&grouping=0&json=noredirect&order_by=time&order_way=desc"
	cases := []struct {
		name string
		q    search.Query
		want string
	}{
		{"browse", search.Query{}, base + fixed},
		{"keyword", search.Query{Keywords: "The Matrix"}, base + fixed + "&searchstr=The+Matrix"},
		{"imdb", search.Query{IMDBID: "tt0133093"}, base + fixed + "&searchstr=tt0133093"},
		{"imdb digits", search.Query{IMDBID: "133093"}, base + fixed + "&searchstr=tt0133093"},
		{"imdb wins over keyword", search.Query{Keywords: "Matrix", IMDBID: "133093"}, base + fixed + "&searchstr=tt0133093"},
	}
	d := searchDriver(t, &scriptDoer{})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := d.buildSearchURL(c.q)
			if got != c.want {
				t.Errorf("buildSearchURL =\n  %s\nwant\n  %s", got, c.want)
			}
			if strings.Contains(got, credAPIUser) || strings.Contains(got, credAPIKey) {
				t.Errorf("URL must not carry a credential: %s", got)
			}
		})
	}
}

// TestSearchPopulated proves a populated 200 JSON response parses to the full set of
// releases (3 torrents across 2 movie groups) and that the request carried the two
// credential headers while keeping the URL secret-free.
func TestSearchPopulated(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doer := &scriptDoer{resp: jsonResp(stdhttp.StatusOK, string(body))}
	rels, err := searchDriver(t, doer).Search(context.Background(), search.Query{Keywords: "matrix"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rels) != 3 {
		t.Fatalf("got %d releases, want 3", len(rels))
	}

	if len(doer.reqs) != 1 {
		t.Fatalf("issued %d requests, want 1", len(doer.reqs))
	}
	req := doer.reqs[0]
	if req.apiUser != credAPIUser {
		t.Errorf("ApiUser header = %q, want the apiuser secret", req.apiUser)
	}
	if req.apiKey != credAPIKey {
		t.Errorf("ApiKey header = %q, want the apikey secret", req.apiKey)
	}
	if strings.Contains(req.url, credAPIUser) || strings.Contains(req.url, credAPIKey) {
		t.Errorf("URL leaked a credential: %s", req.url)
	}
	if req.accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", req.accept)
	}
}

// TestSearchAuthFailure proves a 401 (and only a 401) maps to login.ErrLoginFailed — PTP
// signals bad creds as 401, while 403 is its query-limit (a rate-limit, asserted in
// TestSearchRateLimited). The error never leaks a credential.
func TestSearchAuthFailure(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{resp: jsonResp(stdhttp.StatusUnauthorized, `{}`)}
	_, err := searchDriver(t, doer).Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("err = %v, want login.ErrLoginFailed", err)
	}
	if err != nil && (strings.Contains(err.Error(), credAPIUser) || strings.Contains(err.Error(), credAPIKey)) {
		t.Errorf("error leaked a credential: %v", err)
	}
}

// TestSearchRateLimited proves a 403 (PTP's query-limit) and a 429/503 each map to a
// RateLimitedError carrying the status and any Retry-After. The parity target (Prowlarr's
// PassThePopcornParser) raises RequestLimitReachedException on 403, so it is a pacing
// signal, not an auth failure — matching PTP's 4s/150-per-hour budget.
func TestSearchRateLimited(t *testing.T) {
	t.Parallel()
	for _, status := range []int{stdhttp.StatusForbidden, stdhttp.StatusTooManyRequests, stdhttp.StatusServiceUnavailable} {
		resp := jsonResp(status, `{}`)
		resp.Header.Set("Retry-After", "30")
		doer := &scriptDoer{resp: resp}
		_, err := searchDriver(t, doer).Search(context.Background(), search.Query{Keywords: "x"})
		var rl *search.RateLimitedError
		if !errors.As(err, &rl) {
			t.Fatalf("status %d: err = %v, want *search.RateLimitedError", status, err)
		}
		if rl.StatusCode != status {
			t.Errorf("status %d: RateLimitedError.StatusCode = %d", status, rl.StatusCode)
		}
	}
}

// TestSearchNon2xx proves an unexpected non-2xx status (not 401/403/429/503) is a plain
// error, not an auth or rate-limit signal.
func TestSearchNon2xx(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{resp: jsonResp(stdhttp.StatusInternalServerError, `{}`)}
	_, err := searchDriver(t, doer).Search(context.Background(), search.Query{Keywords: "x"})
	if err == nil {
		t.Fatal("want an error for a 500 status")
	}
	if errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("500 must not be an auth failure: %v", err)
	}
}

// TestSearchNonJSON proves a 200 with a non-JSON Content-Type is a parse error (Prowlarr
// rejects a non-application/json PTP response before parsing).
func TestSearchNonJSON(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{resp: rawResp(stdhttp.StatusOK, "text/html", "<html>maintenance</html>")}
	_, err := searchDriver(t, doer).Search(context.Background(), search.Query{Keywords: "x"})
	if !errors.Is(err, search.ErrParseError) {
		t.Fatalf("err = %v, want search.ErrParseError", err)
	}
}

// TestIsJSONContentType pins the JSON content-type gate: a bare type, a parameterised
// type, and odd casing are JSON; an empty or non-JSON type is not.
func TestIsJSONContentType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"Application/JSON", true},
		{"text/html", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isJSONContentType(c.in); got != c.want {
			t.Errorf("isJSONContentType(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestScrubSecrets proves both credentials are redacted from an arbitrary string (a
// defensive scrub for any error a future leaf might wrap).
func TestScrubSecrets(t *testing.T) {
	t.Parallel()
	d := searchDriver(t, &scriptDoer{})
	in := "boom user=" + credAPIUser + " key=" + credAPIKey
	got := d.scrubSecrets(in)
	if strings.Contains(got, credAPIUser) || strings.Contains(got, credAPIKey) {
		t.Errorf("scrubSecrets left a credential: %q", got)
	}
}
