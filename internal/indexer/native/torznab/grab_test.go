package torznab

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// grabURL is a synthetic download URL embedding synthetic authkey/torrent_pass
// credentials, to prove neither secret nor the URL surfaces in a grab error.
const grabURL = "https://torznab.example.test/torrents.php?action=download&id=1&authkey=" + testAPIKey + "&torrent_pass=SYNTHPASS"

func grabDriver(t *testing.T, doer search.Doer) *driver {
	t.Helper()
	d, err := New(native.Params{
		Def:     presetDefinition(presets[0]),
		Cfg:     map[string]string{"apikey": testAPIKey},
		Doer:    doer,
		BaseURL: "https://torznab.example.test",
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

// TestGrabReturnsTorrentBody proves Grab GETs the credentialed download URL
// server-side and returns the body with the upstream Content-Type and NO Redirect (a
// credentialed URL must never be a redirect).
func TestGrabReturnsTorrentBody(t *testing.T) {
	t.Parallel()
	const torrentBytes = "d8:announce...e" // not a real bencoded torrent, just fixture bytes
	var sawURL string
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		sawURL = r.URL.String()
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = io.WriteString(w, torrentBytes)
	}))
	t.Cleanup(srv.Close)
	d := grabDriver(t, srv.Client())

	link := srv.URL + "/torrents.php?action=download&id=1&authkey=" + testAPIKey + "&torrent_pass=SYNTHPASS"
	res, err := d.Grab(context.Background(), link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(res.Body) != torrentBytes {
		t.Errorf("body mismatch:\n got %q", res.Body)
	}
	if res.ContentType != "application/x-bittorrent" {
		t.Errorf("ContentType = %q, want application/x-bittorrent", res.ContentType)
	}
	if res.Redirect != "" {
		t.Errorf("Redirect = %q, want empty (a credentialed URL must never redirect)", res.Redirect)
	}
	if !strings.Contains(sawURL, "authkey="+testAPIKey) {
		t.Errorf("server did not receive the authkey; saw %q", redact(sawURL))
	}
	assertNoAPIKey(t, "grab body", string(res.Body))
}

// TestGrabTransportErrorSurfacesHostOnly proves a real transport failure — a
// *url.Error whose Error() echoes the FULL credentialed URL — surfaces only its
// scheme://host.
func TestGrabTransportErrorSurfacesHostOnly(t *testing.T) {
	t.Parallel()
	uerr := &url.Error{
		Op:  "Get",
		URL: grabURL,
		Err: errors.New("dial tcp: connection refused"),
	}
	d := grabDriver(t, &errorDoer{err: uerr})
	_, err := d.Grab(context.Background(), grabURL)
	if err == nil {
		t.Fatal("Grab err = nil, want a transport error")
	}
	got := err.Error()
	if !strings.Contains(got, "https://torznab.example.test") {
		t.Errorf("err = %q, want it to surface scheme://host", got)
	}
	assertNoAPIKey(t, "grab transport error", got)
	if strings.Contains(got, "torrent_pass=SYNTHPASS") {
		t.Errorf("err = %q leaks the torrent_pass query", got)
	}
}

// TestGrabUnauthorized proves a 401 on the download surfaces as a login failure.
func TestGrabUnauthorized(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(*stdhttp.Request) *stdhttp.Response {
		return &stdhttp.Response{StatusCode: stdhttp.StatusUnauthorized, Body: io.NopCloser(strings.NewReader("no")), Header: stdhttp.Header{}}
	}}
	d := grabDriver(t, doer)
	_, err := d.Grab(context.Background(), grabURL)
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
	assertNoAPIKey(t, "grab 401 error", err.Error())
}
