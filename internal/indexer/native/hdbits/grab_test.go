package hdbits

import (
	"context"
	"errors"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// grabURL is a synthetic HDBits download URL: it embeds a fake passkey to prove neither
// the passkey nor the URL itself ever surfaces in a grab error. The synthetic secret
// reuses credPass (defined in parse_test.go) so the redaction assertions cover the
// configured passkey.
const grabURL = "https://hdbits.test/download.php?id=100001&passkey=" + credPass

// errorDoer fails every request with a caller-supplied transport error, so the test can
// feed it the realistic *url.Error http.Client.Do returns and prove the grab error surfaces
// only the host, never the passkey-bearing link.
type errorDoer struct{ err error }

func (e *errorDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

// leakURL is a synthetic transport-failure URL that hides credPass in BOTH a path segment
// and a query param, mirroring where trackers stash download secrets. hdbits.org is the
// real HDBits host; its scheme://host is not a secret and is allowed to surface.
const leakURL = "https://hdbits.org/dl/" + credPass + "?passkey=" + credPass

// transportErr is the *url.Error shape http.Client.Do returns on a dial failure: it echoes
// the full secret-bearing URL, so the grab path must redact it to host-only before wrapping.
func transportErr() error {
	return &url.Error{Op: "Get", URL: leakURL, Err: errors.New("dial tcp: connection refused")}
}

// TestGrabReturnsTorrentBytes proves Grab GETs the download URL server-side and returns
// the torrent body and Content-Type, with no extra auth header (the URL carries its own
// passkey) and no Redirect (HDBits serves a direct .torrent).
func TestGrabReturnsTorrentBytes(t *testing.T) {
	t.Parallel()
	const payload = "d8:announce..fake torrent.."
	doer := &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		resp := mkResp(stdhttp.StatusOK, payload)
		resp.Header.Set("Content-Type", "application/x-bittorrent")
		return resp
	}}
	d := liveDriver(t, doer)

	res, err := d.Grab(context.Background(), grabURL)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(res.Body) != payload {
		t.Errorf("body = %q, want the torrent payload", res.Body)
	}
	if res.ContentType != "application/x-bittorrent" {
		t.Errorf("ContentType = %q, want application/x-bittorrent", res.ContentType)
	}
	if res.Redirect != "" {
		t.Errorf("Redirect = %q, want empty (HDBits serves a direct torrent)", res.Redirect)
	}
	if len(doer.reqs) != 1 || doer.reqs[0].method != stdhttp.MethodGet {
		t.Fatalf("requests = %v, want one GET", doer.reqs)
	}
	if doer.reqs[0].url != grabURL {
		t.Errorf("url = %q, want the download URL", doer.reqs[0].url)
	}
}

// TestGrabTransportErrorSurfacesHostOnly proves a transport error from the download fetch
// surfaces the endpoint's scheme://host (not a secret, useful for diagnosis) while dropping
// the passkey wherever it hides — in the path segment and the query param. The redaction
// itself is structural (base DoDownload); this proves the driver stays on that path.
func TestGrabTransportErrorSurfacesHostOnly(t *testing.T) {
	t.Parallel()
	d := liveDriver(t, &scriptDoer{})
	d.Doer = &errorDoer{err: transportErr()}

	_, err := d.Grab(context.Background(), grabURL)
	if err == nil {
		t.Fatal("Grab should error on a transport failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "https://hdbits.org") {
		t.Errorf("grab error should surface the host-only cause, got %q", msg)
	}
	if !strings.Contains(msg, "hdbits: download") {
		t.Errorf("grab error should retain the family/download prefix, got %q", msg)
	}
	for _, leak := range []string{credPass, "/dl/" + credPass, "passkey=" + credPass} {
		if strings.Contains(msg, leak) {
			t.Errorf("grab error leaks %q: %q", leak, msg)
		}
	}
}

// TestGrabContextErrorPassesThrough proves a cancellation/deadline from the fetch is
// preserved (not flattened into the generic "download request failed"), so callers and
// health classification can tell a cancelled request from a real failure.
func TestGrabContextErrorPassesThrough(t *testing.T) {
	t.Parallel()
	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		d := liveDriver(t, &scriptDoer{})
		d.Doer = &errorDoer{err: want}
		_, err := d.Grab(context.Background(), grabURL)
		if !errors.Is(err, want) {
			t.Errorf("Grab err = %v, want errors.Is %v", err, want)
		}
	}
}

// TestGrabStatusDispatch proves the download status handling: 401 maps to
// login.ErrLoginFailed (auth_failure health), 403 (HDBits' query/rate-limit) and 429/503 map
// to a RateLimitedError (never an auth failure), and any other non-2xx is a plain error.
func TestGrabStatusDispatch(t *testing.T) {
	t.Parallel()
	d401 := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusUnauthorized, "nope")
	}})
	if _, err := d401.Grab(context.Background(), grabURL); !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("HTTP 401: err = %v, want login.ErrLoginFailed", err)
	}
	for _, status := range []int{stdhttp.StatusForbidden, stdhttp.StatusTooManyRequests, stdhttp.StatusServiceUnavailable} {
		d := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
			return mkResp(status, "slow down")
		}})
		_, err := d.Grab(context.Background(), grabURL)
		var rl *search.RateLimitedError
		if !errors.As(err, &rl) {
			t.Errorf("HTTP %d: err = %v, want *search.RateLimitedError", status, err)
		}
		if errors.Is(err, login.ErrLoginFailed) {
			t.Errorf("HTTP %d: err = %v, must NOT be login.ErrLoginFailed", status, err)
		}
	}
	d := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusInternalServerError, "boom")
	}})
	if _, err := d.Grab(context.Background(), grabURL); err == nil {
		t.Errorf("HTTP 500: err = nil, want an error")
	}
}
