package nzbindex

import (
	"errors"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// TestGrab proves a .nzb download returns the body as an application/x-nzb GrabResult with no
// Redirect (an .nzb is always fetched, never redirected).
func TestGrab(t *testing.T) {
	t.Parallel()
	const nzb = `<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"></nzb>`
	doer := &scriptDoer{handler: func(*stdhttp.Request) *stdhttp.Response {
		return &stdhttp.Response{
			StatusCode: stdhttp.StatusOK,
			Header:     stdhttp.Header{"Content-Type": {"application/x-nzb"}},
			Body:       io.NopCloser(strings.NewReader(nzb)),
		}
	}}
	d := testDriver(t, nil, doer)
	res, err := d.Grab(t.Context(), testBaseURL+"/api/download/abc.nzb")
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if res.ContentType != nzbContentType {
		t.Errorf("ContentType = %q, want %q", res.ContentType, nzbContentType)
	}
	if string(res.Body) != nzb {
		t.Errorf("Body = %q, want the nzb bytes", res.Body)
	}
	if res.Redirect != "" {
		t.Errorf("Redirect = %q, want empty (nzb is always a body)", res.Redirect)
	}
}

// TestGrabNon200 proves a non-2xx download surfaces an error rather than serving a body.
func TestGrabNon200(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(*stdhttp.Request) *stdhttp.Response { return statusResponse(stdhttp.StatusNotFound) }}
	d := testDriver(t, nil, doer)
	if _, err := d.Grab(t.Context(), testBaseURL+"/api/download/missing.nzb"); err == nil {
		t.Fatal("want an error for a 404 download")
	}
}

// TestGrabUnauthorized proves a 401 download surfaces as a login failure (auth_failure health).
func TestGrabUnauthorized(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(*stdhttp.Request) *stdhttp.Response { return statusResponse(stdhttp.StatusUnauthorized) }}
	d := testDriver(t, nil, doer)
	if _, err := d.Grab(t.Context(), testBaseURL+"/api/download/x.nzb"); !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
}

// TestGrabRateLimited proves a 429 download surfaces a *RateLimitedError carrying the parsed
// Retry-After (the registry backs off rather than misreporting working creds).
func TestGrabRateLimited(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(*stdhttp.Request) *stdhttp.Response {
		r := statusResponse(stdhttp.StatusTooManyRequests)
		r.Header.Set("Retry-After", "120")
		return r
	}}
	d := testDriver(t, nil, doer)
	_, err := d.Grab(t.Context(), testBaseURL+"/api/download/x.nzb")
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want *search.RateLimitedError", err)
	}
	if rl.StatusCode != stdhttp.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want 429", rl.StatusCode)
	}
	if rl.RetryAfter != 120*time.Second {
		t.Errorf("RetryAfter = %v, want 120s (parsed from the Retry-After header)", rl.RetryAfter)
	}
}

// TestGrabTransportError proves a transport failure surfaces the errDownloadRequestFailed
// sentinel with only scheme://host — the request path is dropped (RedactURLError), never leaked.
func TestGrabTransportError(t *testing.T) {
	t.Parallel()
	link := testBaseURL + "/api/download/SECRETPATH.nzb"
	uerr := &url.Error{Op: "Get", URL: link, Err: errors.New("dial tcp: connection refused")}
	d := testDriver(t, nil, &errorDoer{err: uerr})
	_, err := d.Grab(t.Context(), link)
	if !errors.Is(err, errDownloadRequestFailed) {
		t.Fatalf("err = %v, want errDownloadRequestFailed", err)
	}
	got := err.Error()
	if !strings.Contains(got, testBaseURL) {
		t.Errorf("err = %q, want it to surface scheme://host", got)
	}
	if strings.Contains(got, "SECRETPATH") {
		t.Errorf("err = %q leaks the request path", got)
	}
}

// TestGrabPreservesOversizedSentinel proves the shared Base download cap remains
// classifiable by callers rather than being flattened to a generic request failure.
func TestGrabPreservesOversizedSentinel(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", (64<<20)+1)
	doer := &scriptDoer{handler: func(*stdhttp.Request) *stdhttp.Response {
		return &stdhttp.Response{
			StatusCode: stdhttp.StatusOK,
			Header:     stdhttp.Header{"Content-Type": {"application/x-nzb"}},
			Body:       io.NopCloser(strings.NewReader(big)),
		}
	}}
	d := testDriver(t, nil, doer)
	_, err := d.Grab(t.Context(), testBaseURL+"/api/download/big.nzb")
	if !errors.Is(err, native.ErrDownloadTooLarge) {
		t.Fatalf("err = %v, want native.ErrDownloadTooLarge", err)
	}
}
