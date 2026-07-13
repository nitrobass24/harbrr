package myanonamouse

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
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// fakeTorrent is a minimal bencode-shaped body (content is irrelevant to the driver;
// it is returned verbatim to the /dl proxy).
const fakeTorrent = "d8:announce19:https://mam.test/an4:infod6:lengthi1e4:name4:fileee"

// TestGrab proves the download is fetched with the mam_id Cookie and returned as a
// direct torrent (no redirect), and that the mam_id never leaks into the result.
func TestGrab(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
		r := resp(stdhttp.StatusOK, fakeTorrent)
		r.Header.Set("Content-Type", "application/x-bittorrent")
		return r
	}}
	d := newDriver(doer)

	res, err := d.Grab(context.Background(), "https://mam.test/tor/download.php/DLHASH-AAAA?tid=101")
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
	if dl.method != stdhttp.MethodGet || dl.cookie != "mam_id="+mamSecret {
		t.Errorf("download request = %s cookie=%q, want GET with the mam_id", dl.method, dl.cookie)
	}
	if dl.accept != "" {
		t.Errorf("download Accept = %q, want empty (do not force JSON on a .torrent)", dl.accept)
	}
	assertNoSecret(t, dl.url)
	assertNoSecret(t, string(res.Body))
}

// TestGrabStatusErrors proves a 429 is a rate-limit error, a 403 is an auth failure,
// and another non-2xx is a plain error — none leaking the mam_id.
func TestGrabStatusErrors(t *testing.T) {
	t.Parallel()
	mk := func(status int) *driver {
		return newDriver(&scriptDoer{handler: func(_ *stdhttp.Request) *stdhttp.Response {
			return resp(status, "nope")
		}})
	}

	_, err := mk(stdhttp.StatusTooManyRequests).Grab(context.Background(), "https://mam.test/dl/1")
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Errorf("429: err = %v, want *search.RateLimitedError", err)
	}

	_, err = mk(stdhttp.StatusForbidden).Grab(context.Background(), "https://mam.test/dl/1")
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("403: err = %v, want login.ErrLoginFailed", err)
	}

	_, err = mk(stdhttp.StatusInternalServerError).Grab(context.Background(), "https://mam.test/dl/1")
	if err == nil {
		t.Fatal("500: want an error")
	}
	assertNoSecret(t, err.Error())
	assertNoSecret(t, apphttp.RedactError(err))
}

// errorDoer fails the download fetch with a transport error — the case where get would
// wrap the download URL.
type errorDoer struct{ err error }

func (e *errorDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

// TestGrabTransportErrorSanitized proves a transport failure during the download fetch
// surfaces the download endpoint's scheme://host (the host is not a secret) while the
// secret-bearing PATH segment and query param of the download URL never leak — even though
// http.Client.Do returns a *url.Error whose Error() embeds the FULL URL.
func TestGrabTransportErrorSanitized(t *testing.T) {
	t.Parallel()
	const secret = "DLPATH-SECRET-zzzz"
	const base = "https://www.myanonamouse.net"
	link := base + "/dl/" + secret + "?passkey=" + secret
	d := &driver{
		Base: native.Base{
			Family:  "myanonamouse",
			Cfg:     map[string]string{"mam_id": mamSecret},
			Doer:    &errorDoer{err: &url.Error{Op: "Get", URL: link, Err: errors.New("dial tcp: connection refused")}},
			BaseURL: base + "/",
			Clock:   fixedClock,
		},
		currentMamID: mamSecret,
	}
	_, err := d.Grab(context.Background(), link)
	if err == nil {
		t.Fatal("want a transport error")
	}
	got := err.Error()
	if !strings.Contains(got, base) {
		t.Errorf("error should surface the download host %q (the host is not a secret): %v", base, got)
	}
	if strings.Contains(got, secret) || strings.Contains(got, "/dl/"+secret) || strings.Contains(got, "passkey="+secret) {
		t.Errorf("download URL secret leaked into the error: %v", err)
	}
	if !strings.Contains(got, "myanonamouse: download") {
		t.Errorf("error should carry the family/download prefix: %v", err)
	}
	assertNoSecret(t, got)
}
