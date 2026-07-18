package download

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/download/sabnzbd"
)

// TestSabnzbdGPLHeaderPresent is a cheap standing guard on the porting manifest's
// attribution requirement (#241): fails if the header ever gets dropped/edited.
func TestSabnzbdGPLHeaderPresent(t *testing.T) {
	t.Parallel()
	assertGPLHeader(t, "sabnzbd/sabnzbd.go")
}

// sabnzbdStub is a minimal httptest stand-in for SABnzbd's API: mode/apikey/name/cat
// all ride as query params on a single GET /api endpoint, so the stub keys its
// response on the mode param and records the full query for assertions.
type sabnzbdStub struct {
	// wantInvalidBody simulates a rejected apikey the way a real SABnzbd instance
	// would for mode=version (no ApiError field on VersionResponse to carry a
	// structured error) — a body Version's ported client can't JSON-decode, which
	// is the only way its Test surfaces a rejection (the ported client never
	// checks HTTP status).
	wantInvalidBody bool
	// wantAPIError simulates a rejected apikey on mode=addurl, which DOES have an
	// ApiError-shaped response (AddFileResponse embeds it) the driver reads.
	wantAPIError bool
	lastQuery    url.Values
}

func newSabnzbdStub(t *testing.T, s *sabnzbdStub) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		s.lastQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		if s.wantInvalidBody {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("API Key Incorrect"))
			return
		}
		if s.wantAPIError {
			_, _ = w.Write([]byte(`{"error":"API Key Incorrect"}`))
			return
		}
		switch s.lastQuery.Get("mode") {
		case "version":
			_, _ = w.Write([]byte(`{"version":"4.3.0"}`))
		case "addurl":
			_, _ = w.Write([]byte(`{"nzo_ids":["SABnzbd_nzo_abc"]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestSabnzbdDriver(host, apikey, category string) *sabnzbdDriver {
	drv, _ := newSabnzbd(domain.DownloadClient{
		Host:     host,
		Settings: domain.DownloadClientSettings{Sabnzbd: &domain.SabnzbdSettings{Category: category}},
	}, apikey, nil)
	return drv.(*sabnzbdDriver)
}

func TestSabnzbdTest_OK(t *testing.T) {
	t.Parallel()
	stub := &sabnzbdStub{}
	srv := newSabnzbdStub(t, stub)
	drv := newTestSabnzbdDriver(srv.URL, "goodkey", "")
	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
}

// TestSabnzbdTest_TransportErrorRedactsAPIKey mirrors
// TestSabnzbdAdd_TransportErrorRedactsSecrets for Test: the version request URL
// carries the configured apikey as a query param, so a transport-level failure
// (a *url.Error) must not leak it either.
func TestSabnzbdTest_TransportErrorRedactsAPIKey(t *testing.T) {
	t.Parallel()
	stub := &sabnzbdStub{}
	srv := newSabnzbdStub(t, stub)
	const sabnzbdAPIKey = "SABAPIKEY0123456789ABC"
	drv := newTestSabnzbdDriver(srv.URL, sabnzbdAPIKey, "")
	srv.Close() // force a connection-refused transport error

	err := drv.Test(context.Background())
	if err == nil {
		t.Fatal("expected an error after closing the stub server")
	}
	if strings.Contains(err.Error(), sabnzbdAPIKey) {
		t.Fatalf("error leaks the configured sabnzbd apikey: %q", err)
	}
}

func TestSabnzbdTest_BadKey(t *testing.T) {
	t.Parallel()
	stub := &sabnzbdStub{wantInvalidBody: true}
	srv := newSabnzbdStub(t, stub)
	drv := newTestSabnzbdDriver(srv.URL, "wrongkey", "")
	if err := drv.Test(context.Background()); err == nil {
		t.Fatal("expected an error for a bad apikey")
	}
}

func TestSabnzbdAdd_ViaURL(t *testing.T) {
	t.Parallel()
	stub := &sabnzbdStub{}
	srv := newSabnzbdStub(t, stub)
	drv := newTestSabnzbdDriver(srv.URL, "goodkey", "")

	const nzbURL = "http://tracker.example/dl?token=sealed"
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: nzbURL}, AddOptions{Category: "tv"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := stub.lastQuery.Get("mode"); got != "addurl" {
		t.Errorf("mode = %q, want addurl", got)
	}
	if got := stub.lastQuery.Get("apikey"); got != "goodkey" {
		t.Errorf("apikey = %q, want goodkey", got)
	}
	if got := stub.lastQuery.Get("name"); got != nzbURL {
		t.Errorf("name = %q, want %q", got, nzbURL)
	}
	if got := stub.lastQuery.Get("cat"); got != "tv" {
		t.Errorf("cat = %q, want tv (explicit override)", got)
	}
}

func TestSabnzbdAdd_CategoryDefault(t *testing.T) {
	t.Parallel()
	stub := &sabnzbdStub{}
	srv := newSabnzbdStub(t, stub)
	drv := newTestSabnzbdDriver(srv.URL, "goodkey", "default-cat")

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: "http://x/n.nzb"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := stub.lastQuery.Get("cat"); got != "default-cat" {
		t.Errorf("cat = %q, want the settings default", got)
	}
}

func TestSabnzbdAdd_TorrentUnsupported(t *testing.T) {
	t.Parallel()
	stub := &sabnzbdStub{}
	srv := newSabnzbdStub(t, stub)
	drv := newTestSabnzbdDriver(srv.URL, "goodkey", "")

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{})
	if !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("Add(torrent) error = %v, want ErrUnsupportedProtocol", err)
	}
}

func TestSabnzbdAdd_BytesOnlyUnsupported(t *testing.T) {
	t.Parallel()
	stub := &sabnzbdStub{}
	srv := newSabnzbdStub(t, stub)
	drv := newTestSabnzbdDriver(srv.URL, "goodkey", "")

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, Bytes: []byte("nzb bytes")}, AddOptions{})
	if !errors.Is(err, ErrURLRequired) {
		t.Fatalf("Add(bytes-only) error = %v, want ErrURLRequired", err)
	}
}

// TestSabnzbdAdd_TransportErrorRedactsSecrets pins #241: a transport-level
// failure surfaces as a *url.Error whose .URL is the full, percent-encoded
// request URL — carrying both the configured SABnzbd apikey and, embedded in the
// "name" param, harbrr's own sealed-nzb-URL apikey. Neither may reach the
// returned error.
func TestSabnzbdAdd_TransportErrorRedactsSecrets(t *testing.T) {
	t.Parallel()
	stub := &sabnzbdStub{}
	srv := newSabnzbdStub(t, stub)
	const sabnzbdAPIKey = "SABAPIKEY0123456789ABC"
	drv := newTestSabnzbdDriver(srv.URL, sabnzbdAPIKey, "")
	srv.Close() // force a connection-refused transport error

	const harbrrAPIKey = "HARBRRAPIKEY0123456789XYZ"
	sealed := "http://harbrr.local/api/indexers/tt/dl?token=abc&apikey=" + harbrrAPIKey
	err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: sealed}, AddOptions{})
	if err == nil {
		t.Fatal("expected an error after closing the stub server")
	}
	if strings.Contains(err.Error(), harbrrAPIKey) {
		t.Fatalf("error leaks the sealed nzb URL's apikey: %q", err)
	}
	if strings.Contains(err.Error(), sabnzbdAPIKey) {
		t.Fatalf("error leaks the configured sabnzbd apikey: %q", err)
	}
}

func TestSabnzbdAdd_ReportedErrorSurfaced(t *testing.T) {
	t.Parallel()
	stub := &sabnzbdStub{wantAPIError: true}
	srv := newSabnzbdStub(t, stub)
	drv := newTestSabnzbdDriver(srv.URL, "wrongkey", "")

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: "http://x/n.nzb"}, AddOptions{})
	if err == nil {
		t.Fatal("expected an error when SABnzbd reports API Key Incorrect")
	}
}

// TestSabnzbdOptionsHTTPClientInjected proves the factory's shared *http.Client
// (when supplied) is what the ported client actually uses, not a client it built
// itself — the vehicle for #241's "shared Transport becomes the injected
// *http.Client" rewrite.
func TestSabnzbdOptionsHTTPClientInjected(t *testing.T) {
	t.Parallel()
	stub := &sabnzbdStub{}
	srv := newSabnzbdStub(t, stub)
	var used bool
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used = true
		return http.DefaultTransport.RoundTrip(r)
	})}
	drv, err := newSabnzbd(domain.DownloadClient{Host: srv.URL}, "k", client)
	if err != nil {
		t.Fatalf("newSabnzbd: %v", err)
	}
	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !used {
		t.Fatal("driver did not use the injected *http.Client")
	}
}

// sabnzbdClientPackageSmoke proves the ported sabnzbd.Client type is reachable
// with the expected constructor shape, guarding against an accidental rename
// during the port.
var _ = sabnzbd.Options{Addr: "", ApiKey: "", HTTPClient: nil}

// roundTripFunc adapts a func to http.RoundTripper for injection tests.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
