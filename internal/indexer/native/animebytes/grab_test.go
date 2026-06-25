package animebytes

import (
	"context"
	"errors"
	stdhttp "net/http"
	"strings"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// fakeTorrent is a minimal bencode-shaped body; its content is irrelevant to the driver
// (it is returned verbatim to the /dl proxy).
const fakeTorrent = "d8:announce18:https://ab.test/x4:infod6:lengthi1e4:name4:fileee"

// downloadLink is an AnimeBytes download URL — the passkey rides in the PATH (not a query
// param), matching the Link the parser passes through, so RedactURL (query-only) cannot
// strip it; the driver must keep the URL out of every error.
const downloadLink = "https://animebytes.tv/torrent/67890/download/" + credPass

// TestGrab proves the download URL is fetched server-side and returned as a direct
// torrent (no redirect, JSON not forced on the bytes), and that no passkey leaks into the
// result body.
func TestGrab(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		r := httpResp(stdhttp.StatusOK, fakeTorrent)
		r.Header.Set("Content-Type", "application/x-bittorrent")
		return r
	}}
	d := liveDriver(doer)

	res, err := d.Grab(context.Background(), downloadLink)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(res.Body) != fakeTorrent {
		t.Errorf("body = %q, want the torrent bytes", res.Body)
	}
	if res.ContentType != "application/x-bittorrent" {
		t.Errorf("ContentType = %q", res.ContentType)
	}
	if res.Redirect != "" {
		t.Errorf("Redirect = %q, want empty (direct torrent)", res.Redirect)
	}

	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.reqs))
	}
	dl := doer.reqs[0]
	if dl.method != stdhttp.MethodGet {
		t.Errorf("method = %s, want GET", dl.method)
	}
	if dl.accept != "" {
		t.Errorf("download Accept = %q, want empty (do not force JSON on a .torrent)", dl.accept)
	}
	// The download URL carries the passkey (in its path) — sent only server-side; the
	// result body must not echo it.
	if strings.Contains(string(res.Body), credPass) {
		t.Errorf("result body leaks the passkey: %q", res.Body)
	}
}

// TestGrabStatusErrors proves a 429 is a rate-limit error, a 403 is an auth failure, and
// another non-2xx is a plain error — none leaking the passkey.
func TestGrabStatusErrors(t *testing.T) {
	t.Parallel()
	mk := func(status int) *driver {
		return liveDriver(&scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
			return httpResp(status, "nope")
		}})
	}

	_, err := mk(stdhttp.StatusTooManyRequests).Grab(context.Background(), downloadLink)
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Errorf("429: err = %v, want *search.RateLimitedError", err)
	}

	_, err = mk(stdhttp.StatusForbidden).Grab(context.Background(), downloadLink)
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("403: err = %v, want login.ErrLoginFailed", err)
	}

	_, err = mk(stdhttp.StatusInternalServerError).Grab(context.Background(), downloadLink)
	if err == nil {
		t.Fatal("500: want an error")
	}
	if strings.Contains(err.Error(), credPass) {
		t.Errorf("500 error leaks the passkey: %v", err)
	}
}

// errDoer fails the download fetch with a transport error — the case where the download
// URL (whose passkey is in the path) could leak through the wrapped error.
type errDoer struct{ err error }

func (e *errDoer) Do(_ *stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

// TestGrabTransportErrorSanitized proves a transport failure surfaces a fixed,
// passkey-free, URL-free error even though the download URL carries the passkey in its
// path (where RedactURL cannot reach it).
func TestGrabTransportErrorSanitized(t *testing.T) {
	t.Parallel()
	d := liveDriver(nil)
	d.doer = &errDoer{err: errors.New("dial tcp: connection refused " + downloadLink)}

	_, err := d.Grab(context.Background(), downloadLink)
	if err == nil {
		t.Fatal("want a transport error")
	}
	if strings.Contains(err.Error(), credPass) || strings.Contains(err.Error(), downloadLink) {
		t.Errorf("download URL/passkey leaked into the error: %v", err)
	}
	if strings.Contains(apphttp.RedactError(err), credPass) {
		t.Errorf("RedactError leaks the passkey: %v", apphttp.RedactError(err))
	}
}

// TestGrabContextSentinelsPreserved proves context cancellation/deadline pass through
// sanitizeGrabError unchanged (so normal cancellation is not misreported as a failure),
// while still never carrying the URL.
func TestGrabContextSentinelsPreserved(t *testing.T) {
	t.Parallel()
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		d := liveDriver(nil)
		d.doer = &errDoer{err: sentinel}
		_, err := d.Grab(context.Background(), downloadLink)
		if !errors.Is(err, sentinel) {
			t.Errorf("err = %v, want %v passed through", err, sentinel)
		}
	}
}

// TestTestAuthFailure proves Test surfaces an auth failure (the JSON {"error":…} envelope
// AnimeBytes returns with HTTP 200) as login.ErrLoginFailed, with the echoed passkey
// scrubbed.
func TestTestAuthFailure(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return httpResp(stdhttp.StatusOK, `{"error":"Invalid passkey `+credPass+`"}`)
	}}
	err := liveDriver(doer).Test(context.Background())
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("err = %v, want login.ErrLoginFailed", err)
	}
	if strings.Contains(err.Error(), credPass) {
		t.Errorf("error leaks the passkey: %v", err)
	}
}

// TestTestSuccess proves Test passes when an empty-result probe authenticates (HTTP 200
// with the empty envelope).
func TestTestSuccess(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return httpResp(stdhttp.StatusOK, `{"Matches":0,"Groups":[]}`)
	}}
	if err := liveDriver(doer).Test(context.Background()); err != nil {
		t.Errorf("Test: %v", err)
	}
}

func TestReadCapped(t *testing.T) {
	t.Parallel()
	if _, err := readCapped(strings.NewReader("0123456789AB"), 10); !errors.Is(err, errDownloadTooLarge) {
		t.Errorf("12 bytes over cap 10: err = %v, want errDownloadTooLarge", err)
	}
	got, err := readCapped(strings.NewReader("0123456789"), 10)
	if err != nil || len(got) != 10 {
		t.Errorf("at cap: len=%d err=%v, want 10/nil", len(got), err)
	}
	got, err = readCapped(strings.NewReader("hello"), 10)
	if err != nil || string(got) != "hello" {
		t.Errorf("under cap: got=%q err=%v", got, err)
	}
}
