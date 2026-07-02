package broadcastthenet

import (
	"context"
	"errors"
	stdhttp "net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// grabURL is a synthetic BTN download URL: it embeds a fake authkey/torrent_pass to
// prove neither value (nor the URL itself) ever surfaces in a grab error. The synthetic
// secrets live only in this test file.
const grabURL = "https://broadcasthe.net/torrents.php?action=download&id=1555073&authkey=SYNTHETICKEY1&torrent_pass=SYNTHETICPASS1"

// errorDoer fails every request with a transport error that echoes the URL, so the test
// can prove the grab error never leaks the credential-bearing link.
type errorDoer struct{ err error }

func (e *errorDoer) Do(*stdhttp.Request) (*stdhttp.Response, error) { return nil, e.err }

// grabDriver wires a driver to a doer for the grab tests.
func grabDriver(t *testing.T, doer interface {
	Do(*stdhttp.Request) (*stdhttp.Response, error)
},
) *driver {
	t.Helper()
	def := Families()[0].Definition
	d, err := New(native.Params{
		Def:   def,
		Cfg:   map[string]string{"apikey": credAPIKey},
		Doer:  doer,
		Clock: fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

// TestGrabReturnsTorrentBytes proves Grab GETs the download URL server-side and returns
// the torrent body and Content-Type, with no API key sent (the URL carries its own
// authkey/torrent_pass) and no Redirect (BTN serves a direct .torrent).
func TestGrabReturnsTorrentBytes(t *testing.T) {
	t.Parallel()
	const payload = "d8:announce..fake torrent.."
	doer := &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		resp := mkResp(stdhttp.StatusOK, payload)
		resp.Header.Set("Content-Type", "application/x-bittorrent")
		return resp
	}}
	d := grabDriver(t, doer)

	res, err := d.Grab(context.Background(), grabURL)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(res.Body) != payload {
		t.Errorf("body = %q, want the torrent payload", res.Body)
	}
	if res.ContentType != "application/x-bittorrent" {
		t.Errorf("ContentType = %q, want application/x-bittorrent", res.ContentType)
	}
	if res.Redirect != "" {
		t.Errorf("Redirect = %q, want empty (BTN serves a direct torrent)", res.Redirect)
	}
	if len(doer.reqs) != 1 || doer.reqs[0].method != stdhttp.MethodGet {
		t.Fatalf("requests = %v, want one GET", doer.reqs)
	}
	if doer.reqs[0].url != grabURL {
		t.Errorf("url = %q, want the download URL", doer.reqs[0].url)
	}
}

// TestGrabTransportErrorNeverLeaksURL proves a transport error from the download fetch
// is sanitized to a fixed message that carries neither the URL nor the embedded
// authkey/torrent_pass.
func TestGrabTransportErrorNeverLeaksURL(t *testing.T) {
	t.Parallel()
	// The transport error echoes the full URL (incl. the secrets) to simulate a hostile
	// or verbose error; the sanitizer must drop all of it.
	d := grabDriver(t, &errorDoer{err: errors.New("dial tcp: " + grabURL)})

	_, err := d.Grab(context.Background(), grabURL)
	if err == nil {
		t.Fatal("Grab should error on a transport failure")
	}
	msg := err.Error()
	for _, leak := range []string{grabURL, "SYNTHETICKEY1", "SYNTHETICPASS1", "broadcasthe.net"} {
		if strings.Contains(msg, leak) {
			t.Errorf("grab error leaks %q: %q", leak, msg)
		}
	}
}

// TestGrabContextErrorPassesThrough proves a cancellation/deadline from the fetch is
// preserved (not flattened into the generic "download request failed"), so callers and
// health classification can tell a cancelled request from a real failure.
func TestGrabContextErrorPassesThrough(t *testing.T) {
	t.Parallel()
	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		d := grabDriver(t, &errorDoer{err: want})
		_, err := d.Grab(context.Background(), grabURL)
		if !errors.Is(err, want) {
			t.Errorf("Grab err = %v, want errors.Is %v", err, want)
		}
	}
}

// TestGrabStatusDispatch proves a 401/403 download response maps to login.ErrLoginFailed
// (so the registry records an auth_failure health event).
func TestGrabStatusDispatch(t *testing.T) {
	t.Parallel()
	for _, status := range []int{stdhttp.StatusUnauthorized, stdhttp.StatusForbidden} {
		doer := &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
			return mkResp(status, "nope")
		}}
		_, err := grabDriver(t, doer).Grab(context.Background(), grabURL)
		if !errors.Is(err, login.ErrLoginFailed) {
			t.Errorf("HTTP %d: err = %v, want login.ErrLoginFailed", status, err)
		}
	}
}

// TestTestActionSurfacesBadKey proves Test() surfaces an auth failure when the empty
// browse query returns the -32001 ("Invalid API Key") JSON-RPC error envelope.
func TestTestActionSurfacesBadKey(t *testing.T) {
	t.Parallel()
	d := grabDriver(t, &scriptDoer{handler: func(_ *stdhttp.Request, _ string) *stdhttp.Response {
		return mkResp(stdhttp.StatusOK, `{"result":null,"error":{"code":-32001,"message":"Invalid API Key"}}`)
	}})
	if err := d.Test(context.Background()); !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("Test on -32001 = %v, want login.ErrLoginFailed", err)
	}
}
