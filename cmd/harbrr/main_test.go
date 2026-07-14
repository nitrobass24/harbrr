package main

import (
	"bytes"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/version"
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

// TestServeBootsAndShutsDown drives the full serve command (config -> internal/app.New
// -> internal/app.Run): it starts serve in a goroutine, waits until the port is
// listening, cancels the context, and asserts serve returns nil (graceful shutdown).
// A regression that broke boot would surface an error; one that broke shutdown would
// time out. The composition root's own wiring (database, canary, registry, reapers,
// shutdown ordering) is covered directly in internal/app's tests.
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
