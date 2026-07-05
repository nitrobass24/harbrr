package database_test

import (
	"context"
	"errors"
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

	id, err := repo.InsertProfile(ctx, db, domain.SyncProfile{
		Name: "movies-only", Categories: []int{2000, 3030, 5000}, MinSeeders: 5,
		EnableRss: true, EnableAutomaticSearch: false, EnableInteractiveSearch: true,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertProfile: %v", err)
	}

	got, err := repo.GetProfile(ctx, db, id)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if got.Name != "movies-only" || got.MinSeeders != 5 ||
		got.EnableRss != true || got.EnableAutomaticSearch != false || got.EnableInteractiveSearch != true {
		t.Fatalf("GetProfile = %+v", got)
	}
	if !equalInts(got.Categories, []int{2000, 3030, 5000}) {
		t.Fatalf("GetProfile categories = %v, want [2000 3030 5000]", got.Categories)
	}

	list, err := repo.ListProfiles(ctx, db)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListProfiles = %v, %d rows", err, len(list))
	}

	got.Name, got.MinSeeders, got.Categories = "renamed", 0, nil
	got.EnableRss, got.UpdatedAt = false, now.Add(time.Minute)
	if err := repo.UpdateProfile(ctx, db, got); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	after, _ := repo.GetProfile(ctx, db, id)
	if after.Name != "renamed" || after.MinSeeders != 0 || after.EnableRss {
		t.Fatalf("after update = %+v", after)
	}
	// A nil category slice round-trips as an empty (non-nil) slice.
	if after.Categories == nil || len(after.Categories) != 0 {
		t.Fatalf("after update categories = %v, want empty slice", after.Categories)
	}

	if err := repo.DeleteProfile(ctx, db, id); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	if _, err := repo.GetProfile(ctx, db, id); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("GetProfile after delete = %v, want ErrNotFound", err)
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
		SyncLevel: domain.SyncLevelFull, IndexScope: domain.IndexScopeAll, FreeleechMode: domain.FreeleechModeHonor,
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

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
