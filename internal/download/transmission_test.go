package download

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
)

// transmissionRPCRequest is the subset of transmissionrpc's JSON-RPC envelope
// the stub needs to decode an incoming call.
type transmissionRPCRequest struct {
	Method    string         `json:"method"`
	Arguments map[string]any `json:"arguments"`
	Tag       int            `json:"tag"`
}

// transmissionStub is a minimal httptest stand-in for Transmission's RPC
// endpoint: it enforces the X-Transmission-Session-Id 409 handshake, optionally
// requires a specific Basic-auth pair (401 otherwise), answers session-get (for
// Test/RPCVersion) and torrent-add, and records the last torrent-add arguments
// so a test can assert on them.
type transmissionStub struct {
	requireUser, requirePass string // if requireUser is set, a mismatched Basic-auth pair gets a 401
	sessionID                string
	requestCount             int
	addArgs                  map[string]any
}

func newTransmissionStub(t *testing.T, s *transmissionStub) *httptest.Server {
	t.Helper()
	s.sessionID = "test-session-id"
	mux := http.NewServeMux()
	mux.HandleFunc("/transmission/rpc", func(w http.ResponseWriter, r *http.Request) {
		s.requestCount++
		if r.Header.Get("X-Transmission-Session-Id") != s.sessionID {
			w.Header().Set("X-Transmission-Session-Id", s.sessionID)
			w.WriteHeader(http.StatusConflict)
			return
		}
		if s.requireUser != "" {
			u, p, ok := r.BasicAuth()
			if !ok || u != s.requireUser || p != s.requirePass {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}

		var req transmissionRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "session-get":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": "success", "tag": req.Tag,
				"arguments": map[string]any{"rpc-version": 17, "rpc-version-minimum": 1},
			})
		case "torrent-add":
			s.addArgs = req.Arguments
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": "success", "tag": req.Tag,
				"arguments": map[string]any{"torrent-added": map[string]any{"id": 1, "hashString": "abc", "name": "test"}},
			})
		default:
			t.Errorf("unexpected method %q", req.Method)
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestTransmission(host, username, secret string, settings domain.TransmissionSettings) *transmissionDriver {
	drv, err := newTransmission(domain.DownloadClient{
		Host: host, Username: username, Settings: domain.DownloadClientSettings{Transmission: &settings},
	}, secret, http.DefaultClient)
	if err != nil {
		panic(err)
	}
	return drv.(*transmissionDriver)
}

func TestTransmissionTest_OK(t *testing.T) {
	t.Parallel()
	stub := &transmissionStub{requireUser: "admin", requirePass: "adminadmin"}
	srv := newTransmissionStub(t, stub)
	drv := newTestTransmission(srv.URL+"/transmission/rpc", "admin", "adminadmin", domain.TransmissionSettings{})

	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	// One 409 handshake response + one successful session-get.
	if stub.requestCount != 2 {
		t.Fatalf("requestCount = %d, want 2 (409 handshake + retry)", stub.requestCount)
	}
}

func TestTransmissionTest_BadCredentials(t *testing.T) {
	t.Parallel()
	stub := &transmissionStub{requireUser: "admin", requirePass: "adminadmin"}
	srv := newTransmissionStub(t, stub)
	drv := newTestTransmission(srv.URL+"/transmission/rpc", "admin", "wrong", domain.TransmissionSettings{})

	if err := drv.Test(context.Background()); err == nil {
		t.Fatal("expected an error for bad credentials")
	}
}

func TestTransmissionAdd_ViaURL(t *testing.T) {
	t.Parallel()
	stub := &transmissionStub{}
	srv := newTransmissionStub(t, stub)
	drv := newTestTransmission(srv.URL+"/transmission/rpc", "admin", "adminadmin", domain.TransmissionSettings{})

	for _, url := range []string{
		"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=test",
		"http://tracker.example/dl?token=sealed",
	} {
		if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: url}, AddOptions{}); err != nil {
			t.Fatalf("Add(%s): %v", url, err)
		}
		if got, ok := stub.addArgs["filename"].(string); !ok || got != url {
			t.Fatalf("Add(%s): filename arg = %v, want %s", url, stub.addArgs["filename"], url)
		}
		if _, ok := stub.addArgs["metainfo"]; ok {
			t.Fatalf("Add(%s): expected no metainfo arg for a URL payload", url)
		}
	}
}

func TestTransmissionAdd_ViaBytes(t *testing.T) {
	t.Parallel()
	stub := &transmissionStub{}
	srv := newTransmissionStub(t, stub)
	drv := newTestTransmission(srv.URL+"/transmission/rpc", "admin", "adminadmin", domain.TransmissionSettings{})

	raw := []byte("d8:announce...e")
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, Bytes: raw, Name: "test.torrent"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, ok := stub.addArgs["metainfo"].(string)
	if !ok {
		t.Fatalf("Add: expected a metainfo arg, got %v", stub.addArgs)
	}
	decoded, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("decode metainfo: %v", err)
	}
	if string(decoded) != string(raw) {
		t.Fatalf("metainfo decoded = %q, want %q", decoded, raw)
	}
	if _, ok := stub.addArgs["filename"]; ok {
		t.Fatalf("Add via bytes: expected no filename arg, got %v", stub.addArgs["filename"])
	}
}

func TestTransmissionAdd_OptionMapping(t *testing.T) {
	t.Parallel()
	stub := &transmissionStub{}
	srv := newTransmissionStub(t, stub)
	drv := newTestTransmission(srv.URL+"/transmission/rpc", "admin", "adminadmin", domain.TransmissionSettings{DownloadDir: "/downloads/rpc"})

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{
		Category: "tv-sonarr",
		Tags:     []string{"harbrr", "auto"},
		Paused:   true,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	labels, ok := stub.addArgs["labels"].([]any)
	if !ok || len(labels) != 3 || labels[0] != "tv-sonarr" || labels[1] != "harbrr" || labels[2] != "auto" {
		t.Fatalf("labels = %v, want [tv-sonarr harbrr auto]", stub.addArgs["labels"])
	}
	if paused, _ := stub.addArgs["paused"].(bool); !paused {
		t.Fatalf("paused = %v, want true", stub.addArgs["paused"])
	}
	if dir, _ := stub.addArgs["download-dir"].(string); dir != "/downloads/rpc" {
		t.Fatalf("download-dir = %v, want /downloads/rpc", stub.addArgs["download-dir"])
	}
}

// TestTransmissionAdd_NoHitAndRun pins that TorrentAddPayload structurally
// cannot carry a ratio/seed-time/removal option: the sent arguments must be
// exactly the ones this driver sets, nothing else.
func TestTransmissionAdd_NoHitAndRun(t *testing.T) {
	t.Parallel()
	stub := &transmissionStub{}
	srv := newTransmissionStub(t, stub)
	drv := newTestTransmission(srv.URL+"/transmission/rpc", "admin", "adminadmin", domain.TransmissionSettings{})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{
		Category: "tv-sonarr", Tags: []string{"harbrr"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	for _, forbidden := range []string{"seedRatioLimit", "seedRatioMode", "seedIdleLimit", "seedIdleMode"} {
		if _, ok := stub.addArgs[forbidden]; ok {
			t.Fatalf("Add emitted forbidden hit-and-run field %q: %v", forbidden, stub.addArgs)
		}
	}
}

func TestTransmissionAdd_UsenetUnsupported(t *testing.T) {
	t.Parallel()
	stub := &transmissionStub{}
	srv := newTransmissionStub(t, stub)
	drv := newTestTransmission(srv.URL+"/transmission/rpc", "admin", "adminadmin", domain.TransmissionSettings{})

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: "https://example.com/release.nzb"}, AddOptions{})
	if !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("Add(usenet) error = %v, want ErrUnsupportedProtocol", err)
	}
}

// TestTransmissionCredentialsNeverInError proves the secret embedded as URL
// userinfo never leaks into a returned error, even against an unreachable host.
func TestTransmissionCredentialsNeverInError(t *testing.T) {
	t.Parallel()
	const secret = "SECRETTRANSMISSIONPASSWORD"
	// An unused local port: connection refused, but the endpoint URL (with
	// userinfo) is what net/http would otherwise echo into the wrapped error.
	drv := newTestTransmission("http://127.0.0.1:1/transmission/rpc", "admin", secret, domain.TransmissionSettings{})

	err := drv.Test(context.Background())
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaks the secret: %q", err)
	}
}
