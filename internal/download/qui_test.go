package download

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
)

// quiStub is a minimal httptest stand-in for qui's API: it answers
// GET /api/instances and records the form fields posted to the per-instance
// torrents endpoint, so a test can assert on the emitted fields — in particular
// that no share-limit field is ever sent.
type quiStub struct {
	instances   []int
	addForm     map[string][]string
	addWasBytes bool
	addConflict bool
	gotKey      string
}

func newQuiStub(t *testing.T, instanceID int, s *quiStub) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/instances", func(w http.ResponseWriter, r *http.Request) {
		s.gotKey = r.Header.Get("X-API-Key")
		instances := make([]quiInstance, len(s.instances))
		for i, id := range s.instances {
			instances[i] = quiInstance{ID: id}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(instances); err != nil {
			t.Errorf("encode instances: %v", err)
			http.Error(w, "encode failed", http.StatusInternalServerError)
			return
		}
	})
	mux.HandleFunc("/api/instances/"+strconv.Itoa(instanceID)+"/torrents", func(w http.ResponseWriter, r *http.Request) {
		s.gotKey = r.Header.Get("X-API-Key")
		if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // test stub; body is a fixed small torrent fixture, not attacker-controlled.
			t.Errorf("parse multipart form: %v", err)
			http.Error(w, "bad form", http.StatusInternalServerError)
			return
		}
		s.addForm = r.MultipartForm.Value
		s.addWasBytes = len(r.MultipartForm.File["torrent"]) > 0
		if s.addConflict {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`invalid request: apikey=LEAKED_SECRET_VALUE`))
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestQui(host string, instanceID int, apiKey string, settings domain.QuiSettings) *quiDriver {
	settings.InstanceID = instanceID
	drv, _ := newQui(domain.DownloadClient{Host: host, Settings: domain.DownloadClientSettings{Qui: &settings}}, apiKey, http.DefaultClient)
	return drv.(*quiDriver)
}

func TestQuiTest_OK(t *testing.T) {
	t.Parallel()
	stub := &quiStub{instances: []int{1, 7, 9}}
	srv := newQuiStub(t, 7, stub)
	drv := newTestQui(srv.URL, 7, "the-key", domain.QuiSettings{})

	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if stub.gotKey != "the-key" {
		t.Fatalf("X-API-Key = %q, want the-key", stub.gotKey)
	}
}

func TestQuiTest_InstanceNotFound(t *testing.T) {
	t.Parallel()
	stub := &quiStub{instances: []int{1, 2}}
	srv := newQuiStub(t, 7, stub)
	drv := newTestQui(srv.URL, 7, "the-key", domain.QuiSettings{})

	if err := drv.Test(context.Background()); err == nil {
		t.Fatal("expected an error for a missing instance id")
	}
}

func TestQuiAdd_ViaURL(t *testing.T) {
	t.Parallel()
	stub := &quiStub{}
	srv := newQuiStub(t, 7, stub)
	drv := newTestQui(srv.URL, 7, "the-key", domain.QuiSettings{})

	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=test"
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: magnet}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if stub.addWasBytes {
		t.Fatal("Add(url): expected a urls field, got a torrent file part")
	}
	if got := first(stub.addForm["urls"]); got != magnet {
		t.Fatalf("urls form field = %q, want %q", got, magnet)
	}
}

func TestQuiAdd_ViaBytes(t *testing.T) {
	t.Parallel()
	stub := &quiStub{}
	srv := newQuiStub(t, 7, stub)
	drv := newTestQui(srv.URL, 7, "the-key", domain.QuiSettings{})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, Bytes: []byte("d8:announce...e"), Name: "test.torrent"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !stub.addWasBytes {
		t.Fatal("Add via Bytes: expected a torrent file part")
	}
}

func TestQuiAdd_OptionMapping(t *testing.T) {
	t.Parallel()
	stub := &quiStub{}
	srv := newQuiStub(t, 7, stub)
	drv := newTestQui(srv.URL, 7, "the-key", domain.QuiSettings{Category: "default-cat", Tags: []string{"base"}})

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{
		Category: "tv-sonarr",
		Tags:     []string{"harbrr"},
		Paused:   true,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := first(stub.addForm["category"]); got != "tv-sonarr" {
		t.Fatalf("category = %q, want tv-sonarr (opts override settings default)", got)
	}
	if got := first(stub.addForm["tags"]); got != "base,harbrr" {
		t.Fatalf("tags = %q, want base,harbrr (settings ∪ opts)", got)
	}
	if got := first(stub.addForm["paused"]); got != "true" {
		t.Fatalf("paused = %q, want true", got)
	}
}

func TestQuiAdd_PausedEscalatesFromSettings(t *testing.T) {
	t.Parallel()
	stub := &quiStub{}
	srv := newQuiStub(t, 7, stub)
	drv := newTestQui(srv.URL, 7, "the-key", domain.QuiSettings{StartPaused: true})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := first(stub.addForm["paused"]); got != "true" {
		t.Fatalf("paused = %q, want true (settings.StartPaused floor)", got)
	}
}

// TestQuiAdd_NoHitAndRun is the standing assertion that harbrr never asks qui to
// share-limit or auto-remove a torrent it adds.
func TestQuiAdd_NoHitAndRun(t *testing.T) {
	t.Parallel()
	stub := &quiStub{}
	srv := newQuiStub(t, 7, stub)
	drv := newTestQui(srv.URL, 7, "the-key", domain.QuiSettings{})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{
		Category: "tv-sonarr", Tags: []string{"harbrr"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	for _, forbidden := range []string{"ratioLimit", "seedingTimeLimit", "inactiveSeedingTimeLimit"} {
		if _, ok := stub.addForm[forbidden]; ok {
			t.Fatalf("Add emitted forbidden hit-and-run field %q: %v", forbidden, stub.addForm)
		}
	}
}

func TestQuiAdd_UsenetUnsupported(t *testing.T) {
	t.Parallel()
	stub := &quiStub{}
	srv := newQuiStub(t, 7, stub)
	drv := newTestQui(srv.URL, 7, "the-key", domain.QuiSettings{})

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: "https://example.com/release.nzb"}, AddOptions{})
	if !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("Add(usenet) error = %v, want ErrUnsupportedProtocol", err)
	}
}

// TestQuiAdd_ErrorRedactsSecret pins that a non-2xx response body — which could
// echo back a secret-shaped token — never reaches the returned error verbatim.
func TestQuiAdd_ErrorRedactsSecret(t *testing.T) {
	t.Parallel()
	stub := &quiStub{addConflict: true}
	srv := newQuiStub(t, 7, stub)
	drv := newTestQui(srv.URL, 7, "the-key", domain.QuiSettings{})

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{})
	if err == nil {
		t.Fatal("expected an add error from the 409 stub")
	}
	if strings.Contains(err.Error(), "LEAKED_SECRET_VALUE") {
		t.Fatalf("error leaks the response body secret: %q", err)
	}
}
