package app

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/config"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/secrets"
)

// getCtx issues a context-bound GET (noctx requires http.NewRequestWithContext
// over the bare http.Get shorthand).
func getCtx(ctx context.Context, t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request for %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// synthetic 32-byte keys (tests only).
const (
	keyA = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	keyB = "2020202020202020202020202020202020202020202020202020202020202020"
)

// testConfig returns a working Config rooted at a fresh temp dir, so New can
// open+migrate its own on-disk database, auto-generate a keyfile, etc. —
// exercising the same path production boot does.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	return &cfg
}

// TestNewBootsAndServesHandler drives a full New() boot against a temp-dir SQLite
// (exercising canary init, migrations, and the cache-overlay load) and then serves
// requests through Handler() — the httptest-based path, with no listener — proving
// the daemon's whole mux (management API and the *arr-facing Torznab feed on
// separate route trees, per internal/server's routing invariant) comes up wired.
func TestNewBootsAndServesHandler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := testConfig(t)

	a, err := New(ctx, Deps{Config: cfg, Logger: zerolog.Nop()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ts := httptest.NewServer(a.Handler())
	t.Cleanup(ts.Close)

	resp := getCtx(ctx, t, ts.URL+"/healthz")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", resp.StatusCode)
	}

	// A Torznab caps request: unauthenticated/unresolvable requests still answer
	// HTTP 200 with an <error> document (Jackett's torznab contract), so a real
	// minted key is what proves the request actually reached the auth-gated
	// handler rather than merely confirming the route exists.
	key, _, err := a.auth.MintAPIKey(ctx, "test")
	if err != nil {
		t.Fatalf("mint api key: %v", err)
	}
	resp = getCtx(ctx, t, ts.URL+"/api/indexers/nonexistent/results/torznab?t=caps&apikey="+key)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("torznab caps status = %d, want 200 (torznab errors are 200 + XML body)", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "xml") {
		t.Fatalf("torznab caps content-type = %q, want an XML content type", ct)
	}
}

// TestNewWithOptions exercises the two test-widening Option seams: WithDatabase
// (New skips its own openDatabase and uses the caller's already-open one) and
// WithHTTPClient (overrides the outbound client shared by notify/app-sync/
// announce). Each case must still produce a fully working, servable App.
func TestNewWithOptions(t *testing.T) {
	t.Parallel()

	t.Run("WithDatabase", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		db, err := database.Open(filepath.Join(t.TempDir(), "harbrr.db"))
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}

		a, err := New(context.Background(), Deps{Config: cfg, Logger: zerolog.Nop()}, WithDatabase(db))
		if err != nil {
			t.Fatalf("New with WithDatabase: %v", err)
		}
		if a.db != db {
			t.Fatal("New built its own database instead of using the injected one")
		}
		assertHealthy(t, a)
	})

	t.Run("WithHTTPClient", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		a, err := New(context.Background(), Deps{Config: cfg, Logger: zerolog.Nop()},
			WithHTTPClient(&http.Client{Timeout: 5 * time.Second}))
		if err != nil {
			t.Fatalf("New with WithHTTPClient: %v", err)
		}
		assertHealthy(t, a)
	})
}

func assertHealthy(t *testing.T, a *App) {
	t.Helper()
	ts := httptest.NewServer(a.Handler())
	t.Cleanup(ts.Close)
	resp := getCtx(context.Background(), t, ts.URL+"/healthz")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", resp.StatusCode)
	}
}

// TestVerifyCanaryFailsOnChangedKey proves the startup canary: the first run
// writes it, the same key re-verifies, and a different key fails loud (so New
// would refuse to build the App rather than touch secrets under the wrong key).
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

// TestRunFlushesCacheBeforeClose is the end-to-end sibling of
// TestBackgroundCleanupFlushesBeforeClose (lifecycle_test.go): it drives the full
// public API (New + Run against a real listener) instead of calling the reap
// starters directly, proving App.Run's shutdown order — reapers join and flush,
// THEN the database closes — holds through the composition root, not just in the
// reap skeleton. A counter seeded with a stale updated_at must carry a fresh one
// once Run returns, and the same on-disk database (reopened after Run's db.Close)
// must still show it — so the flush both ran AND committed before shutdown.
func TestRunFlushesCacheBeforeClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := testConfig(t)
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = freeAppPort(t)

	a, err := New(ctx, Deps{Config: cfg, Logger: zerolog.Nop()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	instID := insertCleanupInstance(t, a.db)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	counters := database.CacheCountersStore{}
	if err := counters.Upsert(ctx, a.db,
		database.CacheCounter{InstanceID: instID, Hits: 7, Misses: 3, UpdatedAt: old}); err != nil {
		t.Fatalf("seed counter row: %v", err)
	}
	// New()'s own RehydrateCounters ran before this row existed, so the cache's
	// in-memory state has never seen it; re-rehydrate to adopt it before Run starts
	// the reaper that will flush it back out on shutdown.
	if err := a.searchCache.RehydrateCounters(ctx); err != nil {
		t.Fatalf("rehydrate seeded counter: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(runCtx) }()

	addr := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))
	waitForAppListen(t, addr)
	before := time.Now()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not shut down within 10s of context cancel")
	}

	reopened, err := database.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatalf("reopen database after Run's db.Close: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	rows, err := counters.AllCounters(ctx, reopened)
	if err != nil {
		t.Fatalf("read counters: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("counter rows = %d, want 1", len(rows))
	}
	if !rows[0].UpdatedAt.After(old) || rows[0].UpdatedAt.Before(before.Add(-time.Second)) {
		t.Fatalf("counter updated_at = %v, want a fresh shutdown-flush timestamp near %v (flush must commit before db.Close)",
			rows[0].UpdatedAt, before)
	}
}

// freeAppPort returns a currently-free TCP port.
func freeAppPort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// waitForAppListen blocks until addr accepts connections (the server is up).
func waitForAppListen(t *testing.T, addr string) {
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
