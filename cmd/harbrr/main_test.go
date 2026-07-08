package main

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/registry"
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

// TestBackgroundCleanupFlushesBeforeClose proves the shutdown sequence JOINS the
// DB-writing reaper goroutines (mirroring serve()'s bg WaitGroup) so the final
// search-cache counter flush commits against the still-open DB instead of racing or
// being lost to db.Close(). The seeded counter row carries an OLD updated_at; after
// cancel + join the row must carry the cache's shutdown-clock timestamp, which proves
// the on-ctx.Done() flush ran AND that bg.Wait() blocked until it committed.
func TestBackgroundCleanupFlushesBeforeClose(t *testing.T) {
	t.Parallel()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	instID := insertCleanupInstance(t, db)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	counters := database.CacheCountersStore{}
	if err := counters.Upsert(context.Background(), db,
		database.CacheCounter{InstanceID: instID, Hits: 7, Misses: 3, UpdatedAt: old}); err != nil {
		t.Fatalf("seed counter row: %v", err)
	}

	shutdownNow := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	sc := registry.NewSearchCacheWithParams(db, registry.SearchCacheParams{
		Enabled: true, KeywordTTL: 30 * time.Minute, CleanupInterval: time.Hour,
	}, func() time.Time { return shutdownNow }, zerolog.Nop())
	if err := sc.RehydrateCounters(context.Background()); err != nil {
		t.Fatalf("rehydrate counters: %v", err)
	}

	// Launch the reapers bound to a cancellable context, then shut down exactly as
	// serve() does: cancel, then JOIN, all before the DB would be closed.
	bgCtx, bgCancel := context.WithCancel(context.Background())
	var bg sync.WaitGroup
	startSessionCleanup(bgCtx, &bg, database.NewSessionStore(db), zerolog.Nop())
	startSearchCacheCleanup(bgCtx, &bg, sc, zerolog.Nop())
	startHealthEventCleanup(bgCtx, &bg, db, zerolog.Nop())
	bgCancel()
	bg.Wait()

	rows, err := counters.AllCounters(context.Background(), db)
	if err != nil {
		t.Fatalf("read counters: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("counter rows = %d, want 1", len(rows))
	}
	if !rows[0].UpdatedAt.Equal(shutdownNow) {
		t.Fatalf("counter updated_at = %v, want %v (shutdown flush must commit before close)",
			rows[0].UpdatedAt, shutdownNow)
	}
}

// insertCleanupInstance inserts a minimal enabled instance so a cache_counters row
// satisfies its FK, returning the instance id.
func insertCleanupInstance(t *testing.T, db *database.DB) int64 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO indexer_instances (slug, definition_id, name, base_url, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 1, ?, ?)`,
		"fake", "fakedef", "Fake", "", now, now)
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
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
