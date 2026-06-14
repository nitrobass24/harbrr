package main

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/version"
)

// synthetic 32-byte keys (tests only).
const (
	keyA = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	keyB = "2020202020202020202020202020202020202020202020202020202020202020"
)

// execute runs the command tree with args and returns combined stdout/stderr.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestVersionCommand(t *testing.T) {
	t.Parallel()
	out, err := execute(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out, version.String()) {
		t.Errorf("version output %q missing %q", out, version.String())
	}
}

// TestServeBootsAndShutsDown drives the full serve() wiring (config -> database +
// migrations -> keyring + canary -> registry/auth/sessions -> server): it starts
// serve in a goroutine, waits until the port is listening, cancels the context,
// and asserts serve returns nil (graceful shutdown). A regression that broke boot
// would surface an error; one that broke shutdown would time out.
func TestServeBootsAndShutsDown(t *testing.T) {
	t.Parallel()

	port := freePort(t)
	addr := net.JoinHostPort("127.0.0.1", port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		root := newRootCmd()
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"serve", "--host", "127.0.0.1", "--port", port, "--data-dir", t.TempDir(), "--log-level", "error"})
		done <- root.ExecuteContext(ctx)
	}()

	waitForListen(t, addr)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not shut down within 10s of context cancel")
	}
}

// TestVerifyCanaryFailsOnChangedKey proves the §9 startup canary: the first run
// writes it, the same key re-verifies, and a different key fails loud (so serve
// would refuse to start rather than touch secrets under the wrong key).
func TestVerifyCanaryFailsOnChangedKey(t *testing.T) {
	t.Parallel()

	db, err := database.Open(filepath.Join(t.TempDir(), "harbrr.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()

	k1, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: keyA}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring A: %v", err)
	}
	if err := verifyCanary(ctx, db, k1); err != nil {
		t.Fatalf("first run write canary: %v", err)
	}
	if err := verifyCanary(ctx, db, k1); err != nil {
		t.Fatalf("re-verify same key: %v", err)
	}

	k2, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: keyB}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring B: %v", err)
	}
	if err := verifyCanary(ctx, db, k2); err == nil {
		t.Error("verifyCanary with a changed key returned nil, want a fail-loud error")
	}
}

// freePort returns a currently-free TCP port as a string.
func freePort(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

// waitForListen blocks until addr accepts connections (the server is up).
func waitForListen(t *testing.T, addr string) {
	t.Helper()
	var dialer net.Dialer
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		cancel()
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not start listening on %s", addr)
}

func TestServeRejectsBadLogLevel(t *testing.T) {
	t.Parallel()
	if _, err := execute(t, "serve", "--log-level", "loud"); err == nil {
		t.Fatal("serve with invalid log level = nil error, want error")
	}
}
