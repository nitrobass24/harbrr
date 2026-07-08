package database_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

func seedInstance(t *testing.T, db *database.DB, slug string) int64 {
	t.Helper()
	id, err := database.Instances{}.Insert(context.Background(), db, domain.IndexerInstance{
		Slug: slug, DefinitionID: "def", Name: slug, Enabled: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("insert instance %q: %v", slug, err)
	}
	return id
}

func TestHealthRecordAndRecent(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, filepath.Join(t.TempDir(), "health.db"))
	ctx := context.Background()
	id := seedInstance(t, db, "tt")
	h := database.Health{}

	base := time.Date(2026, time.June, 14, 12, 0, 0, 0, time.UTC)
	events := []domain.IndexerHealthEvent{
		{InstanceID: id, Kind: domain.HealthAuthFailure, Detail: "bad creds", OccurredAt: base},
		{InstanceID: id, Kind: domain.HealthRateLimited, Detail: "429", OccurredAt: base.Add(time.Minute)},
		{InstanceID: id, Kind: domain.HealthParseError, Detail: "", OccurredAt: base.Add(2 * time.Minute)},
	}
	for _, e := range events {
		if err := h.Record(ctx, db, e); err != nil {
			t.Fatalf("record %s: %v", e.Kind, err)
		}
	}

	got, err := h.Recent(ctx, db, id, 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	// Newest first, with the timestamp round-tripping.
	if got[0].Kind != domain.HealthParseError {
		t.Errorf("newest kind = %q, want parse_error", got[0].Kind)
	}
	if !got[0].OccurredAt.Equal(base.Add(2 * time.Minute)) {
		t.Errorf("occurred_at round-trip = %v, want %v", got[0].OccurredAt, base.Add(2*time.Minute))
	}
	lim, err := h.Recent(ctx, db, id, 1)
	if err != nil {
		t.Fatalf("recent limit=1: %v", err)
	}
	if len(lim) != 1 {
		t.Fatalf("limit=1 returned %d events", len(lim))
	}
}

// TestHealthCounts proves Counts aggregates one instance's events by kind and reports
// the newest failure time across all kinds; an instance with no events is the zero
// struct.
func TestHealthCounts(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, filepath.Join(t.TempDir(), "health.db"))
	ctx := context.Background()
	id := seedInstance(t, db, "tt")
	empty := seedInstance(t, db, "empty")
	h := database.Health{}

	base := time.Date(2026, time.June, 14, 12, 0, 0, 0, time.UTC)
	newest := base.Add(5 * time.Minute)
	events := []domain.IndexerHealthEvent{
		{InstanceID: id, Kind: domain.HealthAuthFailure, OccurredAt: base},
		{InstanceID: id, Kind: domain.HealthAuthFailure, OccurredAt: base.Add(time.Minute)},
		{InstanceID: id, Kind: domain.HealthRateLimited, OccurredAt: base.Add(2 * time.Minute)},
		{InstanceID: id, Kind: domain.HealthAntiBot, OccurredAt: newest},
	}
	for _, e := range events {
		if err := h.Record(ctx, db, e); err != nil {
			t.Fatalf("record %s: %v", e.Kind, err)
		}
	}

	got, err := h.Counts(ctx, db, id)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if got.AuthFailure != 2 || got.RateLimited != 1 || got.ParseError != 0 || got.AntiBot != 1 {
		t.Errorf("counts = %+v, want auth 2 / rate 1 / parse 0 / antibot 1", got)
	}
	if !got.LastFailureAt.Equal(newest) {
		t.Errorf("lastFailureAt = %v, want %v (newest across kinds)", got.LastFailureAt, newest)
	}

	// An instance with no events yields the zero struct.
	emptyCounts, err := h.Counts(ctx, db, empty)
	if err != nil {
		t.Fatalf("Counts(empty): %v", err)
	}
	if (emptyCounts != database.HealthCounts{}) {
		t.Errorf("empty counts = %+v, want zero struct", emptyCounts)
	}
}

// TestHealthAllCounts proves AllCounts aggregates every instance in one pass, keyed by
// instance id, and omits instances with no events.
func TestHealthAllCounts(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, filepath.Join(t.TempDir(), "health.db"))
	ctx := context.Background()
	id1 := seedInstance(t, db, "one")
	id2 := seedInstance(t, db, "two")
	seedInstance(t, db, "none") // no events -> absent from the map
	h := database.Health{}

	base := time.Date(2026, time.June, 14, 12, 0, 0, 0, time.UTC)
	for _, e := range []domain.IndexerHealthEvent{
		{InstanceID: id1, Kind: domain.HealthAuthFailure, OccurredAt: base},
		{InstanceID: id1, Kind: domain.HealthParseError, OccurredAt: base.Add(time.Minute)},
		{InstanceID: id2, Kind: domain.HealthRateLimited, OccurredAt: base.Add(2 * time.Minute)},
	} {
		if err := h.Record(ctx, db, e); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	got, err := h.AllCounts(ctx, db)
	if err != nil {
		t.Fatalf("AllCounts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("AllCounts len = %d, want 2 (the empty instance omitted)", len(got))
	}
	if c := got[id1]; c.AuthFailure != 1 || c.ParseError != 1 {
		t.Errorf("id1 counts = %+v, want auth 1 / parse 1", c)
	}
	if c := got[id2]; c.RateLimited != 1 {
		t.Errorf("id2 counts = %+v, want rate 1", c)
	}
}

// TestHealthDeleteBefore proves the retention purge removes only events strictly older
// than the cutoff: it returns the count deleted, leaves newer rows intact, and — per the
// `<` semantics — keeps an event landing exactly on the cutoff boundary.
func TestHealthDeleteBefore(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, filepath.Join(t.TempDir(), "health.db"))
	ctx := context.Background()
	id := seedInstance(t, db, "tt")
	h := database.Health{}

	cutoff := time.Date(2026, time.June, 14, 12, 0, 0, 0, time.UTC)
	events := []domain.IndexerHealthEvent{
		{InstanceID: id, Kind: domain.HealthAuthFailure, OccurredAt: cutoff.Add(-48 * time.Hour)}, // older -> deleted
		{InstanceID: id, Kind: domain.HealthRateLimited, OccurredAt: cutoff.Add(-time.Minute)},    // older -> deleted
		{InstanceID: id, Kind: domain.HealthParseError, OccurredAt: cutoff},                       // exactly on boundary -> kept (`<`)
		{InstanceID: id, Kind: domain.HealthAntiBot, OccurredAt: cutoff.Add(time.Minute)},         // newer -> kept
	}
	for _, e := range events {
		if err := h.Record(ctx, db, e); err != nil {
			t.Fatalf("record %s: %v", e.Kind, err)
		}
	}

	deleted, err := h.DeleteBefore(ctx, db, cutoff)
	if err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2 (only the two strictly older than cutoff)", deleted)
	}

	// The survivors are exactly the boundary and newer events, newest first.
	got, err := h.Recent(ctx, db, id, 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("survivors = %d, want 2", len(got))
	}
	if got[0].Kind != domain.HealthAntiBot || got[1].Kind != domain.HealthParseError {
		t.Errorf("survivors = [%q, %q], want [anti_bot, parse_error] (boundary kept)", got[0].Kind, got[1].Kind)
	}

	// A second purge at the same cutoff is a no-op (nothing left older than it).
	again, err := h.DeleteBefore(ctx, db, cutoff)
	if err != nil {
		t.Fatalf("DeleteBefore (repeat): %v", err)
	}
	if again != 0 {
		t.Errorf("second delete = %d, want 0", again)
	}
}

// TestHealthCascadeOnInstanceDelete proves the FK ON DELETE CASCADE actually fires
// (foreign_keys is ON): deleting the parent instance removes its health rows.
func TestHealthCascadeOnInstanceDelete(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, filepath.Join(t.TempDir(), "health.db"))
	ctx := context.Background()
	id := seedInstance(t, db, "tt")
	h := database.Health{}
	if err := h.Record(ctx, db, domain.IndexerHealthEvent{InstanceID: id, Kind: domain.HealthAntiBot, OccurredAt: time.Now()}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := (database.Instances{}).Delete(ctx, db, "tt"); err != nil {
		t.Fatalf("delete instance: %v", err)
	}
	got, err := h.Recent(ctx, db, id, 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("health rows survived instance delete (CASCADE inert): %d rows", len(got))
	}
}

// TestHealthMigrationOnExistingDB proves 0002 applies cleanly on a DB that already
// ran prior migrations (the deployed-instance case): reopening and re-migrating is a
// no-op and the health data persists.
func TestHealthMigrationOnExistingDB(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "deployed.db")
	ctx := context.Background()

	db := openMigrated(t, path)
	id := seedInstance(t, db, "tt")
	if err := (database.Health{}).Record(ctx, db, domain.IndexerHealthEvent{InstanceID: id, Kind: domain.HealthAuthFailure, OccurredAt: time.Now()}); err != nil {
		t.Fatalf("record: %v", err)
	}

	// Reopen the same file and re-run migrations — must be a safe no-op.
	db2 := openMigrated(t, path)
	got, err := (database.Health{}).Recent(ctx, db2, id, 10)
	if err != nil {
		t.Fatalf("recent after reopen: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("health row did not survive reopen + re-migrate: %d rows", len(got))
	}
}
