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
// ran prior migrations (the deployed Phase-5 case): reopening and re-migrating is a
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
