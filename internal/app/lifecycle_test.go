package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native/catalog"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// TestBackgroundCleanupFlushesBeforeClose proves the reap skeleton's shutdown
// contract that App.Run's ordering (bgCancel -> bg.Wait -> drainNotify -> db.Close)
// depends on: cancelling ctx and joining the WaitGroup must block until each
// reaper's final flush has committed, so a caller that then closes the database
// never races or loses it. The seeded counter row carries an OLD updated_at; after
// cancel + join the row must carry the cache's shutdown-clock timestamp, which
// proves both that the on-ctx.Done() flush ran AND that bg.Wait() blocked until it
// committed. No sleeps: bg.Wait() is the only synchronization, so this is
// deterministic under -race.
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
	sc := registry.NewSearchCacheFromConfig(db, registry.CacheConfigView{
		Enabled: true, KeywordTTL: 30 * time.Minute, CleanupInterval: time.Hour,
	}, func() time.Time { return shutdownNow }, zerolog.Nop())
	if err := sc.RehydrateCounters(context.Background()); err != nil {
		t.Fatalf("rehydrate counters: %v", err)
	}

	// Launch the reapers bound to a cancellable context, then shut down exactly as
	// App.Run does: cancel, then JOIN, all before the DB would be closed.
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

// TestStartRSSWarmerJoinsOnShutdown proves the RSS warmer reaper (#252) is wired
// through the same reap skeleton as every other maintenance goroutine: it must
// join the WaitGroup promptly once ctx is cancelled, exactly like
// TestBackgroundCleanupFlushesBeforeClose proves for the existing reapers.
// searchCache is left nil (caching not configured), the D3 gate that makes
// TickOnce a no-op — this test is about the goroutine's shutdown contract, not
// about a live warm firing.
func TestStartRSSWarmerJoinsOnShutdown(t *testing.T) {
	t.Parallel()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	reg := registry.New(db, loader.New(""), nil, catalog.All(), registry.WithLogger(zerolog.Nop()))

	bgCtx, bgCancel := context.WithCancel(context.Background())
	var bg sync.WaitGroup
	startRSSWarmer(bgCtx, &bg, reg, zerolog.Nop())

	done := make(chan struct{})
	go func() {
		bg.Wait()
		close(done)
	}()
	bgCancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RSS warmer goroutine did not join within 5s of ctx cancellation")
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
