package registry

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
)

// statsClock returns a fixed clock so RecordQuery's last-query timestamp is deterministic.
func statsClock() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) }

// newStats builds an IndexerStats over db with the fixed clock.
func newStats(db *database.DB) *IndexerStats {
	return newIndexerStats(db, statsClock, zerolog.Nop())
}

// TestIndexerStatsRecord proves RecordQuery accumulates queries + response time and
// stamps last-query, and RecordGrab counts grabs + stamps last-grab.
func TestIndexerStatsRecord(t *testing.T) {
	t.Parallel()

	db := openCacheDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	id := insertInstanceSlug(t, db, "one")
	s := newStats(db)

	s.RecordQuery(id, 100*time.Millisecond)
	s.RecordQuery(id, 300*time.Millisecond)
	s.RecordQuery(id, -5*time.Millisecond) // a clock-skew sample clamps to 0
	s.RecordGrab(id)

	queries, grabs, respTotal, lastQuery, lastGrab := s.snapshot(id)
	if queries != 3 || grabs != 1 || respTotal != 400 {
		t.Errorf("snapshot = q %d / g %d / ms %d, want 3 / 1 / 400", queries, grabs, respTotal)
	}
	if !lastQuery.Equal(statsClock()) {
		t.Errorf("lastQuery = %v, want %v", lastQuery, statsClock())
	}
	if !lastGrab.Equal(statsClock()) {
		t.Errorf("lastGrab = %v, want %v", lastGrab, statsClock())
	}

	// An instance with no recorded traffic snapshots to zeroes / zero times.
	q, g, ms, lq, lg := s.snapshot(99)
	if q != 0 || g != 0 || ms != 0 || !lq.IsZero() || !lg.IsZero() {
		t.Errorf("empty snapshot = %d/%d/%d/%v/%v, want all zero", q, g, ms, lq, lg)
	}
}

// TestIndexerStatsSurviveRestart is the durability test: counters flushed by one
// IndexerStats are rehydrated by a second one over the SAME db file.
func TestIndexerStatsSurviveRestart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "harbrr.db")

	db1 := openCacheDB(t, path)
	id := insertInstanceSlug(t, db1, "one")

	s1 := newStats(db1)
	if err := s1.RehydrateCounters(ctx); err != nil { // empty DB: arms the flush gate
		t.Fatalf("rehydrate #1: %v", err)
	}
	s1.RecordQuery(id, 200*time.Millisecond)
	s1.RecordQuery(id, 200*time.Millisecond)
	s1.RecordGrab(id)
	s1.FlushCounters(ctx)
	if err := db1.Close(); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	db2 := openCacheDB(t, path)
	s2 := newStats(db2)
	if err := s2.RehydrateCounters(ctx); err != nil {
		t.Fatalf("rehydrate #2: %v", err)
	}
	queries, grabs, respTotal, lastQuery, lastGrab := s2.snapshot(id)
	if queries != 2 || grabs != 1 || respTotal != 400 {
		t.Errorf("after restart = q %d / g %d / ms %d, want 2 / 1 / 400", queries, grabs, respTotal)
	}
	if !lastQuery.Equal(statsClock()) || !lastGrab.Equal(statsClock()) {
		t.Errorf("timestamps after restart = %v / %v, want %v", lastQuery, lastGrab, statsClock())
	}
}

// TestIndexerStatsFlushSelfHeals proves a stats layer whose boot rehydrate was missed
// still persists on flush (rehydrates on demand, arming the gate, then writes).
func TestIndexerStatsFlushSelfHeals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openCacheDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	id := insertInstanceSlug(t, db, "one")

	s := newStats(db) // deliberately NOT rehydrated at boot
	s.RecordQuery(id, 50*time.Millisecond)
	s.FlushCounters(ctx) // must self-heal then flush

	rows, err := database.IndexerStatCountersStore{}.AllCounters(ctx, db)
	if err != nil {
		t.Fatalf("AllCounters: %v", err)
	}
	if len(rows) != 1 || rows[0].InstanceID != id || rows[0].Queries != 1 || rows[0].ResponseMsTotal != 50 {
		t.Errorf("rows = %+v, want a single 1-query/50ms row for instance %d", rows, id)
	}
}

// TestIndexerStatsFlushSkippedWhenRehydrateFails proves the clobber guard: when the
// on-demand rehydrate can't read the store, FlushCounters writes nothing, so a transient
// DB failure can't overwrite EXISTING stored totals with this session's partial counts.
func TestIndexerStatsFlushSkippedWhenRehydrateFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "harbrr.db")
	db := openCacheDB(t, path)
	id := insertInstanceSlug(t, db, "one")

	// A prior session's durable totals that must NOT be clobbered.
	if err := (database.IndexerStatCountersStore{}).Upsert(ctx, db,
		database.IndexerStatCounter{InstanceID: id, Queries: 100, Grabs: 50, ResponseMsTotal: 9000, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("seed stored row: %v", err)
	}

	s := newStats(db)                   // not rehydrated; atomics start at zero
	s.RecordQuery(id, time.Millisecond) // this session's partial count
	if err := db.Close(); err != nil {  // make the store unreadable
		t.Fatalf("close: %v", err)
	}
	s.FlushCounters(ctx) // rehydrate fails -> must skip without clobbering

	db2 := openCacheDB(t, path)
	rows, err := database.IndexerStatCountersStore{}.AllCounters(ctx, db2)
	if err != nil {
		t.Fatalf("AllCounters: %v", err)
	}
	if len(rows) != 1 || rows[0].Queries != 100 || rows[0].Grabs != 50 || rows[0].ResponseMsTotal != 9000 {
		t.Errorf("rows = %+v, want the original 100/50/9000 intact (flush must skip)", rows)
	}
}

// TestIndexerStatsForgetInstance proves a deleted instance's in-memory counters are
// dropped and a later flush writes no row for it.
func TestIndexerStatsForgetInstance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openCacheDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	id1 := insertInstanceSlug(t, db, "keep")
	id2 := insertInstanceSlug(t, db, "drop")

	s := newStats(db)
	if err := s.RehydrateCounters(ctx); err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	s.RecordQuery(id1, time.Millisecond)
	s.RecordQuery(id2, time.Millisecond)

	s.ForgetInstance(id2)
	if q, _, _, _, _ := s.snapshot(id2); q != 0 {
		t.Errorf("forgotten instance still snapshots %d queries, want 0", q)
	}

	s.FlushCounters(ctx)
	rows, err := database.IndexerStatCountersStore{}.AllCounters(ctx, db)
	if err != nil {
		t.Fatalf("AllCounters: %v", err)
	}
	if len(rows) != 1 || rows[0].InstanceID != id1 {
		t.Errorf("rows = %+v, want only the kept instance %d", rows, id1)
	}
}

// TestIndexerStatsRehydrateMaxTimestamp proves the store-if-greater fold: a stored
// last-query time older than the session's current value never rewinds it.
func TestIndexerStatsRehydrateMaxTimestamp(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openCacheDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
	id := insertInstanceSlug(t, db, "one")

	// Seed a stored row whose last_query_at is OLDER than the session clock.
	older := statsClock().Add(-time.Hour)
	if err := (database.IndexerStatCountersStore{}).Upsert(ctx, db,
		database.IndexerStatCounter{InstanceID: id, Queries: 1, LastQueryAt: older, UpdatedAt: older}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s := newStats(db)
	s.RecordQuery(id, time.Millisecond) // session advances last-query to statsClock()
	if err := s.RehydrateCounters(ctx); err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	// Counts add (1 stored + 1 session), last-query stays the newer session time.
	queries, _, _, lastQuery, _ := s.snapshot(id)
	if queries != 2 {
		t.Errorf("queries = %d, want 2 (stored + session)", queries)
	}
	if !lastQuery.Equal(statsClock()) {
		t.Errorf("lastQuery = %v, want %v (newer session time not rewound)", lastQuery, statsClock())
	}
}
