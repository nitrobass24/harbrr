package gazellegames

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

// fakeTorrent is a minimal bencode-shaped body (content is irrelevant to the driver; it
// is returned verbatim to the /dl proxy).
const fakeTorrent = "d8:announce24:https://gazellegames.test4:infod6:lengthi1e4:name4:gameee"

// downloadLink is the Prowlarr-style rebuilt download URL: the passkey rides in
// torrent_pass (authkey is a dummy), so any error or result body that echoes it would
// leak the secret.
const downloadLink = "https://gazellegames.net/torrents.php?action=download&id=42&authkey=prowlarr&torrent_pass=" + credPasskey

// errDoer fails every request with a fixed transport error — the case where the download
// URL (which carries the passkey in its torrent_pass query) could leak through a wrapped
// error.
type errDoer struct{ err error }

func (e *errDoer) Do(_ *stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

// TestGrab proves the rebuilt torrents.php URL is fetched server-side and returned as a
// direct torrent (no redirect), that the .torrent bytes pass through verbatim, and that
// no passkey leaks into the result body.
func TestGrab(t *testing.T) {
	t.Parallel()
	resp := mkResp(stdhttp.StatusOK, fakeTorrent)
	resp.Header.Set("Content-Type", "application/x-bittorrent")
	doer := &scriptDoer{resp: resp}
	d := searchDriver(t, doer)

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
	if doer.reqs[0].method != stdhttp.MethodGet {
		t.Errorf("method = %q, want GET", doer.reqs[0].method)
	}
	// The download URL DOES carry the passkey (Prowlarr style) — it is only ever sent
	// server-side here; the result body must not echo it.
	if strings.Contains(string(res.Body), credPasskey) {
		t.Errorf("result body leaks the passkey: %q", res.Body)
	}
}

// TestGrabStatusErrors proves a 429 is a rate-limit error, a 403 is an auth failure, and
// another non-2xx is a plain error — none leaking the passkey.
func TestGrabStatusErrors(t *testing.T) {
	t.Parallel()
	mk := func(status int) *driver {
		return searchDriver(t, &scriptDoer{resp: mkResp(status, "nope")})
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
	if strings.Contains(err.Error(), credPasskey) {
		t.Errorf("500 error leaks the passkey: %v", err)
	}
}

// TestGrabTransportErrorSanitized proves a transport failure during the download fetch
// surfaces a fixed, passkey-free error even though the download URL carries the passkey
// in its torrent_pass query.
func TestGrabTransportErrorSanitized(t *testing.T) {
	t.Parallel()
	d := searchDriver(t, &errDoer{err: errors.New("connection refused")})

	_, err := d.Grab(context.Background(), downloadLink)
	if err == nil {
		t.Fatal("want a transport error")
	}
	if strings.Contains(err.Error(), credPasskey) || strings.Contains(err.Error(), downloadLink) {
		t.Errorf("download URL/passkey leaked into the error: %v", err)
	}
	if strings.Contains(apphttp.RedactError(err), credPasskey) {
		t.Errorf("RedactError leaks the passkey: %v", apphttp.RedactError(err))
	}
}

// TestGrabContextSentinelsPreserved proves cancellation/deadline sentinels survive
// sanitizeGrabError so normal cancellation is not misreported as a download failure.
func TestGrabContextSentinelsPreserved(t *testing.T) {
	t.Parallel()
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		d := searchDriver(t, &errDoer{err: sentinel})
		_, err := d.Grab(context.Background(), downloadLink)
		if !errors.Is(err, sentinel) {
			t.Errorf("err = %v, want %v preserved", err, sentinel)
		}
	}
}

// TestReadCapped proves the size cap errors (not truncates) over the limit and reads
// through at and under it.
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

// routeDoer answers the quick_user passkey fetch and the search from one mock: a
// request=quick_user URL returns quickUser, everything else returns search.
type routeDoer struct {
	quickUser *stdhttp.Response
	search    *stdhttp.Response
}

func (r *routeDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	if strings.Contains(req.URL.RawQuery, "request=quick_user") {
		return r.quickUser, nil
	}
	return r.search, nil
}

// TestTestProbesAuth proves Test fetches the passkey (request=quick_user) and runs the
// latest-torrents search, and surfaces a 401/403 (from either step) as login.ErrLoginFailed
// (so the registry records an auth_failure health event), without leaking the apikey.
func TestTestProbesAuth(t *testing.T) {
	t.Parallel()
	ok := searchDriver(t, &routeDoer{
		quickUser: mkResp(stdhttp.StatusOK, `{"status":"success","response":{"passkey":"`+credPasskey+`"}}`),
		search:    mkResp(stdhttp.StatusOK, `{"status":"success","response":[]}`),
	})
	if err := ok.Test(context.Background()); err != nil {
		t.Fatalf("Test (success): %v", err)
	}

	// A 401/403 on the passkey fetch is an auth failure that never reaches the search.
	for _, code := range []int{stdhttp.StatusUnauthorized, stdhttp.StatusForbidden} {
		d := searchDriver(t, &scriptDoer{resp: mkResp(code, "")})
		err := d.Test(context.Background())
		if !errors.Is(err, login.ErrLoginFailed) {
			t.Fatalf("HTTP %d: Test err = %v, want login.ErrLoginFailed", code, err)
		}
		if strings.Contains(err.Error(), credAPIKey) {
			t.Errorf("Test error leaks the apikey: %v", err)
		}
	}
}
