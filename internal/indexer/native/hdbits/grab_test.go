package hdbits

import (
	"context"
	"errors"
	stdhttp "net/http"
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

// errorDoer fails every request with a transport error that echoes the URL, so the test
// can prove the grab error never leaks the passkey-bearing link.
type errorDoer struct{ err error }

func (e *errorDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

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

// TestGrabTransportErrorNeverLeaksURL proves a transport error from the download fetch is
// sanitized to a fixed message that carries neither the URL nor the embedded passkey.
func TestGrabTransportErrorNeverLeaksURL(t *testing.T) {
	t.Parallel()
	// The transport error echoes the full URL (incl. the passkey) to simulate a hostile or
	// verbose error; the sanitizer must drop all of it.
	d := liveDriver(t, &scriptDoer{})
	d.doer = &errorDoer{err: errors.New("dial tcp: " + grabURL)}

	_, err := d.Grab(context.Background(), grabURL)
	if err == nil {
		t.Fatal("Grab should error on a transport failure")
	}
	msg := err.Error()
	for _, leak := range []string{grabURL, credPass, "hdbits.test"} {
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
		d.doer = &errorDoer{err: want}
		_, err := d.Grab(context.Background(), grabURL)
		if !errors.Is(err, want) {
			t.Errorf("Grab err = %v, want errors.Is %v", err, want)
		}
	}
}

// TestGrabStatusDispatch proves the download status handling: 401/403 maps to
// login.ErrLoginFailed (so the registry records an auth_failure health event), 429/503
// maps to a RateLimitedError, and any other non-2xx is a plain error.
func TestGrabStatusDispatch(t *testing.T) {
	t.Parallel()
	for _, status := range []int{stdhttp.StatusUnauthorized, stdhttp.StatusForbidden} {
		d := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
			return mkResp(status, "nope")
		}})
		if _, err := d.Grab(context.Background(), grabURL); !errors.Is(err, login.ErrLoginFailed) {
			t.Errorf("HTTP %d: err = %v, want login.ErrLoginFailed", status, err)
		}
	}
	for _, status := range []int{stdhttp.StatusTooManyRequests, stdhttp.StatusServiceUnavailable} {
		d := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
			return mkResp(status, "slow down")
		}})
		_, err := d.Grab(context.Background(), grabURL)
		var rl *search.RateLimitedError
		if !errors.As(err, &rl) {
			t.Errorf("HTTP %d: err = %v, want *search.RateLimitedError", status, err)
		}
	}
	d := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusInternalServerError, "boom")
	}})
	if _, err := d.Grab(context.Background(), grabURL); err == nil {
		t.Errorf("HTTP 500: err = nil, want an error")
	}
}
