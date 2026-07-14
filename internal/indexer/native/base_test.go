package native

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// fakeDoer returns a canned response or error and records the request it saw.
type fakeDoer struct {
	resp *stdhttp.Response
	err  error
	got  *stdhttp.Request
}

func (f *fakeDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	f.got = req
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
func (f searcherFunc) Grab(_ context.Context, _ string) (*search.GrabResult, error) {
	return nil, errors.New("searcherFunc: Grab is not part of this test surface")
}
