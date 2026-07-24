package database_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

func TestSyncProfileRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.SyncProfiles{}
	now := time.Now().UTC().Truncate(time.Second)

	inst1 := insertInstance(t, db, "one")
	inst2 := insertInstance(t, db, "two")

	id, err := repo.InsertProfile(ctx, db, domain.SyncProfile{
		Name: "movies-only", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertProfile: %v", err)
	}
	if err := repo.ReplaceProfileIndexers(ctx, db, id, []int64{inst1, inst2}); err != nil {
		t.Fatalf("ReplaceProfileIndexers: %v", err)
	}

	got, err := repo.GetProfile(ctx, db, id)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if got.Name != "movies-only" {
		t.Fatalf("GetProfile.Name = %q, want movies-only", got.Name)
	}
	if !slices.Equal(got.IndexerIDs, []int64{inst1, inst2}) {
		t.Fatalf("GetProfile.IndexerIDs = %v, want [%d %d]", got.IndexerIDs, inst1, inst2)
	}

	list, err := repo.ListProfiles(ctx, db)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListProfiles = %v, %d rows", err, len(list))
	}
	if !slices.Equal(list[0].IndexerIDs, []int64{inst1, inst2}) {
		t.Fatalf("ListProfiles[0].IndexerIDs = %v, want [%d %d]", list[0].IndexerIDs, inst1, inst2)
	}

	got.Name, got.UpdatedAt = "renamed", now.Add(time.Minute)
	if err := repo.UpdateProfile(ctx, db, got); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	// Present-empty clears the selection (every compatible indexer).
	if err := repo.ReplaceProfileIndexers(ctx, db, id, nil); err != nil {
		t.Fatalf("ReplaceProfileIndexers(clear): %v", err)
	}
	after, _ := repo.GetProfile(ctx, db, id)
	if after.Name != "renamed" {
		t.Fatalf("after update = %+v", after)
	}
	// A cleared selection round-trips as an empty (non-nil) slice.
	if after.IndexerIDs == nil || len(after.IndexerIDs) != 0 {
		t.Fatalf("after clear IndexerIDs = %v, want empty slice", after.IndexerIDs)
	}

	if err := repo.DeleteProfile(ctx, db, id); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	if _, err := repo.GetProfile(ctx, db, id); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("GetProfile after delete = %v, want ErrNotFound", err)
	}
}

// TestSyncProfileIndexersCascadeOnInstanceDelete proves sync_profile_indexers'
// FK ON DELETE CASCADE against indexer_instances: deleting a selected instance
// drops it from the profile's set without touching the profile row itself.
func TestSyncProfileIndexersCascadeOnInstanceDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.SyncProfiles{}
	now := time.Now().UTC().Truncate(time.Second)

	inst1 := insertInstance(t, db, "keep")
	inst2 := insertInstance(t, db, "drop-me")
	id, err := repo.InsertProfile(ctx, db, domain.SyncProfile{Name: "p", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("InsertProfile: %v", err)
	}
	if err := repo.ReplaceProfileIndexers(ctx, db, id, []int64{inst1, inst2}); err != nil {
		t.Fatalf("ReplaceProfileIndexers: %v", err)
	}

	if err := (database.Instances{}).Delete(ctx, db, "drop-me"); err != nil {
		t.Fatalf("delete instance: %v", err)
	}

	ids, err := repo.ListProfileIndexers(ctx, db, id)
	if err != nil {
		t.Fatalf("ListProfileIndexers: %v", err)
	}
	if !slices.Equal(ids, []int64{inst1}) {
		t.Fatalf("ListProfileIndexers after instance delete = %v, want [%d]", ids, inst1)
	}
}

func TestSyncProfileNameUnique(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.SyncProfiles{}
	now := time.Now().UTC().Truncate(time.Second)

	if _, err := repo.InsertProfile(ctx, db, domain.SyncProfile{Name: "dup", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("first InsertProfile: %v", err)
	}
	_, err := repo.InsertProfile(ctx, db, domain.SyncProfile{Name: "dup", CreatedAt: now, UpdatedAt: now})
	if !database.IsUniqueViolation(err) {
		t.Fatalf("second InsertProfile err = %v, want a unique violation", err)
	}
}

// TestConnectionSyncProfileFKSetNull covers the app_connections.sync_profile_id FK
// round-trip and the ON DELETE SET NULL behavior (foreign_keys pragma is ON).
func TestConnectionSyncProfileFKSetNull(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	profiles := database.SyncProfiles{}
	conns := database.AppConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	profileID, err := profiles.InsertProfile(ctx, db, domain.SyncProfile{Name: "p", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("InsertProfile: %v", err)
	}
	connID, err := conns.InsertConnection(ctx, db, domain.AppConnection{
		Name: "Sonarr", Kind: domain.AppKindSonarr, BaseURL: "http://sonarr:8989", Enabled: true,
		SyncLevel: domain.SyncLevelFull, FreeleechMode: domain.FreeleechModeHonor,
		SyncProfileID: &profileID, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertConnection: %v", err)
	}
	got, _ := conns.GetConnection(ctx, db, connID)
	if got.SyncProfileID == nil || *got.SyncProfileID != profileID {
		t.Fatalf("sync_profile_id after insert = %v, want %d", got.SyncProfileID, profileID)
	}

	// Deleting the profile nulls the connection reference (ON DELETE SET NULL).
	if err := profiles.DeleteProfile(ctx, db, profileID); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	got, _ = conns.GetConnection(ctx, db, connID)
	if got.SyncProfileID != nil {
		t.Fatalf("sync_profile_id = %v after profile delete, want nil (ON DELETE SET NULL)", *got.SyncProfileID)
	}
}
