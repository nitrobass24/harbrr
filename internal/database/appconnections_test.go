package database_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

// sampleConnection builds a fully-populated connection referencing appID (nil is valid —
// the partial unique index on app_id only applies to non-NULL values) and bound to the
// given minted harbrr key id. HarbrrAPIKeyEncrypted carries an opaque (pretend-encrypted)
// blob — the repo stores it verbatim, encryption being the service's concern. Identity
// (base_url, the app's own api key) is no longer stored on this row at all (#269) — it
// lives on the referenced App.
func sampleConnection(appID *int64, harbrrKeyID int64, now time.Time) domain.AppConnection {
	return domain.AppConnection{
		Name:                  "Sonarr",
		Kind:                  domain.AppKindSonarr,
		AppID:                 appID,
		HarbrrAPIKeyID:        harbrrKeyID,
		HarbrrAPIKeyEncrypted: "enc(harbrr-key)",
		KeyID:                 "key-1",
		Enabled:               true,
		SyncLevel:             domain.SyncLevelFull,
		IndexScope:            domain.IndexScopeAll,
		FreeleechMode:         domain.FreeleechModeHonor,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}

// mintKey inserts an api_keys row so a connection's harbrr_api_key_id FK resolves.
func mintKey(t *testing.T, db *database.DB, name string) int64 {
	t.Helper()
	id, err := (database.APIKeys{}).Create(context.Background(), db,
		domain.APIKey{Name: name, KeyHash: "hash-" + name, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("mint key: %v", err)
	}
	return id
}

// mintApp inserts an apps row so a connection's app_id FK resolves.
func mintApp(t *testing.T, db *database.DB, kind, baseURL string) int64 {
	t.Helper()
	now := time.Now().UTC()
	id, err := (database.Apps{}).InsertApp(context.Background(), db, domain.App{
		Kind: kind, Name: kind, BaseURL: baseURL, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("mint app: %v", err)
	}
	return id
}

// TestAppConnectionAllKindsRoundTrip inserts and reads back a connection for EVERY
// supported app kind. This is the coverage gap that let #85 ship: the kind support in
// Go (validateKind/newDriver) was widened to lidarr/readarr/whisparr but the DB CHECK
// was not, so those three failed at INSERT. With the CHECK dropped (0008) every kind
// round-trips, and the freeleech_mode column persists.
func TestAppConnectionAllKindsRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AppConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	kinds := []string{
		domain.AppKindSonarr, domain.AppKindRadarr, domain.AppKindLidarr,
		domain.AppKindReadarr, domain.AppKindWhisparr, domain.AppKindQui,
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			appID := mintApp(t, db, kind, "http://"+kind+":9999")
			conn := sampleConnection(&appID, mintKey(t, db, "key-"+kind), now)
			conn.Kind = kind
			conn.Name = kind
			mode := domain.FreeleechModeHonor
			if kind == domain.AppKindQui {
				mode = domain.FreeleechModeBypass
			}
			conn.FreeleechMode = mode

			id, err := repo.InsertConnection(ctx, db, conn)
			if err != nil {
				t.Fatalf("InsertConnection(%s): %v", kind, err)
			}
			got, err := repo.GetConnection(ctx, db, id)
			if err != nil {
				t.Fatalf("GetConnection(%s): %v", kind, err)
			}
			if got.Kind != kind {
				t.Errorf("kind = %q, want %q", got.Kind, kind)
			}
			if got.FreeleechMode != mode {
				t.Errorf("freeleech_mode = %q, want %q", got.FreeleechMode, mode)
			}
		})
	}
}

func TestAppConnectionInsertGetList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AppConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	appID := mintApp(t, db, domain.AppKindSonarr, "http://sonarr:8989")
	keyID := mintKey(t, db, "sonarr")
	id, err := repo.InsertConnection(ctx, db, sampleConnection(&appID, keyID, now))
	if err != nil {
		t.Fatalf("InsertConnection: %v", err)
	}

	got, err := repo.GetConnection(ctx, db, id)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	switch {
	case got.Name != "Sonarr", got.Kind != domain.AppKindSonarr, got.AppID == nil || *got.AppID != appID:
		t.Errorf("identity round-trip mismatch: %+v", got)
	case got.HarbrrAPIKeyEncrypted != "enc(harbrr-key)":
		t.Errorf("secret round-trip mismatch: %+v", got)
	case got.HarbrrAPIKeyID != keyID || got.KeyID != "key-1":
		t.Errorf("key linkage mismatch: %+v", got)
	case !got.Enabled || got.SyncLevel != domain.SyncLevelFull || got.IndexScope != domain.IndexScopeAll:
		t.Errorf("flags round-trip mismatch: %+v", got)
	case got.LastSyncAt != nil:
		t.Errorf("last_sync_at should be nil on a fresh row, got %v", got.LastSyncAt)
	}

	list, err := repo.ListConnections(ctx, db)
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("ListConnections = %+v, want one row id=%d", list, id)
	}
}

func TestAppConnectionGetNotFound(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, ":memory:")
	_, err := (database.AppConnections{}).GetConnection(context.Background(), db, 999)
	if !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("GetConnection(missing) error = %v, want ErrNotFound", err)
	}
}

// TestAppConnectionUniqueAppID proves the partial UNIQUE(app_id) index (#269 — replacing
// the old UNIQUE(kind, base_url)) rejects a second row at the same non-NULL app_id, but
// exempts NULL (multiple host-less/unresolved rows are allowed).
func TestAppConnectionUniqueAppID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AppConnections{}
	now := time.Now().UTC()

	appID := mintApp(t, db, domain.AppKindSonarr, "http://sonarr:8989")
	conn := sampleConnection(&appID, mintKey(t, db, "a"), now)
	if _, err := repo.InsertConnection(ctx, db, conn); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	dup := sampleConnection(&appID, mintKey(t, db, "b"), now) // same app_id
	if _, err := repo.InsertConnection(ctx, db, dup); !database.IsUniqueViolation(err) {
		t.Fatalf("duplicate app_id error = %v, want unique violation", err)
	}

	null1 := sampleConnection(nil, mintKey(t, db, "c"), now)
	null1.Name = "null1"
	if _, err := repo.InsertConnection(ctx, db, null1); err != nil {
		t.Fatalf("first NULL app_id insert: %v", err)
	}
	null2 := sampleConnection(nil, mintKey(t, db, "d"), now)
	null2.Name = "null2"
	if _, err := repo.InsertConnection(ctx, db, null2); err != nil {
		t.Fatalf("second NULL app_id insert (must be exempt from the partial index): %v", err)
	}
}

func TestAppConnectionUpdateAndEnable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AppConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	id, err := repo.InsertConnection(ctx, db, sampleConnection(nil, mintKey(t, db, "k"), now))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	updated := sampleConnection(nil, 0, now)
	updated.ID = id
	updated.Name = "Sonarr 4K"
	updated.SyncLevel = domain.SyncLevelAddUpdate
	updated.IndexScope = domain.IndexScopeSelected
	updated.UpdatedAt = now.Add(time.Minute)
	if err := repo.UpdateConnection(ctx, db, updated); err != nil {
		t.Fatalf("UpdateConnection: %v", err)
	}

	got, _ := repo.GetConnection(ctx, db, id)
	if got.Name != "Sonarr 4K" || got.SyncLevel != domain.SyncLevelAddUpdate ||
		got.IndexScope != domain.IndexScopeSelected {
		t.Errorf("update not applied: %+v", got)
	}

	if err := repo.SetConnectionEnabled(ctx, db, id, false, now); err != nil {
		t.Fatalf("SetConnectionEnabled: %v", err)
	}
	if got, _ := repo.GetConnection(ctx, db, id); got.Enabled {
		t.Errorf("connection still enabled after disable")
	}

	if err := repo.SetConnectionEnabled(ctx, db, 404, true, now); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("SetConnectionEnabled(missing) = %v, want ErrNotFound", err)
	}
}

func TestAppConnectionRecordSyncResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AppConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	id, _ := repo.InsertConnection(ctx, db, sampleConnection(nil, mintKey(t, db, "k"), now))
	at := now.Add(time.Hour)
	if err := repo.RecordSyncResult(ctx, db, id, domain.SyncStatusPartial, "1 of 3 failed", at); err != nil {
		t.Fatalf("RecordSyncResult: %v", err)
	}
	got, _ := repo.GetConnection(ctx, db, id)
	if got.LastSyncStatus != domain.SyncStatusPartial || got.LastSyncError != "1 of 3 failed" {
		t.Errorf("sync result not recorded: %+v", got)
	}
	if got.LastSyncAt == nil || !got.LastSyncAt.Equal(at) {
		t.Errorf("last_sync_at = %v, want %v", got.LastSyncAt, at)
	}
}

func TestAppConnectionIndexerLedger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AppConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	connID, _ := repo.InsertConnection(ctx, db, sampleConnection(nil, mintKey(t, db, "k"), now))
	instID := insertInstance(t, db, "show-tracker")

	pushed := now.Add(time.Minute)
	row := domain.AppConnectionIndexer{
		ConnectionID: connID, InstanceID: instID, RemoteID: "7", Selected: true,
		PayloadHash: "h1", LastPushedAt: &pushed, LastPushStatus: domain.SyncStatusOK,
	}
	if err := repo.UpsertConnectionIndexer(ctx, db, row); err != nil {
		t.Fatalf("UpsertConnectionIndexer insert: %v", err)
	}
	// Upsert again with a new remote id + hash — must update in place, not duplicate.
	row.RemoteID, row.PayloadHash = "9", "h2"
	if err := repo.UpsertConnectionIndexer(ctx, db, row); err != nil {
		t.Fatalf("UpsertConnectionIndexer update: %v", err)
	}

	ledger, err := repo.ListConnectionIndexers(ctx, db, connID)
	if err != nil {
		t.Fatalf("ListConnectionIndexers: %v", err)
	}
	if len(ledger) != 1 {
		t.Fatalf("ledger len = %d, want 1 (upsert must not duplicate)", len(ledger))
	}
	if ledger[0].RemoteID != "9" || ledger[0].PayloadHash != "h2" {
		t.Errorf("upsert did not update in place: %+v", ledger[0])
	}

	if err := repo.DeleteConnectionIndexer(ctx, db, connID, instID); err != nil {
		t.Fatalf("DeleteConnectionIndexer: %v", err)
	}
	if ledger, _ := repo.ListConnectionIndexers(ctx, db, connID); len(ledger) != 0 {
		t.Errorf("ledger not empty after delete: %+v", ledger)
	}
}

func TestAppConnectionDeleteCascadesLedger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AppConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	connID, _ := repo.InsertConnection(ctx, db, sampleConnection(nil, mintKey(t, db, "k"), now))
	instID := insertInstance(t, db, "show-tracker")
	_ = repo.UpsertConnectionIndexer(ctx, db, domain.AppConnectionIndexer{
		ConnectionID: connID, InstanceID: instID, Selected: true,
	})

	if err := repo.DeleteConnection(ctx, db, connID); err != nil {
		t.Fatalf("DeleteConnection: %v", err)
	}
	if ledger, _ := repo.ListConnectionIndexers(ctx, db, connID); len(ledger) != 0 {
		t.Errorf("ledger rows survived parent delete: %+v", ledger)
	}
	if err := repo.DeleteConnection(ctx, db, connID); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("second DeleteConnection = %v, want ErrNotFound", err)
	}
}

func TestAppConnectionKeyRevocationSetsNull(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AppConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	keyID := mintKey(t, db, "sonarr")
	connID, _ := repo.InsertConnection(ctx, db, sampleConnection(nil, keyID, now))

	// Revoking the minted key out of band must null the link, not orphan-block the delete.
	if err := (database.APIKeys{}).Delete(ctx, db, keyID); err != nil {
		t.Fatalf("revoke key: %v", err)
	}
	got, err := repo.GetConnection(ctx, db, connID)
	if err != nil {
		t.Fatalf("GetConnection after revoke: %v", err)
	}
	if got.HarbrrAPIKeyID != 0 {
		t.Errorf("harbrr_api_key_id = %d after revoke, want 0 (SET NULL)", got.HarbrrAPIKeyID)
	}
}

// insertInstance creates a minimal indexer_instances row for ledger FK tests.
func insertInstance(t *testing.T, db *database.DB, slug string) int64 {
	t.Helper()
	now := time.Now().UTC()
	id, err := (database.Instances{}).Insert(context.Background(), db, domain.IndexerInstance{
		Slug: slug, DefinitionID: "def", Name: slug, Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	return id
}
