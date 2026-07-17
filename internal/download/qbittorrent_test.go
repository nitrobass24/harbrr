package download

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
)

// qbitStub is a minimal httptest stand-in for qBittorrent's WebUI API: it answers
// auth/login (ok/bad-creds, gated by wantBadCreds) and records the form fields
// posted to torrents/add so a test can assert on the emitted options — in
// particular that no share-limit/auto-removal field is ever sent (no-hit-and-run).
type qbitStub struct {
	wantBadCreds bool
	addForm      map[string][]string // last torrents/add form fields (url-encoded or multipart)
	addWasBytes  bool                // true if the last add came in as a multipart file upload
}

func newQbitStub(t *testing.T, s *qbitStub) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		if s.wantBadCreds {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("Fails."))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Ok."))
	})
	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		s.addWasBytes = strings.HasPrefix(ct, "multipart/form-data")
		if s.addWasBytes {
			if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // test stub; body is a fixed small torrent fixture, not attacker-controlled.
				t.Fatalf("parse multipart form: %v", err)
			}
			s.addForm = r.MultipartForm.Value
		} else {
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			s.addForm = r.Form
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Ok."))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(host, username, password string) *qbittorrentDriver {
	drv, _ := newQBittorrent(domain.DownloadClient{Host: host, Username: username}, password, nil)
	return drv.(*qbittorrentDriver)
}

func TestQBittorrentTest_OK(t *testing.T) {
	t.Parallel()
	stub := &qbitStub{}
	srv := newQbitStub(t, stub)
	drv := newTestClient(srv.URL, "admin", "adminadmin")
	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
}

func TestQBittorrentTest_BadCredentials(t *testing.T) {
	t.Parallel()
	stub := &qbitStub{wantBadCreds: true}
	srv := newQbitStub(t, stub)
	drv := newTestClient(srv.URL, "admin", "wrong")
	err := drv.Test(context.Background())
	if err == nil {
		t.Fatal("expected an error for bad credentials")
	}
}

func TestQBittorrentAdd_ViaURL(t *testing.T) {
	t.Parallel()
	stub := &qbitStub{}
	srv := newQbitStub(t, stub)
	drv := newTestClient(srv.URL, "admin", "adminadmin")

	for _, url := range []string{
		"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=test",
		"http://tracker.example/dl?token=sealed",
	} {
		if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: url}, AddOptions{}); err != nil {
			t.Fatalf("Add(%s): %v", url, err)
		}
		if stub.addWasBytes {
			t.Fatalf("Add(%s): expected url form, got a multipart upload", url)
		}
		if got := stub.addForm["urls"]; len(got) != 1 || got[0] != url {
			t.Fatalf("Add(%s): urls form field = %v, want [%s]", url, got, url)
		}
	}
}

func TestQBittorrentAdd_ViaBytes(t *testing.T) {
	t.Parallel()
	stub := &qbitStub{}
	srv := newQbitStub(t, stub)
	drv := newTestClient(srv.URL, "admin", "adminadmin")

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, Bytes: []byte("d8:announce...e"), Name: "test.torrent"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !stub.addWasBytes {
		t.Fatal("Add via Bytes: expected a multipart upload")
	}
}

func TestQBittorrentAdd_OptionMapping(t *testing.T) {
	t.Parallel()
	stub := &qbitStub{}
	srv := newQbitStub(t, stub)
	drv := newTestClient(srv.URL, "admin", "adminadmin")

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{
		Category: "tv-sonarr",
		Tags:     []string{"harbrr", "auto"},
		Paused:   true,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := first(stub.addForm["category"]); got != "tv-sonarr" {
		t.Fatalf("category = %q, want tv-sonarr", got)
	}
	if got := first(stub.addForm["tags"]); got != "harbrr,auto" {
		t.Fatalf("tags = %q, want harbrr,auto", got)
	}
	if got := first(stub.addForm["paused"]); got != "true" {
		t.Fatalf("paused = %q, want true", got)
	}
}

// TestQBittorrentAdd_NoHitAndRun is the standing assertion that harbrr never asks
// qBittorrent to share-limit or auto-remove a torrent it adds: the emitted form
// must never carry a ratio/seed-time limit field, whatever AddOptions says.
func TestQBittorrentAdd_NoHitAndRun(t *testing.T) {
	t.Parallel()
	stub := &qbitStub{}
	srv := newQbitStub(t, stub)
	drv := newTestClient(srv.URL, "admin", "adminadmin")

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x"}, AddOptions{
		Category: "tv-sonarr", Tags: []string{"harbrr"}, Paused: false,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	for _, forbidden := range []string{"ratioLimit", "seedingTimeLimit", "inactiveSeedingTimeLimit"} {
		if _, ok := stub.addForm[forbidden]; ok {
			t.Fatalf("Add emitted forbidden hit-and-run field %q: %v", forbidden, stub.addForm)
		}
	}
}

func TestQBittorrentAdd_UsenetUnsupported(t *testing.T) {
	t.Parallel()
	stub := &qbitStub{}
	srv := newQbitStub(t, stub)
	drv := newTestClient(srv.URL, "admin", "adminadmin")

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: "https://example.com/release.nzb"}, AddOptions{})
	if !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("Add(usenet) error = %v, want ErrUnsupportedProtocol", err)
	}
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
