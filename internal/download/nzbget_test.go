package download

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/download/nzbget"
)

// TestNZBGetGPLHeaderPresent is a cheap standing guard on the porting manifest's
// attribution requirement (#241): fails if the header ever gets dropped/edited.
func TestNZBGetGPLHeaderPresent(t *testing.T) {
	t.Parallel()
	assertGPLHeader(t, "nzbget/nzbget.go")
}

// nzbgetRPCRequest mirrors the ported client's unexported rpcRequest shape, just
// enough to decode what the stub receives.
type nzbgetRPCRequest struct {
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
	ID     int           `json:"id"`
}

// nzbgetStub is a minimal httptest stand-in for NZBGet's single JSON-RPC
// endpoint: it keys its response on the RPC method and records the request
// (method, positional params, HTTP Basic auth) so a test can assert on them.
type nzbgetStub struct {
	wantRPCError bool
	lastMethod   string
	lastParams   []interface{}
	lastUser     string
	lastPass     string
	lastAuthOK   bool
}

func newNZBGetStub(t *testing.T, s *nzbgetStub) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/jsonrpc", func(w http.ResponseWriter, r *http.Request) {
		s.lastUser, s.lastPass, s.lastAuthOK = r.BasicAuth()
		var req nzbgetRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode rpc request: %v", err)
		}
		s.lastMethod, s.lastParams = req.Method, req.Params

		w.Header().Set("Content-Type", "application/json")
		if s.wantRPCError {
			_, _ = w.Write([]byte(`{"error":{"code":1,"message":"Authentication failed"}}`))
			return
		}
		switch req.Method {
		case "version":
			_, _ = w.Write([]byte(`{"result":"21.1"}`))
		case "append":
			_, _ = w.Write([]byte(`{"result":42}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestNZBGetDriver(host, username, password, category string) *nzbgetDriver {
	drv, _ := newNZBGet(domain.DownloadClient{
		Host:     host,
		Username: username,
		Settings: domain.DownloadClientSettings{NZBGet: &domain.NZBGetSettings{Category: category}},
	}, password, nil)
	return drv.(*nzbgetDriver)
}

func TestNZBGetTest_OK(t *testing.T) {
	t.Parallel()
	stub := &nzbgetStub{}
	srv := newNZBGetStub(t, stub)
	drv := newTestNZBGetDriver(srv.URL, "nzbget", "tegbzn6789", "")
	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if stub.lastMethod != "version" {
		t.Errorf("method = %q, want version", stub.lastMethod)
	}
}

func TestNZBGetTest_RPCError(t *testing.T) {
	t.Parallel()
	stub := &nzbgetStub{wantRPCError: true}
	srv := newNZBGetStub(t, stub)
	drv := newTestNZBGetDriver(srv.URL, "nzbget", "wrong", "")
	if err := drv.Test(context.Background()); err == nil {
		t.Fatal("expected an error for an RPC-level auth failure")
	}
}

func TestNZBGetAdd_ViaURL(t *testing.T) {
	t.Parallel()
	stub := &nzbgetStub{}
	srv := newNZBGetStub(t, stub)
	drv := newTestNZBGetDriver(srv.URL, "nzbget", "tegbzn6789", "")

	const nzbURL = "http://tracker.example/dl?token=sealed"
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: nzbURL}, AddOptions{Category: "tv"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if stub.lastMethod != "append" {
		t.Fatalf("method = %q, want append", stub.lastMethod)
	}
	if !stub.lastAuthOK || stub.lastUser != "nzbget" || stub.lastPass != "tegbzn6789" {
		t.Fatalf("basic auth = (%q, %q, ok=%v), want (nzbget, tegbzn6789, true)", stub.lastUser, stub.lastPass, stub.lastAuthOK)
	}
	if len(stub.lastParams) != 10 {
		t.Fatalf("append params length = %d, want 10 (Filename,URL,Category,Priority,AddToTop,AddPaused,DupeKey,DupeScore,DupeMode,PPParameters)", len(stub.lastParams))
	}
	if got, _ := stub.lastParams[1].(string); got != nzbURL {
		t.Errorf("params[1] (URL) = %q, want %q", got, nzbURL)
	}
	if got, _ := stub.lastParams[2].(string); got != "tv" {
		t.Errorf("params[2] (Category) = %q, want tv (explicit override)", got)
	}
	if got, _ := stub.lastParams[3].(float64); got != 0 {
		t.Errorf("params[3] (Priority) = %v, want 0 (hardcoded)", got)
	}
	if got, _ := stub.lastParams[5].(bool); got != false {
		t.Errorf("params[5] (AddPaused) = %v, want false (hardcoded)", got)
	}
	if got, _ := stub.lastParams[8].(string); got != "SCORE" {
		t.Errorf("params[8] (DupeMode) = %q, want SCORE (hardcoded)", got)
	}
}

func TestNZBGetAdd_CategoryDefault(t *testing.T) {
	t.Parallel()
	stub := &nzbgetStub{}
	srv := newNZBGetStub(t, stub)
	drv := newTestNZBGetDriver(srv.URL, "nzbget", "tegbzn6789", "default-cat")

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: "http://x/n.nzb"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got, _ := stub.lastParams[2].(string); got != "default-cat" {
		t.Errorf("params[2] (Category) = %q, want the settings default", got)
	}
}

func TestNZBGetAdd_TorrentUnsupported(t *testing.T) {
	t.Parallel()
	stub := &nzbgetStub{}
	srv := newNZBGetStub(t, stub)
	drv := newTestNZBGetDriver(srv.URL, "nzbget", "tegbzn6789", "")

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{})
	if !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("Add(torrent) error = %v, want ErrUnsupportedProtocol", err)
	}
}

func TestNZBGetAdd_BytesOnlyUnsupported(t *testing.T) {
	t.Parallel()
	stub := &nzbgetStub{}
	srv := newNZBGetStub(t, stub)
	drv := newTestNZBGetDriver(srv.URL, "nzbget", "tegbzn6789", "")

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, Bytes: []byte("nzb bytes")}, AddOptions{})
	if !errors.Is(err, ErrURLRequired) {
		t.Fatalf("Add(bytes-only) error = %v, want ErrURLRequired", err)
	}
}

// TestNZBGetAdd_TransportErrorRedactsSecrets pins #241: a transport-level
// failure surfaces as a *url.Error whose .URL is the jsonrpc endpoint — it
// carries no secrets itself (NZBGet auths via HTTP Basic, not the URL), but the
// sealed nzb URL (which does carry a harbrr apikey) must never reach the
// returned error either.
func TestNZBGetAdd_TransportErrorRedactsSecrets(t *testing.T) {
	t.Parallel()
	stub := &nzbgetStub{}
	srv := newNZBGetStub(t, stub)
	drv := newTestNZBGetDriver(srv.URL, "nzbget", "tegbzn6789", "")
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
	if strings.Contains(err.Error(), "tegbzn6789") {
		t.Fatalf("error leaks the configured nzbget password: %q", err)
	}
}

// nzbgetClientPackageSmoke proves the ported nzbget.Client type is reachable
// with the expected constructor shape, guarding against an accidental rename
// during the port.
var _ = nzbget.Options{Host: "", Username: "", Password: "", HTTPClient: nil}
