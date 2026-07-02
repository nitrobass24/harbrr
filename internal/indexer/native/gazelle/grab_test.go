package gazelle

import (
	"context"
	"errors"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// torrentBytes is a minimal bencoded payload: a bencoded dict starts with 'd', which is
// the driver's bencoded-vs-HTML test.
const torrentBytes = "d8:announce11:fake-tracker4:infod6:lengthi1ee"

// seqDoer serves a queued sequence of responses (one per request) and records each
// request's URL and Authorization header. It lets the freeleech-fallback test assert the
// first call carries usetoken=1 and the second drops it.
type seqDoer struct {
	resps []*stdhttp.Response
	reqs  []recordedReq
}

func (s *seqDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	s.reqs = append(s.reqs, recordedReq{
		method:        req.Method,
		url:           req.URL.String(),
		authorization: req.Header.Get("Authorization"),
		accept:        req.Header.Get("Accept"),
	})
	resp := s.resps[len(s.reqs)-1]
	return resp, nil
}

// errDoer fails every request with a transport error that echoes the URL, so the test can
// prove the grab error never leaks the link.
type errDoer struct{ err error }

func (e *errDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

// grabDriver wires a driver for one site to a doer with the synthetic apikey and the given
// settings.
func grabDriver(t *testing.T, id string, cfg map[string]string, doer search.Doer) *driver {
	t.Helper()
	def := familyByID(t, id).Definition
	d, err := New(native.Params{
		Def:   def,
		Cfg:   cfg,
		Doer:  doer,
		Clock: func() time.Time { return fixedClock },
	})
	if err != nil {
		t.Fatalf("New(%q): %v", id, err)
	}
	return d.(*driver)
}

func mkTorrentResp(body string) *stdhttp.Response {
	resp := mkResp(stdhttp.StatusOK, body)
	resp.Header.Set("Content-Type", "application/x-bittorrent")
	return resp
}

// TestGrabReturnsTorrentBytes proves Grab GETs the download URL server-side with the
// Authorization header and returns the torrent body and Content-Type — and that the apikey
// rides only in the header, never in the URL.
func TestGrabReturnsTorrentBytes(t *testing.T) {
	t.Parallel()
	doer := &seqDoer{resps: []*stdhttp.Response{mkTorrentResp(torrentBytes)}}
	d := grabDriver(t, "redacted", map[string]string{"apikey": credAPIKey}, doer)

	link := d.downloadLink(12345, false)
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
	if got.authorization != credAPIKey {
		t.Errorf("Authorization = %q, want the bare RED apikey", got.authorization)
	}
	if strings.Contains(got.url, credAPIKey) {
		t.Errorf("URL leaks the apikey: %q", got.url)
	}
}

// TestGrabOPSAuthHeader proves the OPS grab carries the "token "-prefixed apikey on the
// download request.
func TestGrabOPSAuthHeader(t *testing.T) {
	t.Parallel()
	doer := &seqDoer{resps: []*stdhttp.Response{mkTorrentResp(torrentBytes)}}
	d := grabDriver(t, "orpheus", map[string]string{"apikey": credAPIKey}, doer)

	if _, err := d.Grab(context.Background(), d.downloadLink(7, false)); err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if got := doer.reqs[0].authorization; got != "token "+credAPIKey {
		t.Errorf("Authorization = %q, want the OPS token-prefixed apikey", got)
	}
}

// TestGrabFreeleechRetryWithoutToken proves Prowlarr's freeleech fallback: a usetoken=1
// link whose first response is not a bencoded torrent (an HTML "no tokens left" page)
// triggers a second fetch of the SAME id with usetoken stripped, and that torrent is
// returned. OPS never sees usetoken=0 — the param is removed, not zeroed.
func TestGrabFreeleechRetryWithoutToken(t *testing.T) {
	t.Parallel()
	const htmlPage = "<html>You do not have any freeleech tokens left.</html>"
	doer := &seqDoer{resps: []*stdhttp.Response{
		mkResp(stdhttp.StatusOK, htmlPage),
		mkTorrentResp(torrentBytes),
	}}
	cfg := map[string]string{"apikey": credAPIKey, "use_freeleech_token": "true"}
	d := grabDriver(t, "redacted", cfg, doer)

	link := d.downloadLink(12345, true)
	res, err := d.Grab(context.Background(), link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(res.Body) != torrentBytes {
		t.Errorf("Body = %q, want the torrent from the retry", res.Body)
	}
	if len(doer.reqs) != 2 {
		t.Fatalf("requests = %d, want two (token then fallback)", len(doer.reqs))
	}
	if !strings.Contains(doer.reqs[0].url, "usetoken=1") {
		t.Errorf("first request = %q, want usetoken=1", doer.reqs[0].url)
	}
	if strings.Contains(doer.reqs[1].url, "usetoken") {
		t.Errorf("retry request = %q, want usetoken removed entirely", doer.reqs[1].url)
	}
	if want := d.downloadLink(12345, false); doer.reqs[1].url != want {
		t.Errorf("retry url = %q, want %q (same id, no token)", doer.reqs[1].url, want)
	}
}

// TestGrabNoRetryWhenTorrent proves a usetoken=1 link whose first response IS a bencoded
// torrent is returned as-is, with no second fetch.
func TestGrabNoRetryWhenTorrent(t *testing.T) {
	t.Parallel()
	doer := &seqDoer{resps: []*stdhttp.Response{mkTorrentResp(torrentBytes)}}
	cfg := map[string]string{"apikey": credAPIKey, "use_freeleech_token": "true"}
	d := grabDriver(t, "redacted", cfg, doer)

	if _, err := d.Grab(context.Background(), d.downloadLink(12345, true)); err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want exactly one (no fallback)", len(doer.reqs))
	}
}

// TestGrabNoRetryWhenTokenDisabled proves a non-torrent first body does NOT trigger the
// fallback when the freeleech-token setting is off (the link would not carry usetoken
// anyway, but the guard must also short-circuit on the setting).
func TestGrabNoRetryWhenTokenDisabled(t *testing.T) {
	t.Parallel()
	doer := &seqDoer{resps: []*stdhttp.Response{mkResp(stdhttp.StatusOK, "<html>nope</html>")}}
	d := grabDriver(t, "redacted", map[string]string{"apikey": credAPIKey}, doer)

	// A usetoken=1 link with the setting off should not retry — the setting gates it.
	if _, err := d.Grab(context.Background(), d.downloadLink(12345, true)); err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want one (no fallback when setting off)", len(doer.reqs))
	}
}

// TestGrabTransportErrorNeverLeaksURL proves a transport error is sanitized to a fixed
// message carrying neither the download URL nor the host.
func TestGrabTransportErrorNeverLeaksURL(t *testing.T) {
	t.Parallel()
	link := "https://redacted.sh/ajax.php?action=download&id=12345"
	d := grabDriver(t, "redacted", map[string]string{"apikey": credAPIKey},
		&errDoer{err: errors.New("dial tcp: " + link)})

	_, err := d.Grab(context.Background(), link)
	if err == nil {
		t.Fatal("Grab should error on a transport failure")
	}
	for _, leak := range []string{link, "redacted.sh", "id=12345", credAPIKey} {
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
		d := grabDriver(t, "redacted", map[string]string{"apikey": credAPIKey}, &errDoer{err: want})
		_, err := d.Grab(context.Background(), "https://redacted.sh/ajax.php?action=download&id=1")
		if !errors.Is(err, want) {
			t.Errorf("Grab err = %v, want errors.Is %v", err, want)
		}
	}
}

// TestGrabStatusDispatch proves a 401/403 download response maps to login.ErrLoginFailed.
func TestGrabStatusDispatch(t *testing.T) {
	t.Parallel()
	for _, status := range []int{stdhttp.StatusUnauthorized, stdhttp.StatusForbidden} {
		doer := &seqDoer{resps: []*stdhttp.Response{mkResp(status, "nope")}}
		d := grabDriver(t, "redacted", map[string]string{"apikey": credAPIKey}, doer)
		_, err := d.Grab(context.Background(), "https://redacted.sh/ajax.php?action=download&id=1")
		if !errors.Is(err, login.ErrLoginFailed) {
			t.Errorf("HTTP %d: err = %v, want login.ErrLoginFailed", status, err)
		}
	}
}

// TestTestSurfacesAuthFailure proves Test() runs the empty browse and surfaces a 401 as
// login.ErrLoginFailed (the registry records an auth_failure health event).
func TestTestSurfacesAuthFailure(t *testing.T) {
	t.Parallel()
	doer := &seqDoer{resps: []*stdhttp.Response{mkResp(stdhttp.StatusUnauthorized, "bad key")}}
	d := grabDriver(t, "redacted", map[string]string{"apikey": credAPIKey}, doer)
	if err := d.Test(context.Background()); !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("Test on 401 = %v, want login.ErrLoginFailed", err)
	}
}

// TestTestSucceedsOnEmptyBrowse proves Test() passes when the empty browse returns a
// parseable empty page.
func TestTestSucceedsOnEmptyBrowse(t *testing.T) {
	t.Parallel()
	doer := &seqDoer{resps: []*stdhttp.Response{
		mkResp(stdhttp.StatusOK, `{"status":"success","response":{"results":[],"currentPage":"1","pages":"1"}}`),
	}}
	d := grabDriver(t, "redacted", map[string]string{"apikey": credAPIKey}, doer)
	if err := d.Test(context.Background()); err != nil {
		t.Errorf("Test on empty browse = %v, want nil", err)
	}
}
