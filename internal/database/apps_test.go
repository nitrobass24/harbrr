package database_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

// sampleApp builds a fully-populated App identity (ADR 0004). The credential
// columns are left empty — Apps.InsertApp always writes them empty (the two-phase
// insert-then-seal write, so the credential's AAD can bind the freshly-minted id);
// a test proving the round trip calls SetAppSecret separately.
func sampleApp(now time.Time) domain.App {
	return domain.App{
		Kind:      domain.AppKindQui,
		Name:      "qui",
		BaseURL:   "http://qui:7476",
		HarbrrURL: "http://harbrr:7575",
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// insertApp inserts a and returns it with the assigned id.
func insertApp(t *testing.T, db *database.DB, a domain.App) domain.App {
	t.Helper()
	id, err := (database.Apps{}).InsertApp(context.Background(), db, a)
	if err != nil {
		t.Fatalf("InsertApp: %v", err)
	}
	a.ID = id
	return a
}

func TestAppInsertSetSecretRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.Apps{}
	now := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)

	app := insertApp(t, db, sampleApp(now))
	if app.ID == 0 {
		t.Fatal("InsertApp returned id 0")
	}

	// A fresh insert carries an empty sealed credential.
	got, err := repo.GetApp(ctx, db, app.ID)
	if err != nil {
		t.Fatalf("GetApp: %v", err)
	}
	if got.APIKeyEncrypted != "" || got.KeyID != "" {
		t.Errorf("fresh insert APIKeyEncrypted/KeyID = %q/%q, want empty", got.APIKeyEncrypted, got.KeyID)
	}

	if err := repo.SetAppSecret(ctx, db, app.ID, "enc(key)", "key-1"); err != nil {
		t.Fatalf("SetAppSecret: %v", err)
	}
	got, err = repo.GetApp(ctx, db, app.ID)
	if err != nil {
		t.Fatalf("GetApp after SetAppSecret: %v", err)
	}
	if got.APIKeyEncrypted != "enc(key)" || got.KeyID != "key-1" {
		t.Errorf("APIKeyEncrypted/KeyID = %q/%q, want enc(key)/key-1", got.APIKeyEncrypted, got.KeyID)
	}
	if got.Kind != app.Kind || got.Name != app.Name || got.BaseURL != app.BaseURL || got.HarbrrURL != app.HarbrrURL {
		t.Errorf("round-tripped identity = %+v, want matching %+v", got, app)
	}
}

func TestAppSetAppSecretNotFound(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, ":memory:")
	repo := database.Apps{}
	if err := repo.SetAppSecret(context.Background(), db, 999, "enc", "key"); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("err = %v, want database.ErrNotFound", err)
	}
}

func TestAppGetNotFound(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, ":memory:")
	repo := database.Apps{}
	if _, err := repo.GetApp(context.Background(), db, 999); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("err = %v, want database.ErrNotFound", err)
	}
}

func TestAppGetByIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.Apps{}
	now := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	app := insertApp(t, db, sampleApp(now))

	got, err := repo.GetAppByIdentity(ctx, db, app.Kind, app.BaseURL)
	if err != nil {
		t.Fatalf("GetAppByIdentity (hit): %v", err)
	}
	if got.ID != app.ID {
		t.Errorf("ID = %d, want %d", got.ID, app.ID)
	}

	if _, err := repo.GetAppByIdentity(ctx, db, app.Kind, "http://nope.invalid"); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("err (miss) = %v, want database.ErrNotFound", err)
	}
}

func TestAppListOrdering(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.Apps{}
	now := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)

	a := insertApp(t, db, domain.App{Kind: domain.AppKindQui, Name: "a", BaseURL: "http://a.invalid", CreatedAt: now, UpdatedAt: now})
	b := insertApp(t, db, domain.App{Kind: domain.AppKindSonarr, Name: "b", BaseURL: "http://b.invalid", CreatedAt: now, UpdatedAt: now})
	c := insertApp(t, db, domain.App{Kind: domain.AppKindRadarr, Name: "c", BaseURL: "http://c.invalid", CreatedAt: now, UpdatedAt: now})

	list, err := repo.ListApps(ctx, db)
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	want := []int64{a.ID, b.ID, c.ID}
	if len(list) != len(want) {
		t.Fatalf("len(list) = %d, want %d", len(list), len(want))
	}
	for i, id := range want {
		if list[i].ID != id {
			t.Errorf("list[%d].ID = %d, want %d (ORDER BY id)", i, list[i].ID, id)
		}
	}
}

func TestAppUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.Apps{}
	now := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	app := insertApp(t, db, sampleApp(now))

	app.Name = "renamed"
	app.BaseURL = "http://renamed.invalid"
	app.Username = "user"
	app.HarbrrURL = "http://harbrr2:7575"
	app.Enabled = false
	app.APIKeyEncrypted = "enc(rotated)"
	app.KeyID = "key-2"
	app.UpdatedAt = now.Add(time.Hour)

	if err := repo.UpdateApp(ctx, db, app); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	got, err := repo.GetApp(ctx, db, app.ID)
	if err != nil {
		t.Fatalf("GetApp: %v", err)
	}
	if got.Name != "renamed" || got.BaseURL != "http://renamed.invalid" || got.Username != "user" ||
		got.HarbrrURL != "http://harbrr2:7575" || got.Enabled || got.APIKeyEncrypted != "enc(rotated)" || got.KeyID != "key-2" {
		t.Errorf("after UpdateApp = %+v, want the patched fields applied", got)
	}
	// Kind is immutable — excluded from the UPDATE's SET list.
	if got.Kind != app.Kind {
		t.Errorf("Kind = %q, want unchanged %q", got.Kind, app.Kind)
	}
}

func TestAppUpdateNotFound(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, ":memory:")
	repo := database.Apps{}
	err := repo.UpdateApp(context.Background(), db, domain.App{ID: 999, Kind: domain.AppKindQui, Name: "n", UpdatedAt: time.Now().UTC()})
	if !errors.Is(err, database.ErrNotFound) {
		t.Errorf("err = %v, want database.ErrNotFound", err)
	}
}

func TestAppDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.Apps{}
	now := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	app := insertApp(t, db, sampleApp(now))

	if err := repo.DeleteApp(ctx, db, app.ID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	if _, err := repo.GetApp(ctx, db, app.ID); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("Get after delete = %v, want database.ErrNotFound", err)
	}
}

func TestAppDeleteNotFound(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, ":memory:")
	repo := database.Apps{}
	if err := repo.DeleteApp(context.Background(), db, 999); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("err = %v, want database.ErrNotFound", err)
	}
}

func TestAppUniqueKindBaseURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.Apps{}
	now := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	insertApp(t, db, sampleApp(now))

	_, err := repo.InsertApp(ctx, db, sampleApp(now))
	if err == nil {
		t.Fatal("second insert with same (kind, base_url) succeeded, want a UNIQUE violation")
	}
	if !database.IsUniqueViolation(err) {
		t.Errorf("IsUniqueViolation(%v) = false, want true", err)
	}
}

// TestAppCountReferences inserts one row per surface table (app_connections,
// announce_connections, download_clients) with app_id set to the app under test —
// plus a second download_clients row, to prove the counts are per-surface, not just
// a boolean — and asserts CountAppReferences reports each surface's count and Any().
func TestAppCountReferences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.Apps{}
	now := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	app := insertApp(t, db, sampleApp(now))

	refs, err := repo.CountAppReferences(ctx, db, app.ID)
	if err != nil {
		t.Fatalf("CountAppReferences (unreferenced): %v", err)
	}
	if refs.Any() {
		t.Errorf("refs = %+v, want none referenced", refs)
	}

	if _, err := (database.AppConnections{}).InsertConnection(ctx, db, domain.AppConnection{
		Name: "sonarr-1", Kind: domain.AppKindSonarr, AppID: &app.ID,
		SyncLevel: domain.SyncLevelFull, FreeleechMode: domain.FreeleechModeHonor,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert app_connections: %v", err)
	}
	if _, err := (database.AnnounceConnections{}).InsertAnnounceConnection(ctx, db, domain.AnnounceConnection{
		Name: "announce-1", Kind: domain.AppKindQui, AppID: &app.ID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert announce_connections: %v", err)
	}
	if _, err := (database.DownloadClients{}).InsertDownloadClient(ctx, db, domain.DownloadClient{
		Name: "dl-1", Kind: domain.DownloadClientKindQBittorrent, AppID: &app.ID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert download_clients (1): %v", err)
	}
	if _, err := (database.DownloadClients{}).InsertDownloadClient(ctx, db, domain.DownloadClient{
		Name: "dl-2", Kind: domain.DownloadClientKindQBittorrent, AppID: &app.ID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert download_clients (2): %v", err)
	}

	refs, err = repo.CountAppReferences(ctx, db, app.ID)
	if err != nil {
		t.Fatalf("CountAppReferences: %v", err)
	}
	want := database.AppReferences{AppConnections: 1, Announce: 1, Download: 2}
	if refs != want {
		t.Errorf("refs = %+v, want %+v", refs, want)
	}
	if !refs.Any() {
		t.Error("Any() = false, want true")
	}
}
