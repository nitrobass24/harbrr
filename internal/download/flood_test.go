package download

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
)

// floodAddRequest mirrors the JSON body floodDriver.Add posts, for stub decoding.
type floodAddRequest struct {
	URLs        []string `json:"urls"`
	Files       []string `json:"files"`
	Destination string   `json:"destination"`
	Tags        []string `json:"tags"`
	Start       bool     `json:"start"`
}

// floodStub is a minimal httptest stand-in for Flood's API: authenticate sets a
// jwt cookie; connection-test and the add endpoints require it, answering 401
// otherwise (once, if wantOneUnauthorized is set, to exercise the re-auth path).
type floodStub struct {
	wantUsername, wantPassword string
	authCalls                  int
	unauthorizedOnce           bool
	usedUnauthorized           bool
	lastAddPath                string
	lastAdd                    floodAddRequest
}

func newFloodStub(t *testing.T, s *floodStub) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/authenticate", func(w http.ResponseWriter, r *http.Request) {
		s.authCalls++
		var body struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode auth body: %v", err)
		}
		if s.wantUsername != "" && (body.Username != s.wantUsername || body.Password != s.wantPassword) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "jwt", Value: "session-token"}) //nolint:gosec // G124: test stub; Secure/HttpOnly are the real server's to set, not this fixture's.
		w.WriteHeader(http.StatusOK)
	})
	authed := func(w http.ResponseWriter, r *http.Request) bool {
		ck, err := r.Cookie("jwt")
		if err != nil || ck.Value != "session-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		if s.unauthorizedOnce && !s.usedUnauthorized {
			s.usedUnauthorized = true
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("/api/client/connection-test", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	addHandler := func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		s.lastAddPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&s.lastAdd); err != nil {
			t.Fatalf("decode add body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}
	mux.HandleFunc("/api/torrents/add-urls", addHandler)
	mux.HandleFunc("/api/torrents/add-files", addHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestFlood(host, username, password string, settings domain.FloodSettings) *floodDriver {
	drv, _ := newFlood(domain.DownloadClient{Host: host, Username: username, Settings: domain.DownloadClientSettings{Flood: &settings}}, password, http.DefaultClient)
	return drv.(*floodDriver)
}

func TestFloodTest_OK(t *testing.T) {
	t.Parallel()
	stub := &floodStub{wantUsername: "admin", wantPassword: "hunter2"}
	srv := newFloodStub(t, stub)
	drv := newTestFlood(srv.URL, "admin", "hunter2", domain.FloodSettings{})

	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if stub.authCalls != 1 {
		t.Fatalf("authCalls = %d, want 1", stub.authCalls)
	}
}

func TestFloodTest_BadCredentials(t *testing.T) {
	t.Parallel()
	stub := &floodStub{wantUsername: "admin", wantPassword: "hunter2"}
	srv := newFloodStub(t, stub)
	drv := newTestFlood(srv.URL, "admin", "wrong", domain.FloodSettings{})

	if err := drv.Test(context.Background()); err == nil {
		t.Fatal("expected an error for bad credentials")
	}
}

// TestFloodJWTReusedAcrossCalls pins that the jwt cookie is cached and reused —
// a second call within the same driver instance must not re-authenticate.
func TestFloodJWTReusedAcrossCalls(t *testing.T) {
	t.Parallel()
	stub := &floodStub{}
	srv := newFloodStub(t, stub)
	drv := newTestFlood(srv.URL, "admin", "hunter2", domain.FloodSettings{})

	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test #1: %v", err)
	}
	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test #2: %v", err)
	}
	if stub.authCalls != 1 {
		t.Fatalf("authCalls = %d, want 1 (jwt should be reused)", stub.authCalls)
	}
}

// TestFloodReauthOnceOn401 pins the re-auth-once behavior: a stale/expired jwt
// gets exactly one re-authenticate + retry, not an infinite loop.
func TestFloodReauthOnceOn401(t *testing.T) {
	t.Parallel()
	stub := &floodStub{unauthorizedOnce: true}
	srv := newFloodStub(t, stub)
	drv := newTestFlood(srv.URL, "admin", "hunter2", domain.FloodSettings{})

	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if stub.authCalls != 2 {
		t.Fatalf("authCalls = %d, want 2 (initial + one re-auth after 401)", stub.authCalls)
	}
}

func TestFloodAdd_ViaURL(t *testing.T) {
	t.Parallel()
	stub := &floodStub{}
	srv := newFloodStub(t, stub)
	drv := newTestFlood(srv.URL, "admin", "hunter2", domain.FloodSettings{})

	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=test"
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: magnet}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if stub.lastAddPath != "/api/torrents/add-urls" {
		t.Fatalf("path = %q, want add-urls", stub.lastAddPath)
	}
	if len(stub.lastAdd.URLs) != 1 || stub.lastAdd.URLs[0] != magnet {
		t.Fatalf("urls = %v, want [%s]", stub.lastAdd.URLs, magnet)
	}
	if !stub.lastAdd.Start {
		t.Fatal("start = false, want true (default not-paused)")
	}
}

func TestFloodAdd_ViaBytes(t *testing.T) {
	t.Parallel()
	stub := &floodStub{}
	srv := newFloodStub(t, stub)
	drv := newTestFlood(srv.URL, "admin", "hunter2", domain.FloodSettings{})

	payload := []byte("d8:announce...e")
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, Bytes: payload, Name: "test.torrent"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if stub.lastAddPath != "/api/torrents/add-files" {
		t.Fatalf("path = %q, want add-files", stub.lastAddPath)
	}
	if len(stub.lastAdd.Files) != 1 {
		t.Fatalf("files = %v, want 1 base64 entry", stub.lastAdd.Files)
	}
	got, err := base64.StdEncoding.DecodeString(stub.lastAdd.Files[0])
	if err != nil || string(got) != string(payload) {
		t.Fatalf("files[0] decoded = %q, err %v, want %q", got, err, payload)
	}
}

func TestFloodAdd_OptionMapping(t *testing.T) {
	t.Parallel()
	stub := &floodStub{}
	srv := newFloodStub(t, stub)
	drv := newTestFlood(srv.URL, "admin", "hunter2", domain.FloodSettings{Destination: "/downloads", Tags: []string{"base"}})

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{
		Category: "tv-sonarr",
		Tags:     []string{"harbrr"},
		Paused:   true,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if stub.lastAdd.Destination != "/downloads" {
		t.Fatalf("destination = %q, want /downloads", stub.lastAdd.Destination)
	}
	wantTags := map[string]bool{"base": true, "harbrr": true, "tv-sonarr": true}
	if len(stub.lastAdd.Tags) != len(wantTags) {
		t.Fatalf("tags = %v, want %v (category folded in as a tag)", stub.lastAdd.Tags, wantTags)
	}
	for _, tag := range stub.lastAdd.Tags {
		if !wantTags[tag] {
			t.Fatalf("unexpected tag %q in %v", tag, stub.lastAdd.Tags)
		}
	}
	if stub.lastAdd.Start {
		t.Fatal("start = true, want false (opts.Paused=true)")
	}
}

func TestFloodAdd_PausedEscalatesFromSettings(t *testing.T) {
	t.Parallel()
	stub := &floodStub{}
	srv := newFloodStub(t, stub)
	drv := newTestFlood(srv.URL, "admin", "hunter2", domain.FloodSettings{StartPaused: true})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if stub.lastAdd.Start {
		t.Fatal("start = true, want false (settings.StartPaused floor)")
	}
}

func TestFloodAdd_UsenetUnsupported(t *testing.T) {
	t.Parallel()
	stub := &floodStub{}
	srv := newFloodStub(t, stub)
	drv := newTestFlood(srv.URL, "admin", "hunter2", domain.FloodSettings{})

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: "https://example.com/release.nzb"}, AddOptions{})
	if !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("Add(usenet) error = %v, want ErrUnsupportedProtocol", err)
	}
}
