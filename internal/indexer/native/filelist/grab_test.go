package filelist

import (
	"context"
	"encoding/base64"
	"errors"
	stdhttp "net/http"
	"strings"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// fakeTorrent is a minimal bencode-shaped body (content is irrelevant to the driver;
// it is returned verbatim to the /dl proxy).
const fakeTorrent = "d8:announce20:https://filelist.test4:infod6:lengthi1e4:name4:fileee"

func grabDriver(handler func(*stdhttp.Request, string) *stdhttp.Response) (*driver, *scriptDoer) {
	doer := &scriptDoer{handler: handler}
	return liveDriver(doer), doer
}

// TestGrab proves the rebuilt download.php URL is fetched with the Basic header and
// returned as a direct torrent (no redirect), that JSON is not forced on the .torrent,
// and that no passkey leaks into the result body.
func TestGrab(t *testing.T) {
	t.Parallel()
	const link = "https://filelist.test/download.php?id=12345&passkey=" + credPass
	d, doer := grabDriver(func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		r := resp(stdhttp.StatusOK, fakeTorrent)
		r.Header.Set("Content-Type", "application/x-bittorrent")
		return r
	})

	res, err := d.Grab(context.Background(), link)
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
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(credUser+":"+credPass))
	if dl.method != stdhttp.MethodGet || dl.auth != wantAuth {
		t.Errorf("download request = %s auth=%q, want GET with the Basic header", dl.method, dl.auth)
	}
	if dl.accept != "" {
		t.Errorf("download Accept = %q, want empty (do not force JSON on a .torrent)", dl.accept)
	}
	// The download URL DOES carry the passkey (Prowlarr style) — it is only ever sent
	// server-side here; the result body must not echo it.
	if strings.Contains(string(res.Body), credPass) {
		t.Errorf("result body leaks the passkey: %q", res.Body)
	}
}

// TestGrabStatusErrors proves a 429 is a rate-limit error, a 403 is an auth failure,
// and another non-2xx is a plain error — none leaking the passkey.
func TestGrabStatusErrors(t *testing.T) {
	t.Parallel()
	mk := func(status int) *driver {
		d, _ := grabDriver(func(_ *stdhttp.Request, _ string) *stdhttp.Response {
			return resp(status, "nope")
		})
		return d
	}
	const link = "https://filelist.test/download.php?id=1&passkey=" + credPass

	_, err := mk(stdhttp.StatusTooManyRequests).Grab(context.Background(), link)
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Errorf("429: err = %v, want *search.RateLimitedError", err)
	}

	_, err = mk(stdhttp.StatusForbidden).Grab(context.Background(), link)
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("403: err = %v, want login.ErrLoginFailed", err)
	}

	_, err = mk(stdhttp.StatusInternalServerError).Grab(context.Background(), link)
	if err == nil {
		t.Fatal("500: want an error")
	}
	if strings.Contains(err.Error(), credPass) {
		t.Errorf("500 error leaks the passkey: %v", err)
	}
}

// authErrorDoer fails the download fetch with a transport error — the case where the
// download URL (which carries the passkey in its query) could leak through the wrapped
// error.
type authErrorDoer struct{ err error }

func (a *authErrorDoer) Do(_ *stdhttp.Request) (*stdhttp.Response, error) {
	return nil, a.err
}

// TestGrabTransportErrorSanitized proves a transport failure during the download fetch
// surfaces a fixed, passkey-free error even though the download URL carries the passkey
// in its query.
func TestGrabTransportErrorSanitized(t *testing.T) {
	t.Parallel()
	const link = "https://filelist.test/download.php?id=9&passkey=" + credPass
	d := liveDriver(nil)
	d.doer = &authErrorDoer{err: errors.New("connection refused")}

	_, err := d.Grab(context.Background(), link)
	if err == nil {
		t.Fatal("want a transport error")
	}
	if strings.Contains(err.Error(), credPass) || strings.Contains(err.Error(), link) {
		t.Errorf("download URL/passkey leaked into the error: %v", err)
	}
	if strings.Contains(apphttp.RedactError(err), credPass) {
		t.Errorf("RedactError leaks the passkey: %v", apphttp.RedactError(err))
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
