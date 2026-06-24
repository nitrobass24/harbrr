package passthepopcorn

import (
	"context"
	"errors"
	stdhttp "net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// torrentBytes is a minimal bencoded payload: a bencoded dict starts with 'd'. PTP serves a
// direct .torrent; the Grab returns it verbatim.
const torrentBytes = "d8:announce11:fake-tracker4:infod6:lengthi1ee"

// errDoer fails every request with a transport error that echoes the URL, so a test can
// prove the grab error never leaks the link.
type errDoer struct{ err error }

func (e *errDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

// torrentResp builds a 200 response with a bittorrent Content-Type.
func torrentResp(body string) *stdhttp.Response {
	return rawResp(stdhttp.StatusOK, "application/x-bittorrent", body)
}

// TestGrabReturnsTorrentBytes proves Grab GETs the download URL server-side with the two
// credential headers and returns the torrent body and Content-Type — and that neither
// credential rides in the URL (the link is secret-free; auth is the headers).
func TestGrabReturnsTorrentBytes(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{resp: torrentResp(torrentBytes)}
	d := searchDriver(t, doer)

	link := d.downloadLink(12345)
	res, err := d.Grab(context.Background(), link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(res.Body) != torrentBytes {
		t.Errorf("Body = %q, want the torrent payload", res.Body)
	}
	if res.ContentType != "application/x-bittorrent" {
		t.Errorf("ContentType = %q, want application/x-bittorrent", res.ContentType)
	}
	if res.Redirect != "" {
		t.Errorf("Redirect = %q, want empty (direct torrent)", res.Redirect)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want exactly one", len(doer.reqs))
	}
	got := doer.reqs[0]
	if got.method != stdhttp.MethodGet || got.url != link {
		t.Errorf("request = %s %s, want GET %s", got.method, got.url, link)
	}
	if got.apiUser != credAPIUser || got.apiKey != credAPIKey {
		t.Errorf("credential headers = (%q,%q), want both secrets attached", got.apiUser, got.apiKey)
	}
	if strings.Contains(got.url, credAPIUser) || strings.Contains(got.url, credAPIKey) {
		t.Errorf("URL leaks a credential: %q", got.url)
	}
}

// TestGrabTransportErrorNeverLeaksURLOrSecret proves a transport error is sanitized to a
// fixed message carrying neither the download URL, the host, nor either credential.
func TestGrabTransportErrorNeverLeaksURLOrSecret(t *testing.T) {
	t.Parallel()
	link := "https://passthepopcorn.me/torrents.php?action=download&id=12345"
	d := searchDriver(t, &errDoer{err: errors.New("dial tcp " + link + " user=" + credAPIUser)})

	_, err := d.Grab(context.Background(), link)
	if err == nil {
		t.Fatal("Grab should error on a transport failure")
	}
	for _, leak := range []string{link, "passthepopcorn.me", "id=12345", credAPIUser, credAPIKey} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("grab error leaks %q: %q", leak, err.Error())
		}
	}
}

// TestGrabContextErrorPassesThrough proves a cancellation/deadline is preserved (not
// flattened to the generic failure), so health classification can distinguish it.
func TestGrabContextErrorPassesThrough(t *testing.T) {
	t.Parallel()
	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		d := searchDriver(t, &errDoer{err: want})
		_, err := d.Grab(context.Background(), "https://passthepopcorn.me/torrents.php?action=download&id=1")
		if !errors.Is(err, want) {
			t.Errorf("Grab err = %v, want errors.Is %v", err, want)
		}
	}
}

// TestGrabStatusDispatch proves a 401/403 download response maps to login.ErrLoginFailed
// (and never leaks a credential), and that a rate-limit status surfaces a RateLimitedError.
func TestGrabStatusDispatch(t *testing.T) {
	t.Parallel()
	for _, status := range []int{stdhttp.StatusUnauthorized, stdhttp.StatusForbidden} {
		d := searchDriver(t, &scriptDoer{resp: rawResp(status, "text/html", "nope")})
		_, err := d.Grab(context.Background(), "https://passthepopcorn.me/torrents.php?action=download&id=1")
		if !errors.Is(err, login.ErrLoginFailed) {
			t.Errorf("HTTP %d: err = %v, want login.ErrLoginFailed", status, err)
		}
		if err != nil && (strings.Contains(err.Error(), credAPIUser) || strings.Contains(err.Error(), credAPIKey)) {
			t.Errorf("HTTP %d: error leaked a credential: %v", status, err)
		}
	}

	d := searchDriver(t, &scriptDoer{resp: rawResp(stdhttp.StatusTooManyRequests, "text/html", "slow down")})
	_, err := d.Grab(context.Background(), "https://passthepopcorn.me/torrents.php?action=download&id=1")
	var rl *search.RateLimitedError
	if !errors.As(err, &rl) {
		t.Errorf("HTTP 429: err = %v, want *search.RateLimitedError", err)
	}
}

// TestGrabRejectsOversizeBody proves a body past the cap errors (a truncated .torrent is
// corrupt) rather than returning a silently truncated payload.
func TestGrabRejectsOversizeBody(t *testing.T) {
	t.Parallel()
	got, err := readTorrent(strings.NewReader(strings.Repeat("x", 17)), 16)
	if !errors.Is(err, errDownloadTooLarge) {
		t.Fatalf("readTorrent err = %v, want errDownloadTooLarge", err)
	}
	if got != nil {
		t.Errorf("oversize body returned %d bytes, want nil", len(got))
	}
}

// TestTestSurfacesAuthFailure proves Test() runs the empty browse and surfaces a 401 as
// login.ErrLoginFailed (the registry records an auth_failure health event).
func TestTestSurfacesAuthFailure(t *testing.T) {
	t.Parallel()
	d := searchDriver(t, &scriptDoer{resp: jsonResp(stdhttp.StatusUnauthorized, `{}`)})
	if err := d.Test(context.Background()); !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("Test on 401 = %v, want login.ErrLoginFailed", err)
	}
}

// TestTestSucceedsOnEmptyBrowse proves Test() passes when the empty browse returns a
// parseable empty page (TotalResults "0", no movies).
func TestTestSucceedsOnEmptyBrowse(t *testing.T) {
	t.Parallel()
	d := searchDriver(t, &scriptDoer{resp: jsonResp(stdhttp.StatusOK, `{"TotalResults":"0","Movies":[]}`)})
	if err := d.Test(context.Background()); err != nil {
		t.Errorf("Test on empty browse = %v, want nil", err)
	}
}
