package download

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
)

func newBlackholeDriver(t *testing.T, s domain.BlackholeSettings) *blackholeDriver {
	t.Helper()
	drv, err := newBlackhole(domain.DownloadClient{Settings: domain.DownloadClientSettings{Blackhole: &s}}, "", nil)
	if err != nil {
		t.Fatalf("newBlackhole: %v", err)
	}
	return drv.(*blackholeDriver)
}

// dirEntries lists the plain filenames present in dir, for asserting no
// temp-file residue survives a successful (or failed) Add.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestBlackholeAdd_TorrentBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	drv := newBlackholeDriver(t, domain.BlackholeSettings{TorrentDir: dir})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, Bytes: []byte("d8:announce...e"), Name: "Some Release"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	names := dirEntries(t, dir)
	if len(names) != 1 || names[0] != "Some Release.torrent" {
		t.Fatalf("dir entries = %v, want exactly [Some Release.torrent]", names)
	}
	info, err := os.Stat(filepath.Join(dir, names[0]))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}
	got, err := os.ReadFile(filepath.Join(dir, names[0])) //nolint:gosec // test-owned tempdir path.
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "d8:announce...e" {
		t.Errorf("content = %q, want d8:announce...e", got)
	}
}

func TestBlackholeAdd_NZBBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	drv := newBlackholeDriver(t, domain.BlackholeSettings{NZBDir: dir})

	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, Bytes: []byte("<nzb/>"), Name: "Some Release"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	names := dirEntries(t, dir)
	if len(names) != 1 || names[0] != "Some Release.nzb" {
		t.Fatalf("dir entries = %v, want exactly [Some Release.nzb]", names)
	}
}

func TestBlackholeAdd_TorrentURLFetch(t *testing.T) {
	t.Parallel()
	const body = "fetched torrent bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	drv := newBlackholeDriver(t, domain.BlackholeSettings{TorrentDir: dir})
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: srv.URL, Name: "fetched"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "fetched.torrent")) //nolint:gosec // test-owned tempdir path.
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != body {
		t.Errorf("content = %q, want %q", got, body)
	}
}

func TestBlackholeAdd_NZBURLFetch(t *testing.T) {
	t.Parallel()
	const body = "<nzb>fetched</nzb>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	drv := newBlackholeDriver(t, domain.BlackholeSettings{NZBDir: dir})
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: srv.URL, Name: "fetched"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "fetched.nzb")) //nolint:gosec // test-owned tempdir path.
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != body {
		t.Errorf("content = %q, want %q", got, body)
	}
}

func TestBlackholeAdd_TorrentFetchExceedsCap(t *testing.T) {
	t.Parallel()
	oversized := bytes.Repeat([]byte("x"), maxTorrentFetchBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(oversized)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	drv := newBlackholeDriver(t, domain.BlackholeSettings{TorrentDir: dir})
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: srv.URL, Name: "big"}, AddOptions{}); err == nil {
		t.Fatal("expected an error for an oversized torrent fetch")
	}
	if names := dirEntries(t, dir); len(names) != 0 {
		t.Errorf("dir entries after failed fetch = %v, want none", names)
	}
}

// TestBlackholeAdd_NZBCapIsSeparate pins the coordinator-approved deviation: an
// nzb fetch bigger than the torrent cap but within the nzb cap must succeed,
// and one bigger than the nzb cap must fail — proving the two caps are
// genuinely independent, not both clamped to the smaller torrent value.
func TestBlackholeAdd_NZBCapIsSeparate(t *testing.T) {
	t.Parallel()
	withinNZBCap := bytes.Repeat([]byte("y"), maxTorrentFetchBytes+(1<<20)) // > torrent cap, < nzb cap
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(withinNZBCap)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	drv := newBlackholeDriver(t, domain.BlackholeSettings{NZBDir: dir})
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: srv.URL, Name: "big"}, AddOptions{}); err != nil {
		t.Fatalf("Add(within nzb cap, over torrent cap): %v", err)
	}

	oversized := bytes.Repeat([]byte("z"), maxNZBFetchBytes+1)
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(oversized)
	}))
	t.Cleanup(srv2.Close)
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, URL: srv2.URL, Name: "toobig"}, AddOptions{}); err == nil {
		t.Fatal("expected an error for an oversized nzb fetch")
	}
}

func TestBlackholeAdd_MagnetSaved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	drv := newBlackholeDriver(t, domain.BlackholeSettings{TorrentDir: dir, SaveMagnetFiles: true})

	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=test"
	if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: magnet, Name: "magnet-release"}, AddOptions{}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "magnet-release.magnet")) //nolint:gosec // test-owned tempdir path.
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != magnet+"\n" {
		t.Errorf("content = %q, want %q", got, magnet+"\n")
	}
}

func TestBlackholeAdd_MagnetNotSaved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	drv := newBlackholeDriver(t, domain.BlackholeSettings{TorrentDir: dir})

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, URL: "magnet:?xt=urn:btih:x", Name: "n"}, AddOptions{})
	if !errors.Is(err, errMagnetNotSaved) {
		t.Fatalf("err = %v, want errMagnetNotSaved", err)
	}
	if names := dirEntries(t, dir); len(names) != 0 {
		t.Errorf("dir entries = %v, want none", names)
	}
}

func TestBlackholeAdd_UnsupportedProtocol(t *testing.T) {
	t.Parallel()
	drv := newBlackholeDriver(t, domain.BlackholeSettings{TorrentDir: t.TempDir()})

	err := drv.Add(context.Background(), Payload{Protocol: ProtocolUsenet, Bytes: []byte("<nzb/>"), Name: "n"}, AddOptions{})
	if !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("err = %v, want ErrUnsupportedProtocol", err)
	}
}

func TestBlackholeAdd_ReAddOverwritesNoResidue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	drv := newBlackholeDriver(t, domain.BlackholeSettings{TorrentDir: dir})

	for _, content := range []string{"first", "second"} {
		if err := drv.Add(context.Background(), Payload{Protocol: ProtocolTorrent, Bytes: []byte(content), Name: "dup"}, AddOptions{}); err != nil {
			t.Fatalf("Add(%s): %v", content, err)
		}
	}
	names := dirEntries(t, dir)
	if len(names) != 1 || names[0] != "dup.torrent" {
		t.Fatalf("dir entries = %v, want exactly [dup.torrent]", names)
	}
	got, err := os.ReadFile(filepath.Join(dir, "dup.torrent")) //nolint:gosec // test-owned tempdir path.
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("content = %q, want second (overwritten)", got)
	}
}

func TestBlackholeAdd_SanitizesName(t *testing.T) {
	t.Parallel()
	longName := strings.Repeat("x", maxSanitizedNameLen+50)
	tests := []struct {
		name string
		in   string
	}{
		{"path separators", "../../etc/passwd"},
		{"windows separators", `a\b\c`},
		{"oversized", longName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			subDir := t.TempDir()
			d := newBlackholeDriver(t, domain.BlackholeSettings{TorrentDir: subDir})
			if err := d.Add(context.Background(), Payload{Protocol: ProtocolTorrent, Bytes: []byte("x"), Name: tt.in}, AddOptions{}); err != nil {
				t.Fatalf("Add: %v", err)
			}
			names := dirEntries(t, subDir)
			if len(names) != 1 {
				t.Fatalf("dir entries = %v, want exactly one file directly in %s", names, subDir)
			}
			if strings.ContainsAny(names[0], `/\`) {
				t.Errorf("filename %q retains a path separator", names[0])
			}
			if len(names[0]) > maxSanitizedNameLen+len(".torrent") {
				t.Errorf("filename %q exceeds the length bound", names[0])
			}
		})
	}
}

func TestBlackholeTest_OK(t *testing.T) {
	t.Parallel()
	drv := newBlackholeDriver(t, domain.BlackholeSettings{TorrentDir: t.TempDir(), NZBDir: t.TempDir()})
	if err := drv.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
}

// TestBlackholeTest_UnwritableDir uses a dir whose parent doesn't exist rather
// than chmod: harbrr's CI runners run as root, where chmod-based
// permission-denied tests are silently neutered.
func TestBlackholeTest_UnwritableDir(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "does-not-exist", "sub")
	drv := newBlackholeDriver(t, domain.BlackholeSettings{TorrentDir: missing})
	err := drv.Test(context.Background())
	if err == nil {
		t.Fatal("expected an error for a nonexistent dir")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error %q does not name the dir %q", err, missing)
	}
}
