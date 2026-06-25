package torrentday

import (
	"bytes"
	"context"
	"errors"
	stdhttp "net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// torrentBytes is a synthetic .torrent body (a bencoded stub); the driver returns it
// verbatim, so the exact bytes only need to round-trip.
const torrentBytes = "d8:announce20:http://td/announce!e"

// downloadLink is the resolved download URL the served feed routes through /dl; the
// torrent id fills both path segments (download.php/<id>/<id>.torrent).
const downloadLink = base + "download.php/2743197/2743197.torrent"

// TestGrabReturnsTorrentBytes proves Grab fetches the .torrent with the session cookie and
// returns the body + content type verbatim.
func TestGrabReturnsTorrentBytes(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		r := resp(stdhttp.StatusOK, torrentBytes)
		r.Header.Set("Content-Type", "application/x-bittorrent")
		return r
	}}
	d := testDriver(t, doer, nil)

	res, err := d.Grab(context.Background(), downloadLink)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if !bytes.Equal(res.Body, []byte(torrentBytes)) {
		t.Errorf("Body = %q, want the torrent bytes", res.Body)
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
	got := doer.reqs[0]
	if got.cookie != credCookie {
		t.Errorf("Cookie header = %q, want the configured cookie", got.cookie)
	}
	// The cookie rides only the Cookie header, never the download URL.
	assertNoSecret(t, got.url)
}

// TestGrabStatusDispatch proves Grab maps the response status: 429/503 -> rate-limit;
// 401/403 and a redirect to /login.php -> auth failure; any other non-2xx -> error.
func TestGrabStatusDispatch(t *testing.T) {
	t.Parallel()
	mkStatus := func(r *stdhttp.Response) *driver {
		return testDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response { return r }}, nil)
	}
	tests := []struct {
		name     string
		resp     *stdhttp.Response
		wantAuth bool
		wantRate bool
		wantErr  bool
	}{
		{name: "rate limit 429", resp: resp(stdhttp.StatusTooManyRequests, ""), wantRate: true},
		{name: "rate limit 503", resp: resp(stdhttp.StatusServiceUnavailable, ""), wantRate: true},
		{name: "unauthorized 401", resp: resp(stdhttp.StatusUnauthorized, ""), wantAuth: true},
		{name: "forbidden 403", resp: resp(stdhttp.StatusForbidden, ""), wantAuth: true},
		{name: "login redirect", resp: redirectResp(stdhttp.StatusFound, base+"login.php"), wantAuth: true},
		{name: "server error 500", resp: resp(stdhttp.StatusInternalServerError, ""), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := mkStatus(tt.resp)
			_, err := d.Grab(context.Background(), downloadLink)
			if err == nil {
				t.Fatal("Grab: want error, got nil")
			}
			var rl *search.RateLimitedError
			switch {
			case tt.wantAuth && !errors.Is(err, login.ErrLoginFailed):
				t.Errorf("err = %v, want login.ErrLoginFailed", err)
			case tt.wantRate && !errors.As(err, &rl):
				t.Errorf("err = %v, want RateLimitedError", err)
			case tt.wantErr && (errors.Is(err, login.ErrLoginFailed) || errors.As(err, &rl)):
				t.Errorf("err = %v, want a generic error", err)
			}
			assertNoSecret(t, err.Error())
		})
	}
}

// TestGrabTransportErrorHidesURLAndSecret proves a transport error surfaces neither the
// download URL nor the cookie: sanitizeGrabError replaces a non-sentinel error with a
// fixed message (the URL can carry the torrent id; the cookie never escapes).
func TestGrabTransportErrorHidesURLAndSecret(t *testing.T) {
	t.Parallel()
	d := testDriver(t, nil, nil)
	d.doer = &errDoer{err: errors.New("dial tcp " + downloadLink + " cookie=" + credCookie + ": refused")}

	_, err := d.Grab(context.Background(), downloadLink)
	if err == nil {
		t.Fatal("Grab: want error, got nil")
	}
	assertNoSecret(t, err.Error())
	if strings.Contains(err.Error(), "download.php") || strings.Contains(err.Error(), "2743197") {
		t.Errorf("error leaks the download URL: %q", err)
	}
}

// TestGrabTransportErrorPreservesSentinels proves sanitizeGrabError keeps the auth and
// rate-limit sentinels (for health classification) even though the get() error is
// scrubbed.
func TestGrabTransportErrorPreservesSentinels(t *testing.T) {
	t.Parallel()
	if got := sanitizeGrabError(login.ErrLoginFailed); !errors.Is(got, login.ErrLoginFailed) {
		t.Errorf("sanitizeGrabError dropped login sentinel: %v", got)
	}
	rl := &search.RateLimitedError{StatusCode: 429}
	if got := sanitizeGrabError(rl); !errors.As(got, &rl) {
		t.Errorf("sanitizeGrabError dropped rate-limit sentinel: %v", got)
	}
	if got := sanitizeGrabError(errors.New("dial tcp " + credCookie)); strings.Contains(got.Error(), credCookie) {
		t.Errorf("sanitizeGrabError leaked secret: %v", got)
	}
}

// TestTestSurfacesAuthFailure proves Test issues an empty browse and surfaces a stale
// cookie (a redirect to /login.php) as login.ErrLoginFailed, with the cookie scrubbed.
func TestTestSurfacesAuthFailure(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return redirectResp(stdhttp.StatusFound, base+"login.php")
	}}
	d := testDriver(t, doer, nil)

	err := d.Test(context.Background())
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("Test err = %v, want login.ErrLoginFailed", err)
	}
	assertNoSecret(t, err.Error())
}

// TestTestSucceeds proves Test returns nil when the cookie authenticates (an empty JSON
// array is a valid, zero-result browse).
func TestTestSucceeds(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		return resp(stdhttp.StatusOK, "[]")
	}}
	d := testDriver(t, doer, nil)

	if err := d.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.reqs))
	}
	// Test browses with no term: t.json?q=
	if !strings.HasPrefix(doer.reqs[0].url, base+"t.json?q=") {
		t.Errorf("Test request = %q, want an empty browse", doer.reqs[0].url)
	}
}
