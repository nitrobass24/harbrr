package registry

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
)

// openCacheDB opens and migrates a DB at path. Unlike testCache's :memory: DB, a file
// path survives across two handles, so a test can close it and reopen to simulate a
// restart. Closed via t.Cleanup (a manual Close before reopen is a harmless no-op).
func openCacheDB(t *testing.T, path string) *database.DB {
	t.Helper()
	db, err := database.Open(path)
	if err != nil {
		t.Fatalf("open db %q: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// newCacheOn builds a SearchCache over db with a fixed clock and caching enabled.
func newCacheOn(db *database.DB) *SearchCache {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	t := cacheTuning{enabled: true, ttl: ttlConfig{rss: time.Hour, keyword: time.Hour, thin: time.Hour}, cleanup: time.Hour}
	return NewSearchCache(db, t, func() time.Time { return now }, zerolog.Nop())
}

// insertInstanceSlug inserts a minimal enabled instance with the given (unique) slug,
// returning its id. (insertTestInstance hardcodes one slug, so it can't make two.)
func insertInstanceSlug(t *testing.T, db *database.DB, slug string) int64 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO indexer_instances (slug, definition_id, name, base_url, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 1, ?, ?)`, slug, "def", slug, "", now, now)
	if err != nil {
		t.Fatalf("insert instance %q: %v", slug, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

// seedCounters sets one instance's in-memory hit/miss/suppressed counters.
func seedCounters(c *SearchCache, instanceID, hits, misses, suppressed int64) {
	ic := c.counters(instanceID)
	ic.hits.Store(hits)
	ic.misses.Store(misses)
	ic.suppressed.Store(suppressed)
}

// TestCountersSurviveRestart is the headline durability test: counters flushed by one
// SearchCache are rehydrated by a second one over the SAME db file, and the global
// totals come back as the exact sum of the per-instance rows.
func TestCountersSurviveRestart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "harbrr.db")

	db1 := openCacheDB(t, path)
	id1 := insertInstanceSlug(t, db1, "one")
	id2 := insertInstanceSlug(t, db1, "two")

	sc1 := newCacheOn(db1)
	if err := sc1.RehydrateCounters(ctx); err != nil { // empty DB: arms the flush gate
		t.Fatalf("rehydrate #1: %v", err)
	}
	seedCounters(sc1, id1, 5, 2, 1)
	seedCounters(sc1, id2, 3, 9, 4)
	sc1.FlushCounters(ctx)
	if err := db1.Close(); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	// "Restart": a fresh cache over the same file.
	db2 := openCacheDB(t, path)
	sc2 := newCacheOn(db2)
	if err := sc2.RehydrateCounters(ctx); err != nil {
		t.Fatalf("rehydrate #2: %v", err)
	}

	stats, err := sc2.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Hits != 8 || stats.Misses != 11 || stats.BreakerSuppressed != 5 {
		t.Errorf("global = hits %d / misses %d / suppressed %d, want 8 / 11 / 5",
			stats.Hits, stats.Misses, stats.BreakerSuppressed)
	}
	if want := 8.0 / 19.0; stats.HitRatio != want {
		t.Errorf("global hitRatio = %v, want %v", stats.HitRatio, want)
	}

	byInst, err := sc2.StatsByInstance(ctx)
	if err != nil {
		t.Fatalf("StatsByInstance: %v", err)
	}
	got := map[int64]InstanceCacheStats{}
	for _, row := range byInst {
		got[row.InstanceID] = row
	}
	if r := got[id1]; r.Hits != 5 || r.Misses != 2 || r.BreakerSuppressed != 1 {
		t.Errorf("instance %d = %d/%d/%d, want 5/2/1", id1, r.Hits, r.Misses, r.BreakerSuppressed)
	}
	if r := got[id2]; r.Hits != 3 || r.Misses != 9 || r.BreakerSuppressed != 4 {
		t.Errorf("instance %d = %d/%d/%d, want 3/9/4", id2, r.Hits, r.Misses, r.BreakerSuppressed)
	}
}

// TestFlushCountersIdempotent proves absolute writes: flushing twice with no traffic
// in between leaves the stored totals unchanged.
func TestFlushCountersIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openCacheDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	id := insertInstanceSlug(t, db, "one")

	sc := newCacheOn(db)
	if err := sc.RehydrateCounters(ctx); err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	seedCounters(sc, id, 4, 6, 2)
	sc.FlushCounters(ctx)
	sc.FlushCounters(ctx) // no new traffic → must not change the row

	rows, err := database.CacheCountersStore{}.AllCounters(ctx, db)
	if err != nil {
		t.Fatalf("AllCounters: %v", err)
	}
	if len(rows) != 1 || rows[0].Hits != 4 || rows[0].Misses != 6 || rows[0].Suppressed != 2 {
		t.Errorf("rows = %+v, want a single 4/6/2 row", rows)
	}
}

// TestFlushCountersSelfHeals proves a cache whose boot rehydrate was missed still
// persists on flush while the DB is reachable: FlushCounters rehydrates on demand
// (arming the gate) and then writes the counters.
func TestFlushCountersSelfHeals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openCacheDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	id := insertInstanceSlug(t, db, "one")

	sc := newCacheOn(db) // deliberately NOT rehydrated at boot
	seedCounters(sc, id, 4, 5, 0)
	sc.FlushCounters(ctx) // must self-heal (rehydrate empty DB) then flush

	rows, err := database.CacheCountersStore{}.AllCounters(ctx, db)
	if err != nil {
		t.Fatalf("AllCounters: %v", err)
	}
	if len(rows) != 1 || rows[0].InstanceID != id || rows[0].Hits != 4 || rows[0].Misses != 5 {
		t.Errorf("rows = %+v, want a single 4/5/0 row for instance %d", rows, id)
	}
}

// TestFlushCountersSkippedWhenRehydrateFails proves the clobber guard survives the
// self-heal: when the on-demand rehydrate can't read the store, FlushCounters writes
// nothing — so a transient DB failure can't overwrite EXISTING stored totals with this
// session's partial in-memory counts.
func TestFlushCountersSkippedWhenRehydrateFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "harbrr.db")
	db := openCacheDB(t, path)
	id := insertInstanceSlug(t, db, "one")

	// A prior session's durable totals that must NOT be clobbered by a failed flush.
	if err := (database.CacheCountersStore{}).Upsert(ctx, db,
		database.CacheCounter{InstanceID: id, Hits: 100, Misses: 50, Suppressed: 10, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("seed stored row: %v", err)
	}

	sc := newCacheOn(db)               // not rehydrated; in-memory atomics start at zero
	seedCounters(sc, id, 9, 9, 9)      // this session's partial counts (differ from stored)
	if err := db.Close(); err != nil { // make the store unreadable
		t.Fatalf("close: %v", err)
	}
	sc.FlushCounters(ctx) // rehydrate fails -> must skip without clobbering the stored row

	db2 := openCacheDB(t, path) // reopen and confirm the original totals are intact
	rows, err := database.CacheCountersStore{}.AllCounters(ctx, db2)
	if err != nil {
		t.Fatalf("AllCounters: %v", err)
	}
	if len(rows) != 1 || rows[0].InstanceID != id ||
		rows[0].Hits != 100 || rows[0].Misses != 50 || rows[0].Suppressed != 10 {
		t.Errorf("rows = %+v, want the original 100/50/10 intact (flush must skip on failed rehydrate)", rows)
	}
}

// TestForgetInstance proves a deleted instance's counters are pruned: the globals drop
// by its counts (preserving global = sum of rows), it disappears from StatsByInstance,
// and a later FlushCounters writes no row for it.
func TestForgetInstance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openCacheDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	id1 := insertInstanceSlug(t, db, "keep")
	id2 := insertInstanceSlug(t, db, "drop")

	sc := newCacheOn(db)
	if err := sc.RehydrateCounters(ctx); err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	seedCounters(sc, id1, 5, 2, 0)
	seedCounters(sc, id2, 3, 4, 1)
	// Globals are the sum of both instances (as the real increment pairs would make them).
	sc.hits.Store(8)
	sc.misses.Store(6)
	sc.breakerSuppressed.Store(1)

	sc.ForgetInstance(id2)

	stats, err := sc.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Hits != 5 || stats.Misses != 2 || stats.BreakerSuppressed != 0 {
		t.Errorf("globals after forget = %d/%d/%d, want 5/2/0 (id2 subtracted)",
			stats.Hits, stats.Misses, stats.BreakerSuppressed)
	}

	byInst, err := sc.StatsByInstance(ctx)
	if err != nil {
		t.Fatalf("StatsByInstance: %v", err)
	}
	for _, row := range byInst {
		if row.InstanceID == id2 {
			t.Errorf("instance %d still present in StatsByInstance after forget", id2)
		}
	}

	// A flush now persists only the surviving instance — no doomed Upsert for id2.
	sc.FlushCounters(ctx)
	rows, err := database.CacheCountersStore{}.AllCounters(ctx, db)
	if err != nil {
		t.Fatalf("AllCounters: %v", err)
	}
	if len(rows) != 1 || rows[0].InstanceID != id1 {
		t.Errorf("rows = %+v, want only the kept instance %d", rows, id1)
	}
}

// TestFlushCountersSkipsDeletedInstance proves the per-row best-effort behavior: a
// counter for an instance deleted out from under the in-memory map fails its FK insert
// and is skipped, while every other instance still flushes.
func TestFlushCountersSkipsDeletedInstance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openCacheDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	id1 := insertInstanceSlug(t, db, "live")
	id2 := insertInstanceSlug(t, db, "doomed")

	sc := newCacheOn(db)
	if err := sc.RehydrateCounters(ctx); err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	seedCounters(sc, id1, 5, 0, 0)
	seedCounters(sc, id2, 7, 0, 0)

	// Delete id2 from under the in-memory counter (its instCounters entry lingers).
	if _, err := db.ExecContext(ctx, "DELETE FROM indexer_instances WHERE id = ?", id2); err != nil {
		t.Fatalf("delete instance: %v", err)
	}
	sc.FlushCounters(ctx) // must not panic or abort the live instance's flush

	rows, err := database.CacheCountersStore{}.AllCounters(ctx, db)
	if err != nil {
		t.Fatalf("AllCounters: %v", err)
	}
	if len(rows) != 1 || rows[0].InstanceID != id1 || rows[0].Hits != 5 {
		t.Errorf("rows = %+v, want only the live instance %d with 5 hits", rows, id1)
	}
}
