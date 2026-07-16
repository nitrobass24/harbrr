package native

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"

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

// TestNormalizeReadError proves the mid-body read failure keeps its ErrParseError health
// classification (and its ErrBodyRead cause) while any other error (transport/status)
// passes through unchanged.
func TestNormalizeReadError(t *testing.T) {
	if NormalizeReadError(nil) != nil {
		t.Fatal("nil in, nil out")
	}
	readErr := fmt.Errorf("testfam: %w: %w", ErrBodyRead, errors.New("unexpected EOF"))
	got := NormalizeReadError(readErr)
	if !errors.Is(got, search.ErrParseError) {
		t.Fatalf("err = %v, want errors.Is(search.ErrParseError)", got)
	}
	if !errors.Is(got, ErrBodyRead) {
		t.Fatalf("err = %v, want errors.Is(ErrBodyRead)", got)
	}
	other := errors.New("testfam: request returned HTTP 500")
	if !errors.Is(NormalizeReadError(other), other) {
		t.Fatalf("non-read error must pass through unchanged, got %v", NormalizeReadError(other))
	}
}

// TestNormalizeReadErrorSurvivesRewording proves the errors.Is classification is
// text-independent: reworded human-readable wrapping around ErrBodyRead (as if
// roundTrip's message in base.go changed) still classifies as ErrParseError.
func TestNormalizeReadErrorSurvivesRewording(t *testing.T) {
	readErr := fmt.Errorf("testfam: totally different wording here: %w: %w", ErrBodyRead, errors.New("EOF"))
	got := NormalizeReadError(readErr)
	if !errors.Is(got, search.ErrParseError) {
		t.Fatalf("err = %v, want errors.Is(search.ErrParseError) even after rewording", got)
	}
}
