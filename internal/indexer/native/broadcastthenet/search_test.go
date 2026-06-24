package broadcastthenet

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
// make (the body — which carries the API key — the Content-Type header, the URL).
type recordedReq struct {
	method, url, body, contentType string
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
	})
	return s.handler(req, body), nil
}

func mkResp(status int, body string) *stdhttp.Response {
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: stdhttp.Header{}}
}

// liveDriver wires a driver to a scriptDoer with the synthetic API key, for the
// request-shape / redaction / status tests.
func liveDriver(t *testing.T, doer *scriptDoer) *driver {
	t.Helper()
	def := Families()[0].Definition
	d, err := New(native.Params{
		Def:   def,
		Cfg:   map[string]string{"apikey": credAPIKey},
		Doer:  doer,
		Clock: fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

// TestBuildParameters is the parity gate for the getTorrents "parameters" object: it
// asserts the exact object harbrr emits per search type against Prowlarr's
// BroadcastheNetRequestGenerator contract (Tvdb/Tvrage, Search space->'%', the
// season/episode/daily Name+Category patterns, and the empty {} for browse).
func TestBuildParameters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		query search.Query
		want  btnParameters
	}{
		{name: "empty -> RSS/browse", query: search.Query{}, want: btnParameters{}},
		{name: "keyword spaces -> %", query: search.Query{Keywords: "the wire"}, want: btnParameters{Search: "the%wire"}},
		{name: "tvdbid", query: search.Query{TVDBID: "81189"}, want: btnParameters{Tvdb: "81189"}},
		{name: "rageid only", query: search.Query{RageID: "55555"}, want: btnParameters{Tvrage: "55555"}},
		{name: "tvdb wins over rage", query: search.Query{TVDBID: "81189", RageID: "55555"}, want: btnParameters{Tvdb: "81189"}},
		{name: "standard episode S01E02", query: search.Query{Season: "1", Ep: "2"}, want: btnParameters{Category: "Episode", Name: "S01E02%"}},
		{name: "double-digit S10E20", query: search.Query{Season: "10", Ep: "20"}, want: btnParameters{Category: "Episode", Name: "S10E20%"}},
		{name: "season only -> S01E%", query: search.Query{Season: "1"}, want: btnParameters{Category: "Episode", Name: "S01E%"}},
		{name: "daily", query: search.Query{Season: "2024", Ep: "01/15"}, want: btnParameters{Category: "Episode", Name: "2024.01.15%"}},
		{
			name:  "keyword + season/episode coexist",
			query: search.Query{Keywords: "the wire", Season: "1", Ep: "2"},
			want:  btnParameters{Search: "the%wire", Category: "Episode", Name: "S01E02%"},
		},
		{
			name:  "tvdb + keyword coexist",
			query: search.Query{Keywords: "the wire", TVDBID: "81189"},
			want:  btnParameters{Tvdb: "81189", Search: "the%wire"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := (&driver{}).buildParameters(tc.query); got != tc.want {
				t.Errorf("buildParameters =\n  %+v\nwant\n  %+v", got, tc.want)
			}
		})
	}
}

// TestBuildRPCBodyParity asserts the exact JSON-RPC body harbrr emits: params[] order is
// [apikey, parametersObj, results, offset], method getTorrents, and the API key is
// params[0] inside the body.
func TestBuildRPCBodyParity(t *testing.T) {
	t.Parallel()
	d := &driver{cfg: map[string]string{"apikey": credAPIKey}}
	cases := []struct {
		name  string
		query search.Query
		want  string
	}{
		{"empty/RSS", search.Query{}, `{"jsonrpc":"2.0","method":"getTorrents","params":["` + credAPIKey + `",{},100,0],"id":1}`},
		{"keyword", search.Query{Keywords: "the wire"}, `{"jsonrpc":"2.0","method":"getTorrents","params":["` + credAPIKey + `",{"Search":"the%wire"},100,0],"id":1}`},
		{"tvdbid", search.Query{TVDBID: "81189"}, `{"jsonrpc":"2.0","method":"getTorrents","params":["` + credAPIKey + `",{"Tvdb":"81189"},100,0],"id":1}`},
		{"S01E02", search.Query{Season: "1", Ep: "2"}, `{"jsonrpc":"2.0","method":"getTorrents","params":["` + credAPIKey + `",{"Category":"Episode","Name":"S01E02%"},100,0],"id":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body, err := d.buildRPCBody(d.buildParameters(tc.query), pageResults, pageOffset)
			if err != nil {
				t.Fatalf("buildRPCBody: %v", err)
			}
			if string(body) != tc.want {
				t.Errorf("body =\n  %s\nwant\n  %s", body, tc.want)
			}
		})
	}
}

// TestSearchIssuesRPCPost proves Search drives a JSON-RPC POST to the BTN endpoint with
// the API key inside the body (never the URL), Content-Type application/json, and that
// the recorded URL leaks no API key.
func TestSearchIssuesRPCPost(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/getTorrents_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doer := &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusOK, string(body))
	}}
	d := liveDriver(t, doer)
	got, err := d.Search(context.Background(), search.Query{Keywords: "the wire"})
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
	if r.url != "https://api.broadcasthe.net/" {
		t.Errorf("url = %q, want the BTN endpoint", r.url)
	}
	// The API key MUST be in the body (params[0]) but NEVER in the URL.
	if !strings.Contains(r.body, credAPIKey) {
		t.Errorf("body does not carry the API key as params[0]")
	}
	if strings.Contains(r.url, credAPIKey) {
		t.Errorf("URL leaks the API key: %q", r.url)
	}
	if strings.Contains(apphttp.RedactURL(r.url), credAPIKey) {
		t.Errorf("RedactURL leaks the API key")
	}
	// Confirm params[] order is [apikey, parametersObj, results, offset].
	var req rpcRequest
	if err := json.Unmarshal([]byte(r.body), &req); err != nil {
		t.Fatalf("decode recorded body: %v", err)
	}
	if len(req.Params) != 4 || req.Params[0] != credAPIKey {
		t.Errorf("params[] = %v, want [apikey, parametersObj, results, offset]", req.Params)
	}
}

// TestSearchStatusDispatch proves Search maps the response status the way the contract
// requires: 401/403 is an auth failure, a rate-limit status is a RateLimitedError.
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

	_, err := mk(stdhttp.StatusTooManyRequests).Search(context.Background(), search.Query{Keywords: "x"})
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Errorf("429: err = %v, want *search.RateLimitedError", err)
	}
}

// TestSearchBadKeyMapsToLoginFailed proves a 200 response carrying the -32001 JSON-RPC
// error envelope surfaces as login.ErrLoginFailed.
func TestSearchBadKeyMapsToLoginFailed(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/bad_key.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	d := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusOK, string(body))
	}})
	if _, err := d.Search(context.Background(), search.Query{}); !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("err = %v, want login.ErrLoginFailed", err)
	}
}

// TestTestAction proves Test() returns nil on a good (200) probe and an auth failure on
// a 401.
func TestTestAction(t *testing.T) {
	t.Parallel()
	ok := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusOK, `{"result":{"results":"0","torrents":{}}}`)
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
		{"2024", "01/15", "2024.01.15", true},
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
