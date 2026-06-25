package beyondhd

import (
	"context"
	"errors"
	stdhttp "net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// downloadURL is the synthetic BeyondHD download_url shape: the rsskey rides in the URL
// PATH (auto.<id>.<rsskey>), not the query, so apphttp.RedactURL cannot hide it and the
// driver must keep the URL out of every error.
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
// fetch never surfaces the rsskey-bearing download URL. apphttp.RedactURL would NOT hide a
// path-embedded rsskey, so the driver collapses the error to a fixed, link-free message.
func TestGrabTransportErrorNeverLeaksURL(t *testing.T) {
	t.Parallel()
	def := Families()[0].Definition
	doer := errDoer{err: errors.New("dial tcp beyond-hd.me/torrent/download/auto.12345." + credRSSKey + ": connection refused")}
	d, err := New(native.Params{Def: def, Cfg: creds(), Doer: doer, Clock: fixedClock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = d.Grab(context.Background(), downloadURL)
	if err == nil {
		t.Fatal("Grab: err = nil, want a transport error")
	}
	if strings.Contains(err.Error(), credRSSKey) {
		t.Errorf("transport error leaks the rsskey: %v", err)
	}
	if strings.Contains(err.Error(), "beyond-hd.me") {
		t.Errorf("transport error leaks the download URL: %v", err)
	}
}

// TestGrabPreservesSentinels proves sanitizeGrabError passes through the sentinels callers
// must classify (auth, rate-limit, context cancellation/deadline, the size cap) unchanged
// while collapsing any other error to the fixed link-free message.
func TestGrabPreservesSentinels(t *testing.T) {
	t.Parallel()
	rl := &search.RateLimitedError{StatusCode: stdhttp.StatusTooManyRequests}
	cases := []struct {
		name string
		in   error
		same bool // expect the same error back (sentinel preserved)
	}{
		{"auth", login.ErrLoginFailed, true},
		{"rate-limit", rl, true},
		{"context canceled", context.Canceled, true},
		{"deadline", context.DeadlineExceeded, true},
		{"too large", errDownloadTooLarge, true},
		{"other transport", errors.New("dial tcp ...auto.12345." + credRSSKey + ": refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeGrabError(tc.in)
			if tc.same && !errors.Is(got, tc.in) {
				t.Errorf("sanitizeGrabError(%v) = %v, want the sentinel preserved", tc.in, got)
			}
			if !tc.same {
				if errors.Is(got, tc.in) {
					t.Errorf("sanitizeGrabError did not collapse a non-sentinel error: %v", got)
				}
				if strings.Contains(got.Error(), credRSSKey) {
					t.Errorf("collapsed error leaks the rsskey: %v", got)
				}
			}
		})
	}
}

// TestGrabDownloadTooLarge proves a download body over the cap is rejected (rather than
// silently truncated) with the size-cap sentinel, which Grab passes through.
func TestGrabDownloadTooLarge(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", maxTorrentBytes+1)
	d := liveDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusOK, big)
	}})
	_, err := d.Grab(context.Background(), downloadURL)
	if !errors.Is(err, errDownloadTooLarge) {
		t.Errorf("err = %v, want errDownloadTooLarge", err)
	}
}
