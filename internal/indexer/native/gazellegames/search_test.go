package gazellegames

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// recordedReq captures one issued request for assertions a black-box transport cannot
// otherwise make: the URL (which must NEVER carry the apikey) and the X-API-Key header
// (which carries the apikey secret).
type recordedReq struct {
	method, url, apiKey, accept string
}

// scriptDoer records every request and serves a single scripted response (or a default
// empty success body when none is scripted).
type scriptDoer struct {
	resp *stdhttp.Response
	reqs []recordedReq
}

func (s *scriptDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	s.reqs = append(s.reqs, recordedReq{
		method: req.Method,
		url:    req.URL.String(),
		apiKey: req.Header.Get(apiKeyHeader),
		accept: req.Header.Get("Accept"),
	})
	if s.resp != nil {
		return s.resp, nil
	}
	return mkResp(stdhttp.StatusOK, `{"status":"success","response":[]}`), nil
}

func mkResp(status int, body string) *stdhttp.Response {
	return &stdhttp.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: stdhttp.Header{}}
}

// searchDriver wires a driver to a scriptDoer with the synthetic apikey/passkey.
func searchDriver(t *testing.T, doer search.Doer) *driver {
	t.Helper()
	def := Families()[0].Definition
	d, err := New(native.Params{
		Def:   def,
		Cfg:   map[string]string{"apikey": credAPIKey, "passkey": credPasskey},
		Doer:  doer,
		Clock: func() time.Time { return fixedClock },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

// TestBuildSearchURL is the parity gate for the api.php search request URL: it asserts the
// exact URL harbrr emits per query shape against the Phase-1 confirmed contract (the static
// request/search_type/empty_groups/order_by/order_way params always set; searchstr carries
// the free-text term when present and is omitted otherwise) and that the apikey never
// appears in the URL.
func TestBuildSearchURL(t *testing.T) {
	t.Parallel()
	const base = "https://gazellegames.net/api.php?"
	cases := []struct {
		name  string
		query search.Query
		want  string
	}{
		{
			name:  "empty -> RSS/browse",
			query: search.Query{},
			want:  "empty_groups=filled&order_by=time&order_way=desc&request=search&search_type=torrents",
		},
		{
			name:  "keyword",
			query: search.Query{Keywords: "cool game"},
			want:  "empty_groups=filled&order_by=time&order_way=desc&request=search&search_type=torrents&searchstr=cool+game",
		},
		{
			name:  "keyword trimmed",
			query: search.Query{Keywords: "  cool game  "},
			want:  "empty_groups=filled&order_by=time&order_way=desc&request=search&search_type=torrents&searchstr=cool+game",
		},
		{
			name:  "blank keyword omits searchstr",
			query: search.Query{Keywords: "   "},
			want:  "empty_groups=filled&order_by=time&order_way=desc&request=search&search_type=torrents",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := searchDriver(t, &scriptDoer{})
			got := d.buildSearchURL(tc.query)
			want := base + tc.want
			if got != want {
				t.Errorf("buildSearchURL()\n got=%q\nwant=%q", got, want)
			}
			if strings.Contains(got, credAPIKey) {
				t.Errorf("search URL leaks the apikey: %q", got)
			}
		})
	}
}

// TestSearchAuthHeader proves the X-API-Key header carries the apikey, the apikey is NEVER
// in the recorded URL, the method is GET, and Accept advertises JSON.
func TestSearchAuthHeader(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{}
	d := searchDriver(t, doer)
	if _, err := d.Search(context.Background(), search.Query{Keywords: "x"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("want 1 request, got %d", len(doer.reqs))
	}
	req := doer.reqs[0]
	if req.method != stdhttp.MethodGet {
		t.Errorf("method = %q, want GET", req.method)
	}
	if req.apiKey != credAPIKey {
		t.Errorf("%s = %q, want %q", apiKeyHeader, req.apiKey, credAPIKey)
	}
	if req.accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", req.accept)
	}
	if strings.Contains(req.url, credAPIKey) {
		t.Errorf("URL must NOT carry the apikey, got %q", req.url)
	}
}

// TestSearchSuccessReturnsReleases proves a 200 status:"success" body flows through
// parseSearch into normalized releases.
func TestSearchSuccessReturnsReleases(t *testing.T) {
	t.Parallel()
	body := readFixture(t, "testdata/search.json")
	doer := &scriptDoer{resp: mkResp(stdhttp.StatusOK, string(body))}
	d := searchDriver(t, doer)
	rels, err := d.Search(context.Background(), search.Query{Keywords: "cool game"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rels) == 0 {
		t.Fatalf("want releases, got 0")
	}
}

// TestSearchAuthFailure proves a 401/403 maps to login.ErrLoginFailed and never leaks the
// apikey in the error.
func TestSearchAuthFailure(t *testing.T) {
	t.Parallel()
	for _, code := range []int{stdhttp.StatusUnauthorized, stdhttp.StatusForbidden} {
		doer := &scriptDoer{resp: mkResp(code, "")}
		d := searchDriver(t, doer)
		_, err := d.Search(context.Background(), search.Query{})
		if !errors.Is(err, login.ErrLoginFailed) {
			t.Fatalf("HTTP %d: want login.ErrLoginFailed, got %v", code, err)
		}
		if strings.Contains(err.Error(), credAPIKey) {
			t.Errorf("error leaks the apikey: %v", err)
		}
	}
}

// TestSearchRateLimited proves a 429 surfaces a RateLimitedError carrying the status and
// Retry-After.
func TestSearchRateLimited(t *testing.T) {
	t.Parallel()
	resp := mkResp(stdhttp.StatusTooManyRequests, "")
	resp.Header.Set("Retry-After", "12")
	doer := &scriptDoer{resp: resp}
	d := searchDriver(t, doer)
	_, err := d.Search(context.Background(), search.Query{})
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("want RateLimitedError, got %v", err)
	}
	if rl.StatusCode != stdhttp.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want 429", rl.StatusCode)
	}
	if rl.RetryAfter != 12*time.Second {
		t.Errorf("RetryAfter = %v, want 12s", rl.RetryAfter)
	}
}

// TestSearchServerError proves a non-2xx, non-auth, non-rate-limit status is a plain error.
func TestSearchServerError(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{resp: mkResp(stdhttp.StatusInternalServerError, "boom")}
	d := searchDriver(t, doer)
	_, err := d.Search(context.Background(), search.Query{})
	if err == nil {
		t.Fatalf("want error on HTTP 500")
	}
	if errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("500 should not be a login failure: %v", err)
	}
}

// TestScrubSecrets proves both the apikey and the persisted passkey are redacted out of any
// surfaced message so neither can leak through a server echo.
func TestScrubSecrets(t *testing.T) {
	t.Parallel()
	d := searchDriver(t, &scriptDoer{})
	got := d.scrubSecrets("key " + credAPIKey + " pass " + credPasskey)
	if strings.Contains(got, credAPIKey) || strings.Contains(got, credPasskey) {
		t.Fatalf("scrubSecrets left a secret: %q", got)
	}
}
