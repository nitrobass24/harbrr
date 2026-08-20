package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestGrabDirectReturnsResult proves the shared direct-GET grab path (beyondhd,
// broadcastthenet, hdbits) issues a plain GET and returns the body/Content-Type as a
// GrabResult with no Redirect.
func TestGrabDirectReturnsResult(t *testing.T) {
	h := stdhttp.Header{}
	h.Set("Content-Type", "application/x-bittorrent")
	b := newTestBase(t, &fakeDoer{resp: respWith(200, "torrent-bytes", h)})
	got, err := b.GrabDirect(context.Background(), "https://tracker.example/dl?passkey=secret", ClassifyAuth403)
	if err != nil {
		t.Fatalf("GrabDirect: %v", err)
	}
	if string(got.Body) != "torrent-bytes" || got.ContentType != "application/x-bittorrent" || got.Redirect != "" {
		t.Fatalf("GrabDirect result = %+v", got)
	}
}

// TestGrabDirectBuildErrorIsGeneric proves a request-build failure returns the generic
// ErrGrabRequestFailed sentinel bare, never the (possibly credential-bearing) link that
// failed to build.
func TestGrabDirectBuildErrorIsGeneric(t *testing.T) {
	b := newTestBase(t, &fakeDoer{})
	_, err := b.GrabDirect(context.Background(), "http://tracker.example/\x7f?passkey=secret", ClassifyAuth403)
	if !errors.Is(err, ErrGrabRequestFailed) {
		t.Fatalf("err = %v, want ErrGrabRequestFailed", err)
	}
}

// TestGrabDirectPassesThroughClassifiedError proves a classified-status error (login
// failure, rate limit) surfaces unchanged, not flattened by GrabDirect.
func TestGrabDirectPassesThroughClassifiedError(t *testing.T) {
	b := newTestBase(t, &fakeDoer{resp: respWith(401, "", nil)})
	_, err := b.GrabDirect(context.Background(), "https://tracker.example/dl", ClassifyAuth403)
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
}

// TestGrabNZBReturnsResult proves the shared usenet grab path (newznab, nzbindex) issues
// a plain GET and returns the body under the caller-supplied Content-Type.
func TestGrabNZBReturnsResult(t *testing.T) {
	sentinel := errors.New("fam: download request failed")
	b := newTestBase(t, &fakeDoer{resp: respWith(200, "<nzb/>", nil)})
	got, err := b.GrabNZB(context.Background(), "https://tracker.example/getnzb?r=apikey", "application/x-nzb", ClassifyRateLimit403, sentinel)
	if err != nil {
		t.Fatalf("GrabNZB: %v", err)
	}
	if string(got.Body) != "<nzb/>" || got.ContentType != "application/x-nzb" {
		t.Fatalf("GrabNZB result = %+v", got)
	}
}

// TestGrabNZBBuildErrorIsCallerSentinel proves a request-build failure returns the
// caller's OWN family-prefixed sentinel bare (never the credential-bearing link).
func TestGrabNZBBuildErrorIsCallerSentinel(t *testing.T) {
	sentinel := errors.New("fam: download request failed")
	b := newTestBase(t, &fakeDoer{})
	_, err := b.GrabNZB(context.Background(), "http://tracker.example/\x7f?r=apikey", "application/x-nzb", ClassifyRateLimit403, sentinel)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the caller's sentinel", err)
	}
}

// TestGrabNZBPassesThroughClassifiedError proves a classified-status error (login
// failure, rate limit) is NOT sanitized away — it must stay classifiable for health.
func TestGrabNZBPassesThroughClassifiedError(t *testing.T) {
	sentinel := errors.New("fam: download request failed")
	b := newTestBase(t, &fakeDoer{resp: respWith(401, "", nil)})
	_, err := b.GrabNZB(context.Background(), "https://tracker.example/getnzb", "application/x-nzb", ClassifyRateLimit403, sentinel)
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
}

// TestGrabNZBTransportErrorSanitized proves a real transport failure — a *url.Error whose
// Error() embeds the full apikey-bearing URL — is host-only redacted, still errors.Is the
// caller's sentinel, and never leaks the apikey.
func TestGrabNZBTransportErrorSanitized(t *testing.T) {
	sentinel := errors.New("fam: download request failed")
	const secret = "APIKEY0123456789"
	leakURL := "https://tracker.example/getnzb/" + secret + "?r=" + secret
	uerr := &url.Error{Op: "Get", URL: leakURL, Err: errors.New("dial tcp: connection refused")}
	b := newTestBase(t, &fakeDoer{err: uerr})
	_, err := b.GrabNZB(context.Background(), leakURL, "application/x-nzb", ClassifyRateLimit403, sentinel)
	if err == nil {
		t.Fatal("GrabNZB: err = nil, want a transport error")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want errors.Is(sentinel)", err)
	}
	got := err.Error()
	if !strings.Contains(got, "https://tracker.example") {
		t.Errorf("err = %q, want it to surface scheme://host", got)
	}
	if strings.Contains(got, secret) {
		t.Errorf("err = %q leaks the apikey", got)
	}
}

// TestGrabNZBUnexpectedErrorFlattened proves a transport error that is NOT a *url.Error —
// free text that may embed a secret-bearing URL no scrubber can safely rewrite — is
// flattened to the bare caller sentinel instead of being surfaced.
func TestGrabNZBUnexpectedErrorFlattened(t *testing.T) {
	sentinel := errors.New("fam: download request failed")
	const secret = "APIKEY0123456789"
	b := newTestBase(t, &fakeDoer{err: errors.New("proxy said: https://tracker.example/getnzb?r=" + secret)})
	_, err := b.GrabNZB(context.Background(), "https://tracker.example/getnzb?r="+secret, "application/x-nzb", ClassifyRateLimit403, sentinel)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want errors.Is(sentinel)", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("err = %q leaks the apikey", err)
	}
}

// TestGrabNZBPreservesOversizedSentinel proves the shared Base download cap remains
// classifiable through GrabNZB rather than being flattened to the generic sentinel.
func TestGrabNZBPreservesOversizedSentinel(t *testing.T) {
	sentinel := errors.New("fam: download request failed")
	big := &stdhttp.Response{
		StatusCode: 200,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(io.LimitReader(neverEnding('x'), maxTorrentBytes+1)),
	}
	b := newTestBase(t, &fakeDoer{resp: big})
	_, err := b.GrabNZB(context.Background(), "https://tracker.example/getnzb", "application/x-nzb", ClassifyRateLimit403, sentinel)
	if !errors.Is(err, ErrDownloadTooLarge) {
		t.Fatalf("err = %v, want ErrDownloadTooLarge", err)
	}
}

// TestSanitizeGrabErrorCollapsesUnmarkedWrappers proves the classification always
// survives while the WRAPPER's free text — which may embed a secret-bearing URL — is
// dropped unless the error is provably host-redacted.
func TestSanitizeGrabErrorCollapsesUnmarkedWrappers(t *testing.T) {
	t.Parallel()

	const secret = "PASSKEY0123456789"
	sentinel := errors.New("fam: download request failed")
	rl := &search.RateLimitedError{StatusCode: 429, RetryAfter: 90 * time.Second}

	tests := []struct {
		name       string
		err        error
		wantIs     error
		wantHas    []string
		wantNoLeak []string
	}{
		{
			name:       "unmarked login-failure wrapper collapses to the bare sentinel",
			err:        fmt.Errorf("leak https://t.test/dl/%s: %w", secret, login.ErrLoginFailed),
			wantIs:     login.ErrLoginFailed,
			wantNoLeak: []string{secret, "t.test"},
		},
		{
			name:       "unmarked context cancellation collapses to context.Canceled",
			err:        fmt.Errorf("leak https://t.test/dl/%s: %w", secret, context.Canceled),
			wantIs:     context.Canceled,
			wantNoLeak: []string{secret, "t.test"},
		},
		{
			name:    "marked context cancellation keeps its detail",
			err:     apphttp.MarkHostRedacted(fmt.Errorf("fam: download to https://t.test failed: %w", context.Canceled)),
			wantIs:  context.Canceled,
			wantHas: []string{"https://t.test", "download"},
		},
		{
			name:       "unmarked rate-limit wrapper keeps the typed classification only",
			err:        fmt.Errorf("leak https://t.test/dl/%s: %w", secret, rl),
			wantIs:     search.ErrRateLimited,
			wantHas:    []string{"retry after 1m30s"},
			wantNoLeak: []string{secret, "t.test"},
		},
		{
			name:    "marked transport error is wrapped as the caller sentinel",
			err:     apphttp.MarkHostRedacted(errors.New("fam: download to https://t.test failed: dial tcp")),
			wantIs:  sentinel,
			wantHas: []string{"fam: download request failed", "https://t.test", "dial tcp"},
		},
		{
			name:       "unmarked free text collapses to the bare caller sentinel",
			err:        errors.New("proxy said: https://t.test/dl?r=" + secret),
			wantIs:     sentinel,
			wantNoLeak: []string{secret, "t.test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SanitizeGrabError(tt.err, sentinel)
			if !errors.Is(got, tt.wantIs) {
				t.Fatalf("SanitizeGrabError = %v, want errors.Is(%v)", got, tt.wantIs)
			}
			msg := got.Error()
			for _, want := range tt.wantHas {
				if !strings.Contains(msg, want) {
					t.Errorf("sanitized error %q missing %q", msg, want)
				}
			}
			for _, leak := range tt.wantNoLeak {
				if strings.Contains(msg, leak) {
					t.Errorf("sanitized error %q leaked %q", msg, leak)
				}
			}
		})
	}
}

// TestSanitizeGrabErrorKeepsRetryAfter proves the rate-limit collapse returns the TYPED
// error, so a caller's errors.As still sees RetryAfter for health backoff.
func TestSanitizeGrabErrorKeepsRetryAfter(t *testing.T) {
	t.Parallel()
	rl := &search.RateLimitedError{StatusCode: 429, RetryAfter: 90 * time.Second}
	got := SanitizeGrabError(fmt.Errorf("leak https://t.test/dl/SECRET: %w", rl), errors.New("fam: download request failed"))
	var out *search.RateLimitedError
	if !errors.As(got, &out) {
		t.Fatalf("SanitizeGrabError = %v, want a *search.RateLimitedError", got)
	}
	if out.StatusCode != 429 || out.RetryAfter != 90*time.Second {
		t.Errorf("classification lost: %+v", out)
	}
}

// droppingBody stands in for a connection reset after the 200: the status is already
// read, then the body read fails.
type droppingBody struct{ err error }

func (d droppingBody) Read([]byte) (int, error) { return 0, d.err }
func (d droppingBody) Close() error             { return nil }

// TestGrabNZBMidBodyDropStaysTransportClassifiable proves a grab whose connection dies
// while the download body is being read stays classifiable as a transport failure
// (#479): it used to reach sanitizeGrabError unmarked and unsentinelled, get flattened
// to the bare caller sentinel, and so record NO health event at all. Both the
// ErrBodyRead sentinel (registry isTransportError matches it directly) and the
// net.Error cause must survive — and the surfaced error must still leak no URL.
func TestGrabNZBMidBodyDropStaysTransportClassifiable(t *testing.T) {
	sentinel := errors.New("fam: download request failed")
	const secret = "APIKEY0123456789"
	reset := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	dropped := &stdhttp.Response{StatusCode: 200, Header: stdhttp.Header{}, Body: droppingBody{err: reset}}
	b := newTestBase(t, &fakeDoer{resp: dropped})

	_, err := b.GrabNZB(context.Background(), "https://tracker.example/getnzb?r="+secret, "application/x-nzb", ClassifyRateLimit403, sentinel)
	if !errors.Is(err, ErrBodyRead) {
		t.Fatalf("err = %v, want errors.Is(ErrBodyRead) so the registry classifies it as transport", err)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want errors.Is(sentinel)", err)
	}
	if _, ok := errors.AsType[net.Error](err); !ok {
		t.Errorf("err = %v, want the net.Error cause preserved", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("err = %q leaks the apikey", err)
	}
}

// TestGrabNZBContextSentinelsSurvive proves a cancellation/deadline from the fetch is
// preserved through GrabNZB rather than flattened into the generic download failure.
func TestGrabNZBContextSentinelsSurvive(t *testing.T) {
	sentinel := errors.New("fam: download request failed")
	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		b := newTestBase(t, &fakeDoer{err: want})
		_, err := b.GrabNZB(context.Background(), "https://tracker.example/getnzb", "application/x-nzb", ClassifyRateLimit403, sentinel)
		if !errors.Is(err, want) {
			t.Errorf("err = %v, want errors.Is(%v)", err, want)
		}
	}
}
