package newznab

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

// grabURL is a synthetic .nzb download URL embedding the synthetic apikey, to prove neither
// the apikey nor the URL surfaces in a grab error or the returned result.
const grabURL = "https://news.example.test/getnzb/abc123.nzb?r=" + testAPIKey

// grabDriver wires a driver to the given doer (the cfg apikey matters only for redaction
// coverage; the grab URL already carries its own apikey).
func grabDriver(t *testing.T, doer search.Doer) *driver {
	t.Helper()
	d, err := New(native.Params{
		Def:     GenericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey},
		Doer:    doer,
		BaseURL: "https://news.example.test",
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

// TestGrabReturnsNZBBody proves Grab GETs the .nzb URL server-side and returns the body with
// ContentType application/x-nzb and NO Redirect (an apikey-bearing URL must never be a
// redirect). The apikey reaches the server query but appears in no returned string.
func TestGrabReturnsNZBBody(t *testing.T) {
	t.Parallel()
	nzb := string(readGolden(t, "sample.nzb"))
	var sawURL string
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		sawURL = r.URL.String()
		w.Header().Set("Content-Type", "application/x-nzb")
		_, _ = io.WriteString(w, nzb)
	}))
	t.Cleanup(srv.Close)
	d := grabDriver(t, srv.Client())

	link := srv.URL + "/getnzb/abc123.nzb?r=" + testAPIKey
	res, err := d.Grab(context.Background(), link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(res.Body) != nzb {
		t.Errorf("body mismatch:\n got %q", res.Body)
	}
	if res.ContentType != "application/x-nzb" {
		t.Errorf("ContentType = %q, want application/x-nzb", res.ContentType)
	}
	if res.Redirect != "" {
		t.Errorf("Redirect = %q, want empty (apikey URL must never redirect)", res.Redirect)
	}
	if !strings.Contains(sawURL, "r="+testAPIKey) {
		t.Errorf("server did not receive the apikey; saw %q", redact(sawURL))
	}
	// The served body is the verbatim .nzb — it must not have harbrr's apikey injected.
	assertNoApikey(t, "grab body", string(res.Body))
}

// TestGrabTransportErrorSurfacesHostOnly proves a real transport failure — a *url.Error, as
// http.Client.Do returns, whose Error() echoes the FULL apikey-bearing URL (secret in both a
// path segment and a query param) — surfaces only its scheme://host. The apikey-bearing
// path/query is dropped, the enriched error still errors.Is the sentinel, and it still carries
// the "download request failed" substring.
func TestGrabTransportErrorSurfacesHostOnly(t *testing.T) {
	t.Parallel()
	// Secret in a PATH segment (/getnzb/<secret>) AND a query param (r=<secret>).
	leakURL := "https://news.example.test/getnzb/" + testAPIKey + "?r=" + testAPIKey
	uerr := &url.Error{
		Op:  "Get",
		URL: leakURL,
		Err: errors.New("dial tcp: connection refused"),
	}
	d := grabDriver(t, &errorDoer{err: uerr})
	_, err := d.Grab(context.Background(), leakURL)
	if err == nil {
		t.Fatal("Grab err = nil, want a transport error")
	}
	got := err.Error()
	// The host now surfaces for diagnosis — it is not a secret.
	if !strings.Contains(got, "https://news.example.test") {
		t.Errorf("err = %q, want it to surface scheme://host", got)
	}
	// The apikey and its path/query carriers must be gone.
	assertNoApikey(t, "grab transport error", got)
	if strings.Contains(got, "/getnzb/"+testAPIKey) || strings.Contains(got, "r="+testAPIKey) {
		t.Errorf("err = %q leaks the apikey-bearing path/query", got)
	}
	// The sentinel identity and its message must survive the host-only enrichment.
	if !errors.Is(err, errDownloadRequestFailed) {
		t.Errorf("err = %q, want errors.Is(errDownloadRequestFailed)", got)
	}
	if !strings.Contains(got, "newznab: download request failed") {
		t.Errorf("err = %q, want the download-request-failed message", got)
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
	assertNoApikey(t, "grab 401 error", err.Error())
}
