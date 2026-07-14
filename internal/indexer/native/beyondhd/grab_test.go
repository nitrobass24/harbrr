package beyondhd

import (
	"context"
	"errors"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// downloadURL is the synthetic BeyondHD download_url shape: the rsskey rides in the URL
// PATH (auto.<id>.<rsskey>), not the query, so apphttp.RedactURL cannot hide it and the
// driver must keep the rsskey out of every error (only the scheme://host may surface).
const downloadURL = "https://beyond-hd.me/torrent/download/auto.12345." + credRSSKey

// TestGrabReturnsTorrentBytes proves Grab fetches the download_url server-side and returns
// the .torrent body and content type. A plain GET (no auth header) is issued because the
// rsskey rides in the URL itself.
func TestGrabReturnsTorrentBytes(t *testing.T) {
	t.Parallel()
	const torrent = "d8:announce20:https://tracker/anne"
	doer := &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		resp := mkResp(stdhttp.StatusOK, torrent)
		resp.Header.Set("Content-Type", "application/x-bittorrent")
		return resp
	}}
	d := liveDriver(t, doer)

	got, err := d.Grab(context.Background(), downloadURL)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(got.Body) != torrent {
		t.Errorf("body = %q, want %q", got.Body, torrent)
	}
	if got.ContentType != "application/x-bittorrent" {
		t.Errorf("content type = %q, want application/x-bittorrent", got.ContentType)
	}
	if got.Redirect != "" {
		t.Errorf("redirect = %q, want empty (direct torrent)", got.Redirect)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.reqs))
	}
	r := doer.reqs[0]
	if r.method != stdhttp.MethodGet {
		t.Errorf("method = %s, want GET", r.method)
	}
	if r.url != downloadURL {
		t.Errorf("url = %q, want the verbatim download_url", r.url)
	}
}

// TestGrabStatusDispatch proves Grab maps the download HTTP status: 401/403 are auth
// failures (login.ErrLoginFailed), 429/503 are rate-limits (RateLimitedError), and any other
// non-2xx is a plain error — and none of those error strings leaks the rsskey-bearing URL.
func TestGrabStatusDispatch(t *testing.T) {
	t.Parallel()
	mk := func(status int) *driver {
		return liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
			return mkResp(status, "nope")
		}})
	}

	for _, status := range []int{stdhttp.StatusUnauthorized, stdhttp.StatusForbidden} {
		_, err := mk(status).Grab(context.Background(), downloadURL)
		if !errors.Is(err, login.ErrLoginFailed) {
			t.Errorf("HTTP %d: err = %v, want login.ErrLoginFailed", status, err)
		}
	}

	for _, status := range []int{stdhttp.StatusTooManyRequests, stdhttp.StatusServiceUnavailable} {
		_, err := mk(status).Grab(context.Background(), downloadURL)
		var rl *search.RateLimitedError
		if !errors.As(err, &rl) {
			t.Errorf("HTTP %d: err = %v, want *search.RateLimitedError", status, err)
		}
	}

	_, err := mk(stdhttp.StatusInternalServerError).Grab(context.Background(), downloadURL)
	if err == nil {
		t.Fatalf("HTTP 500: err = nil, want an error")
	}
	if strings.Contains(err.Error(), credRSSKey) {
		t.Errorf("HTTP 500 error leaks the rsskey: %v", err)
	}
}

// TestGrabTransportErrorNeverLeaksURL proves a transport (network) error from the download
// fetch surfaces only the scheme://host (which is not a secret) and never the rsskey — even
// when the failing *url.Error hides the rsskey in BOTH a path segment and a query param.
// http.Client.Do returns a *url.Error whose Error() quotes its full URL, so the driver must
// route the cause through host-only redaction before wrapping it.
func TestGrabTransportErrorNeverLeaksURL(t *testing.T) {
	t.Parallel()
	def := Families()[0].Definition
	// A realistic transport failure: http.Client.Do hands back a *url.Error carrying the
	// rsskey in the path (/dl/<rsskey>) and the query (?passkey=<rsskey>).
	failURL := "https://beyond-hd.me/dl/" + credRSSKey + "?passkey=" + credRSSKey
	doer := errDoer{err: &url.Error{
		Op:  "Get",
		URL: failURL,
		Err: errors.New("dial tcp: connection refused"),
	}}
	d, err := New(native.Params{Def: def, Cfg: creds(), Doer: doer, Clock: fixedClock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = d.Grab(context.Background(), downloadURL)
	if err == nil {
		t.Fatal("Grab: err = nil, want a transport error")
	}
	msg := err.Error()
	// The host is not a secret and now surfaces so the failure is diagnosable.
	if !strings.Contains(msg, "https://beyond-hd.me") {
		t.Errorf("transport error should surface the host: %v", err)
	}
	// The rsskey must never surface, in any position it hid.
	if strings.Contains(msg, credRSSKey) {
		t.Errorf("transport error leaks the rsskey: %v", err)
	}
	if strings.Contains(msg, "/dl/"+credRSSKey) {
		t.Errorf("transport error leaks the secret path segment: %v", err)
	}
	if strings.Contains(msg, "passkey="+credRSSKey) {
		t.Errorf("transport error leaks the secret query param: %v", err)
	}
	// The base download prefix is still present so the failure is recognizable.
	if !strings.Contains(msg, "beyondhd: download to https://beyond-hd.me failed") {
		t.Errorf("transport error missing the base prefix: %v", err)
	}
}

// TestGrabDownloadTooLarge proves a download body over the cap is rejected (rather than
// silently truncated) with the native size-cap sentinel, which Grab passes through.
func TestGrabDownloadTooLarge(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", (64<<20)+1)
	d := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusOK, big)
	}})
	_, err := d.Grab(context.Background(), downloadURL)
	if !errors.Is(err, native.ErrDownloadTooLarge) {
		t.Errorf("err = %v, want native.ErrDownloadTooLarge", err)
	}
}
