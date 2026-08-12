package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
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
// -> internal/app.Run): it starts serve in a goroutine, waits until harbrr itself
// answers /healthz, cancels the context, and asserts serve returns nil (graceful shutdown).
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

	waitForReady(t, addr, done)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not shut down within 30s of context cancel")
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

// waitForReady blocks until harbrr's own /healthz answers on addr, up to one
// generous 60s budget — it returns the moment harbrr is up, so the happy path pays
// nothing and slow runner I/O only costs time. If serve exits before then (e.g. a
// boot failure), it fails immediately with the returned error instead of burning
// the budget.
//
// The readiness check is identity-checked on purpose (autobrr/harbrr#469): a bare
// TCP connect to addr does NOT prove harbrr is up. The port comes from the ephemeral
// range, so a loopback dial can succeed while harbrr is still migrating — either via
// a TCP self-connect (the kernel hands the dialer the destination port as its source
// port) or because another process on a busy shared runner grabbed the port after
// freePort released it. The old dial-based probe took that for readiness and
// cancelled the boot context mid-migration. Only harbrr's own liveness payload
// proves boot finished.
func waitForReady(t *testing.T, addr string, done <-chan error) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	url := "http://" + addr + "/healthz"
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("serve exited early: %v", err)
		default:
		}
		if healthy(client, url) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("harbrr did not answer /healthz on %s within 60s", addr)
}

// healthy reports whether url answers with harbrr's liveness payload — status "ok"
// and this build's version. Anything else (connection refused, a foreign listener,
// a self-connected socket echoing the request back) is not ready.
func healthy(client *http.Client, url string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var health struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<12)).Decode(&health); err != nil {
		return false
	}
	return health.Status == "ok" && health.Version == version.Version
}

func TestServeRejectsBadLogLevel(t *testing.T) {
	t.Parallel()
	if _, err := execute(t, "serve", "--log-level", "loud"); err == nil {
		t.Fatal("serve with invalid log level = nil error, want error")
	}
}
