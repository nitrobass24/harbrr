package iptorrents

import (
	"context"
	"errors"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// fakeTorrent is a minimal bencode-shaped body (content is irrelevant to the driver; it
// is returned verbatim to the /dl proxy).
const fakeTorrent = "d8:announce20:https://ipt.test/an4:infod6:lengthi1e4:name4:fileee"

// TestGrab proves the download is fetched with the Cookie + User-Agent headers and
// returned as a direct torrent (no redirect), and that no credential leaks into the
// recorded URL or the returned bytes.
func TestGrab(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		r := resp(stdhttp.StatusOK, fakeTorrent)
		r.Header.Set("Content-Type", "application/x-bittorrent")
		return r
	}}
	d := testDriver(doer, nil)

	res, err := d.Grab(context.Background(), "https://iptorrents.com/download.php/9/file.torrent")
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
	if dl.method != stdhttp.MethodGet || dl.cookie != credCookie || dl.userAgent != credUA {
		t.Errorf("download request = %s cookie=%q ua=%q, want GET with the cookie+UA", dl.method, dl.cookie, dl.userAgent)
	}
	if dl.accept != "" {
		t.Errorf("download Accept = %q, want empty (do not force a type on a .torrent)", dl.accept)
	}
	assertNoSecret(t, string(res.Body))
	assertNoSecret(t, dl.url)
}

// TestGrabStatusErrors proves a 429 is a rate-limit error, a 401/403 is an auth failure,
// and another non-2xx is a plain error — none leaking a credential.
func TestGrabStatusErrors(t *testing.T) {
	t.Parallel()
	mk := func(status int) *driver {
		return testDriver(&scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
			return resp(status, "nope")
		}}, nil)
	}

	_, err := mk(stdhttp.StatusTooManyRequests).Grab(context.Background(), "https://iptorrents.com/dl/1")
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Errorf("429: err = %v, want *search.RateLimitedError", err)
	}

	_, err = mk(stdhttp.StatusUnauthorized).Grab(context.Background(), "https://iptorrents.com/dl/1")
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("401: err = %v, want login.ErrLoginFailed", err)
	}

	_, err = mk(stdhttp.StatusForbidden).Grab(context.Background(), "https://iptorrents.com/dl/1")
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("403: err = %v, want login.ErrLoginFailed", err)
	}

	_, err = mk(stdhttp.StatusInternalServerError).Grab(context.Background(), "https://iptorrents.com/dl/1")
	if err == nil {
		t.Fatal("500: want an error")
	}
	assertNoSecret(t, err.Error())
	assertNoSecret(t, apphttp.RedactError(err))
}

// errorDoer fails every request with a transport error — the case where get would wrap
// the (possibly path-key-bearing) download URL.
type errorDoer struct{ err error }

func (e *errorDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

// TestGrabTransportErrorSanitized proves a transport failure surfaces the endpoint's
// scheme://host (not a secret, useful for diagnosis) while the download link's secret —
// carried in BOTH a PATH segment and a query param of the real *url.Error http.Client.Do
// returns — never reaches the error. The RedactURLError fallback rebuilds that *url.Error
// host-only, which the query-scoped URL redactor could not do for the path secret.
func TestGrabTransportErrorSanitized(t *testing.T) {
	t.Parallel()
	const (
		base   = "https://iptorrents.example"
		secret = "SECRET-dlkey-7f3a9c"
		link   = base + "/download.php/9/file.torrent"
	)
	d := testDriver(nil, nil)
	d.doer = &errorDoer{err: &url.Error{
		Op:  "Get",
		URL: base + "/dl/" + secret + "?passkey=" + secret,
		Err: errors.New("dial tcp: connection refused"),
	}}
	_, err := d.Grab(context.Background(), link)
	if err == nil {
		t.Fatal("want a transport error")
	}
	got := err.Error()
	if !strings.Contains(got, base) {
		t.Errorf("error dropped the scheme://host %q (host is not a secret): %v", base, err)
	}
	if strings.Contains(got, secret) ||
		strings.Contains(got, "/dl/"+secret) ||
		strings.Contains(got, "passkey="+secret) {
		t.Errorf("download link secret leaked into the error: %v", err)
	}
	if !strings.Contains(got, "iptorrents: download request failed") {
		t.Errorf("error dropped the fixed prefix: %v", err)
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
}
