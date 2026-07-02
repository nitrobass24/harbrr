package database_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
)

func TestAppSettingsSetGet(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()
	store := database.AppSettings{}
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	// Missing key: not an error, found=false.
	if _, found, err := store.Get(ctx, db, "cache.rss_ttl"); err != nil || found {
		t.Fatalf("Get missing = (found %v, err %v), want (false, nil)", found, err)
	}

	if err := store.Set(ctx, db, "cache.rss_ttl", "10m", now); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, found, err := store.Get(ctx, db, "cache.rss_ttl")
	if err != nil || !found || v != "10m" {
		t.Fatalf("Get = (%q, %v, %v), want (\"10m\", true, nil)", v, found, err)
	}

	// Upsert overwrites in place.
	if err := store.Set(ctx, db, "cache.rss_ttl", "7m", now.Add(time.Minute)); err != nil {
		t.Fatalf("Set (upsert): %v", err)
	}
	if v, _, _ := store.Get(ctx, db, "cache.rss_ttl"); v != "7m" {
		t.Errorf("after upsert Get = %q, want 7m", v)
	}
}

func TestAppSettingsGetAll(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()
	store := database.AppSettings{}
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

	if all, err := store.GetAll(ctx, db); err != nil || len(all) != 0 {
		t.Fatalf("GetAll empty = (%v, %v), want (empty, nil)", all, err)
	}

	for k, v := range map[string]string{"cache.enabled": "true", "cache.keyword_ttl": "30m"} {
		if err := store.Set(ctx, db, k, v, now); err != nil {
			t.Fatalf("Set %q: %v", k, err)
		}
	}
	all, err := store.GetAll(ctx, db)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if all["cache.enabled"] != "true" || all["cache.keyword_ttl"] != "30m" || len(all) != 2 {
		t.Errorf("GetAll = %v, want the two seeded keys", all)
	}
}
