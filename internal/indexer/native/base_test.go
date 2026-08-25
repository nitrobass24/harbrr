package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// fakeDoer returns a canned response or error and records the request it saw (and
// how many it saw, so a capture can be proved to cost no extra round-trip).
type fakeDoer struct {
	resp  *stdhttp.Response
	err   error
	got   *stdhttp.Request
	calls int
}

func (f *fakeDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	f.got = req
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func respWith(status int, body string, header stdhttp.Header) *stdhttp.Response {
	if header == nil {
		header = stdhttp.Header{}
	}
	return &stdhttp.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testDef() *loader.Definition {
	return &loader.Definition{
		ID:    "testfam",
		Name:  "Test Family",
		Links: []string{"https://tracker.example/"},
		Caps: loader.Caps{
			CategoryMappings: []loader.CategoryMapping{
				{ID: loader.Scalar{Value: "1", Set: true}, Cat: "Movies"},
			},
			Modes: loader.Modes{Search: []string{"q"}},
		},
	}
}

func newTestBase(t *testing.T, doer search.Doer) Base {
	t.Helper()
	b, err := NewBase("testfam", Params{Def: testDef(), Doer: doer})
	if err != nil {
		t.Fatalf("NewBase: %v", err)
	}
	return b
}

func mustRequest(t *testing.T, rawurl string) *stdhttp.Request {
	t.Helper()
	req, err := stdhttp.NewRequestWithContext(context.Background(), stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	return req
}

func TestNewBaseScaffold(t *testing.T) {
	t.Run("nil definition", func(t *testing.T) {
		_, err := NewBase("testfam", Params{})
		if err == nil || !strings.Contains(err.Error(), "testfam: nil definition") {
			t.Fatalf("want family-prefixed nil-def error, got %v", err)
		}
	})

	t.Run("baseURL from def link, normalised", func(t *testing.T) {
		b := newTestBase(t, &fakeDoer{})
		if b.BaseURL != "https://tracker.example/" {
			t.Fatalf("BaseURL = %q", b.BaseURL)
		}
	})

	t.Run("explicit baseURL wins and gains slash", func(t *testing.T) {
		b, err := NewBase("testfam", Params{Def: testDef(), BaseURL: "https://mirror.example"})
		if err != nil {
			t.Fatalf("NewBase: %v", err)
		}
		if b.BaseURL != "https://mirror.example/" {
			t.Fatalf("BaseURL = %q", b.BaseURL)
		}
	})

	t.Run("no base URL anywhere fails fast", func(t *testing.T) {
		def := testDef()
		def.Links = nil
		_, err := NewBase("testfam", Params{Def: def})
		if err == nil || !strings.Contains(err.Error(), "testfam: no base URL") {
			t.Fatalf("want fail-fast no-base-URL error, got %v", err)
		}
	})

	t.Run("clock defaults, caps built, cap default", func(t *testing.T) {
		b := newTestBase(t, &fakeDoer{})
		if b.Clock == nil || b.Caps == nil || b.Capabilities() != b.Caps {
			t.Fatalf("scaffold not wired: clock set=%t caps=%v", b.Clock != nil, b.Caps)
		}
		if b.MaxBodyBytes != defaultMaxBodyBytes {
			t.Fatalf("MaxBodyBytes = %d", b.MaxBodyBytes)
		}
		if b.SupportsOffsetPaging() {
			t.Fatal("SupportsOffsetPaging default must be false")
		}
	})
}

// TestBaseNewRequest proves NewRequest is the drivers' request primitive: a valid URL
// yields a request carrying ctx/method/body, and a build failure surfaces only the
// endpoint's scheme://host — never the passkey-bearing path/query the *url.Error quotes.
func TestBaseNewRequest(t *testing.T) {
	const secret = "SYNTHETIC-PASS"
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	b := newTestBase(t, &fakeDoer{})

	tests := []struct {
		name    string
		rawurl  string
		wantErr bool
	}{
		{name: "valid URL", rawurl: "https://tracker.example/torrents.php?torrent_pass=" + secret},
		{name: "control character", rawurl: "https://tracker.example/torrents.php\x7f?torrent_pass=" + secret, wantErr: true},
		{name: "invalid percent escape", rawurl: "https://tracker.example/torrents.php%zz?torrent_pass=" + secret, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := b.NewRequest(ctx, stdhttp.MethodPost, tt.rawurl, strings.NewReader("body"))
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("NewRequest: %v", err)
				}
				if req.Context().Value(ctxKey{}) != "marker" || req.Method != stdhttp.MethodPost || req.Body == nil {
					t.Fatalf("request not wired: ctx=%v method=%s body=%v", req.Context(), req.Method, req.Body)
				}
				return
			}
			if err == nil {
				t.Fatal("want a build error")
			}
			if !apphttp.IsHostRedacted(err) {
				t.Fatalf("build error not marked host-redacted: %v", err)
			}
			got := err.Error()
			if strings.Contains(got, secret) || strings.Contains(got, "torrents.php") {
				t.Fatalf("build error leaked the path/query: %v", err)
			}
			// An unparseable URL has no extractable host, so SchemeHost yields its
			// REDACTED placeholder; the family/op prefix is what must survive.
			if !strings.HasPrefix(got, "testfam: build request to ") {
				t.Fatalf("build error lost its family prefix: %v", err)
			}
		})
	}
}

// TestDoTransportErrorRedaction is the structural secret-hygiene guarantee: a
// transport error carrying a passkey-bearing URL surfaces only scheme://host.
func TestDoTransportErrorRedaction(t *testing.T) {
	const secret = "PASSKEY-hex-0123456789abcdef"
	transportErr := &url.Error{
		Op:  "Get",
		URL: "https://tracker.example/download.php?id=1&passkey=" + secret,
		Err: errors.New("connection refused"),
	}
	b := newTestBase(t, &fakeDoer{err: transportErr})

	req := mustRequest(t, "https://tracker.example/download.php?id=1&passkey="+secret)
	for _, call := range []func() (*Response, error){
		func() (*Response, error) { return b.Do(context.Background(), req, ClassifyAuth403) },
		func() (*Response, error) { return b.DoDownload(context.Background(), req, ClassifyAuth403) },
	} {
		resp, err := call()
		if resp != nil || err == nil {
			t.Fatalf("want nil response + error, got %v / %v", resp, err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("transport error leaked the secret: %v", err)
		}
		if !strings.Contains(err.Error(), "https://tracker.example") {
			t.Fatalf("transport error lost the host context: %v", err)
		}
		if !strings.Contains(err.Error(), "testfam") {
			t.Fatalf("transport error lost the family prefix: %v", err)
		}
	}
}

// TestDoContextSentinelsSurvive: a cancelled request stays classifiable so it is
// not misreported as a tracker failure.
func TestDoContextSentinelsSurvive(t *testing.T) {
	transportErr := &url.Error{Op: "Get", URL: "https://tracker.example/x", Err: context.Canceled}
	b := newTestBase(t, &fakeDoer{err: transportErr})
	_, err := b.Do(context.Background(), mustRequest(t, "https://tracker.example/x"), ClassifyAuth403)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("context.Canceled not detectable through the wrap: %v", err)
	}
}

func TestClassifyDialects(t *testing.T) {
	tests := []struct {
		name       string
		classify   Classify
		status     int
		wantAuth   bool
		wantRate   bool
		wantHTTPIn string // substring for the plain non-2xx error, "" if n/a
	}{
		{name: "majority 401 auth", classify: ClassifyAuth403, status: 401, wantAuth: true},
		{name: "majority 403 auth", classify: ClassifyAuth403, status: 403, wantAuth: true},
		{name: "majority 429 rate", classify: ClassifyAuth403, status: 429, wantRate: true},
		{name: "majority 503 rate", classify: ClassifyAuth403, status: 503, wantRate: true},
		{name: "majority 500 plain", classify: ClassifyAuth403, status: 500, wantHTTPIn: "HTTP 500"},
		{name: "hdbits 401 auth", classify: ClassifyRateLimit403, status: 401, wantAuth: true},
		{name: "hdbits 403 rate", classify: ClassifyRateLimit403, status: 403, wantRate: true},
		{name: "mam 403 auth", classify: ClassifyAuthOnly403, status: 403, wantAuth: true},
		{name: "mam 401 plain", classify: ClassifyAuthOnly403, status: 401, wantHTTPIn: "HTTP 401"},
		{name: "avistaz AlsoAuth 412", classify: ClassifyAuth403.AlsoAuth(412), status: 412, wantAuth: true},
		{name: "AlsoRateLimited 509", classify: ClassifyAuth403.AlsoRateLimited(509), status: 509, wantRate: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newTestBase(t, &fakeDoer{resp: respWith(tt.status, "", nil)})
			resp, err := b.Do(context.Background(), mustRequest(t, "https://tracker.example/api"), tt.classify)
			if err == nil {
				t.Fatal("want an error for a non-2xx status")
			}
			// The classified-status contract: headers stay available, body is nil.
			if resp == nil || resp.StatusCode != tt.status || resp.Body != nil {
				t.Fatalf("classified-status Response contract violated: %+v", resp)
			}
			var rl *search.RateLimitedError
			switch {
			case tt.wantAuth:
				if !errors.Is(err, login.ErrLoginFailed) {
					t.Fatalf("want ErrLoginFailed, got %v", err)
				}
			case tt.wantRate:
				if !errors.As(err, &rl) || rl.StatusCode != tt.status {
					t.Fatalf("want RateLimitedError(%d), got %v", tt.status, err)
				}
			default:
				if errors.Is(err, login.ErrLoginFailed) || errors.As(err, &rl) {
					t.Fatalf("plain HTTP error misclassified: %v", err)
				}
				if !strings.Contains(err.Error(), tt.wantHTTPIn) {
					t.Fatalf("want %q in error, got %v", tt.wantHTTPIn, err)
				}
			}
		})
	}
}

func TestClassifyRetryAfterHonored(t *testing.T) {
	h := stdhttp.Header{}
	h.Set("Retry-After", "120")
	b := newTestBase(t, &fakeDoer{resp: respWith(429, "", h)})
	_, err := b.Do(context.Background(), mustRequest(t, "https://tracker.example/api"), ClassifyAuth403)
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) || rl.RetryAfter != 2*time.Minute {
		t.Fatalf("Retry-After not honored: %v", err)
	}
}

func TestClassifyAuthReason(t *testing.T) {
	b := newTestBase(t, &fakeDoer{resp: respWith(403, "", nil)})
	_, err := b.Do(context.Background(), mustRequest(t, "https://tracker.example/api"),
		ClassifyAuthOnly403.WithAuthReason("mam_id expired or invalid"))
	if err == nil || !strings.Contains(err.Error(), "mam_id expired or invalid") {
		t.Fatalf("auth reason lost: %v", err)
	}
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("auth reason broke the sentinel: %v", err)
	}
}

func TestDoBodySilentTruncateAtCap(t *testing.T) {
	b := newTestBase(t, &fakeDoer{resp: respWith(200, strings.Repeat("x", 64), nil)})
	b.MaxBodyBytes = 16
	resp, err := b.Do(context.Background(), mustRequest(t, "https://tracker.example/api"), ClassifyAuth403)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(resp.Body) != 16 {
		t.Fatalf("want silent truncation at the cap, got %d bytes", len(resp.Body))
	}
}

func TestDoDownloadCapErrors(t *testing.T) {
	t.Run("within cap", func(t *testing.T) {
		payload := "d8:announce3:abce" // bencoded-ish bytes
		h := stdhttp.Header{}
		h.Set("Content-Type", "application/x-bittorrent")
		b := newTestBase(t, &fakeDoer{resp: respWith(200, payload, h)})
		resp, err := b.DoDownload(context.Background(), mustRequest(t, "https://tracker.example/dl"), ClassifyAuth403)
		if err != nil {
			t.Fatalf("DoDownload: %v", err)
		}
		if !bytes.Equal(resp.Body, []byte(payload)) || resp.Header.Get("Content-Type") != "application/x-bittorrent" {
			t.Fatalf("body/header lost: %+v", resp)
		}
	})

	t.Run("over cap errors, never truncates", func(t *testing.T) {
		big := io.NopCloser(io.LimitReader(neverEnding('x'), maxTorrentBytes+1))
		b := newTestBase(t, &fakeDoer{resp: &stdhttp.Response{StatusCode: 200, Header: stdhttp.Header{}, Body: big}})
		_, err := b.DoDownload(context.Background(), mustRequest(t, "https://tracker.example/dl"), ClassifyAuth403)
		if !errors.Is(err, ErrDownloadTooLarge) {
			t.Fatalf("want ErrDownloadTooLarge, got %v", err)
		}
		if !strings.Contains(err.Error(), "testfam") {
			t.Fatalf("cap error lost the family prefix: %v", err)
		}
	})
}

// refusalTestBase wires a Base whose configured apikey is a secret value the capture
// must scrub, over a doer serving one canned response.
func refusalTestBase(t *testing.T, doer *fakeDoer, apikey string) Base {
	t.Helper()
	b, err := NewBase("testfam", Params{Def: scrubTestDef(), Cfg: map[string]string{"apikey": apikey}, Doer: doer})
	if err != nil {
		t.Fatalf("NewBase: %v", err)
	}
	return b
}

// TestDownloadRefusalCapture: a refused grab carries the redacted exchange
// (autobrr/harbrr#465). Without it, an unclassified refusal — MAM's 406 — discards
// the one thing that says why, because the body dies with the closed response.
func TestDownloadRefusalCapture(t *testing.T) {
	t.Parallel()
	const (
		// pathSecret is passkey-shaped (a long hex run) because that is the shape
		// path redaction is built for; queryKey is caught by its parameter NAME.
		pathSecret  = "0123456789abcdef0123456789abcdef0123456789abcdef"
		queryKey    = "QUERY-APIKEY-2b7f"
		cookieValue = "SESSIONCOOKIE-9d41"
	)
	header := stdhttp.Header{}
	header.Set("Content-Type", "text/html")
	header.Set("Set-Cookie", "session="+cookieValue)
	body := "<h1>Not Acceptable</h1><p>rejected for apikey=" + queryKey + "</p>"

	doer := &fakeDoer{resp: respWith(stdhttp.StatusNotAcceptable, body, header)}
	b := refusalTestBase(t, doer, queryKey)
	req := mustRequest(t, "https://tracker.example/download.php/"+pathSecret+"?tid=1&apikey="+queryKey)
	req.Header.Set("Cookie", "session="+cookieValue)

	resp, err := b.DoDownload(context.Background(), req, ClassifyAuth403)
	if err == nil {
		t.Fatal("want the 406 status error")
	}
	if resp == nil || resp.StatusCode != stdhttp.StatusNotAcceptable {
		t.Fatalf("response shell lost: %+v", resp)
	}
	// The capture wraps, never replaces: an unclassified 406 stays unclassified and a
	// classified status stays classifiable (asserted below for 403).
	if !strings.Contains(err.Error(), "testfam: download returned HTTP 406") {
		t.Fatalf("status error reworded: %v", err)
	}
	capture, ok := search.CaptureOf(err)
	if !ok {
		t.Fatal("refused download carries no capture")
	}
	if capture.Status != stdhttp.StatusNotAcceptable || capture.Method != stdhttp.MethodGet {
		t.Errorf("capture = %s %d, want GET 406", capture.Method, capture.Status)
	}
	if !strings.Contains(capture.Body, "Not Acceptable") {
		t.Errorf("capture body lost the diagnostic: %q", capture.Body)
	}
	if capture.Headers["Content-Type"] != "text/html" {
		t.Errorf("capture headers = %v, want the Content-Type", capture.Headers)
	}
	// Rendered as the diagnostics API serves it: no synthetic secret from the path,
	// the query, the cookie, or the body survives anywhere in the entry.
	rendered, merr := json.Marshal(capture)
	if merr != nil {
		t.Fatalf("marshal capture: %v", merr)
	}
	for _, secret := range []string{pathSecret, queryKey, cookieValue} {
		if strings.Contains(string(rendered), secret) {
			t.Errorf("capture leaked %q: %s", secret, rendered)
		}
	}
	// The capture is built from bytes the grab already fetched — never a second fetch.
	if doer.calls != 1 {
		t.Errorf("requests = %d, want exactly 1 (the capture costs no round-trip)", doer.calls)
	}
}

// TestCaptureScope pins where the capture applies: the download path on any non-2xx
// (classified or not), and nowhere else — a successful grab and the API request path
// are untouched.
func TestCaptureScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		status      int
		download    bool
		wantCapture bool
	}{
		{name: "classified download refusal captures too", status: stdhttp.StatusForbidden, download: true, wantCapture: true},
		{name: "successful download captures nothing", status: stdhttp.StatusOK, download: true},
		{name: "api request refusal captures nothing", status: stdhttp.StatusNotAcceptable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := newTestBase(t, &fakeDoer{resp: respWith(tt.status, "<html>refused</html>", nil)})
			req := mustRequest(t, "https://tracker.example/dl")
			var err error
			if tt.download {
				_, err = b.DoDownload(context.Background(), req, ClassifyAuth403)
			} else {
				_, err = b.Do(context.Background(), req, ClassifyAuth403)
			}
			if _, ok := search.CaptureOf(err); ok != tt.wantCapture {
				t.Fatalf("capture present = %t, want %t (err = %v)", ok, tt.wantCapture, err)
			}
			// A captured 403 is still an auth failure: wrapping preserves the chain.
			if tt.status == stdhttp.StatusForbidden && !errors.Is(err, login.ErrLoginFailed) {
				t.Errorf("captured 403 lost login.ErrLoginFailed: %v", err)
			}
		})
	}
}

// neverEnding is an infinite reader of one byte, so the over-cap test needs no
// quarter-gigabyte allocation.
type neverEnding byte

func (b neverEnding) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(b)
	}
	return len(p), nil
}

func TestDoAppliesContext(t *testing.T) {
	f := &fakeDoer{resp: respWith(200, "ok", nil)}
	b := newTestBase(t, f)
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "v")
	if _, err := b.Do(ctx, mustRequest(t, "https://tracker.example/api"), ClassifyAuth403); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if f.got == nil || f.got.Context().Value(key{}) != "v" {
		t.Fatal("Do must attach the caller's context to the request")
	}
}

func TestTestViaSearch(t *testing.T) {
	wantErr := fmt.Errorf("probe failed: %w", login.ErrLoginFailed)
	s := searcherFunc(func(_ context.Context, q search.Query) ([]*normalizer.Release, error) {
		if q.Keywords != "" {
			t.Fatalf("TestViaSearch must probe with an empty query, got %+v", q)
		}
		return nil, wantErr
	})
	if err := TestViaSearch(context.Background(), s); !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("TestViaSearch must surface the search error, got %v", err)
	}
}

// searcherFunc adapts a func to the Searcher probe surface for the test.
type searcherFunc func(ctx context.Context, q search.Query) ([]*normalizer.Release, error)

func (f searcherFunc) Capabilities() *mapper.Capabilities { return nil }
func (f searcherFunc) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	return f(ctx, q)
}
func (f searcherFunc) NeedsResolver() bool        { return false }
func (f searcherFunc) DownloadNeedsAuth() bool    { return false }
func (f searcherFunc) SupportsOffsetPaging() bool { return false }
func (f searcherFunc) ConsumesSearchMode() bool   { return false }
func (f searcherFunc) Grab(_ context.Context, _ string) (*search.GrabResult, error) {
	return nil, errors.New("searcherFunc: Grab is not part of this test surface")
}
