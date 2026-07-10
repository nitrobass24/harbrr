package torrentday

import (
	"bytes"
	"context"
	"errors"
	stdhttp "net/http"
	"net/url"
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

// grabErrHost is the scheme://host of the fabricated transport failure below. It is not a
// secret: RedactURLError surfaces the host so a failure is diagnosable, while the
// secret-bearing path and query are dropped.
const grabErrHost = "https://torrentday.example"

// TestGrabTransportErrorSurfacesHostHidesSecret proves a realistic transport error — a
// *url.Error, the shape http.Client.Do returns — surfaces only its host-only cause: the
// scheme://host survives (it is not a secret and aids diagnosis), while a secret hidden in
// a path segment AND a query param is dropped, and the cookie never escapes. Production
// only ever hands sanitizeGrabError a host-only error (get() rebuilds the *url.Error
// host-only before wrapping), so the injected error mirrors that.
func TestGrabTransportErrorSurfacesHostHidesSecret(t *testing.T) {
	t.Parallel()
	// A synthetic secret in both a path segment and a query param; the token is one
	// assertNoSecret recognises, so a leak into the returned error is caught.
	const secret = "SECRET-deadbeefdeadbeefdeadbeefdeadbeef"
	d := testDriver(t, nil, nil)
	d.doer = &errDoer{err: &url.Error{
		Op:  "Get",
		URL: grabErrHost + "/dl/" + secret + "?passkey=" + secret,
		Err: errors.New("dial tcp: connection refused"),
	}}

	_, err := d.Grab(context.Background(), downloadLink)
	if err == nil {
		t.Fatal("Grab: want error, got nil")
	}
	got := err.Error()
	assertNoSecret(t, got)
	if !strings.Contains(got, grabErrHost) {
		t.Errorf("error dropped the host (it is not a secret): %q", got)
	}
	if strings.Contains(got, secret) ||
		strings.Contains(got, "/dl/"+secret) ||
		strings.Contains(got, "passkey="+secret) {
		t.Errorf("error leaks the path/query secret: %q", got)
	}
	if !strings.Contains(got, "torrentday: download request failed") {
		t.Errorf("error dropped the fixed prefix: %q", got)
	}
}

// TestGrabTransportErrorPreservesSentinels proves sanitizeGrabError keeps the auth and
// rate-limit sentinels (for health classification) rather than flattening them into the
// generic "download request failed" wrap.
func TestGrabTransportErrorPreservesSentinels(t *testing.T) {
	t.Parallel()
	if got := sanitizeGrabError(login.ErrLoginFailed); !errors.Is(got, login.ErrLoginFailed) {
		t.Errorf("sanitizeGrabError dropped login sentinel: %v", got)
	}
	rl := &search.RateLimitedError{StatusCode: 429}
	if got := sanitizeGrabError(rl); !errors.As(got, &rl) {
		t.Errorf("sanitizeGrabError dropped rate-limit sentinel: %v", got)
	}
}

// TestGrabContextErrorPassesThrough proves a cancellation/deadline from the fetch is
// preserved (not flattened into the generic "download request failed"), so callers and
// health classification can tell a cancelled request from a real failure.
func TestGrabContextErrorPassesThrough(t *testing.T) {
	t.Parallel()
	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		d := testDriver(t, nil, nil)
		d.doer = &errDoer{err: want}
		_, err := d.Grab(context.Background(), downloadLink)
		if !errors.Is(err, want) {
			t.Errorf("Grab err = %v, want errors.Is %v", err, want)
		}
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
