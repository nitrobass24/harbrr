package database_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

// sampleConnection builds a fully-populated connection bound to the given minted
// harbrr key id. Both secret columns carry opaque (pretend-encrypted) blobs — the
// repo stores them verbatim, encryption being the service's concern.
func sampleConnection(harbrrKeyID int64, now time.Time) domain.AppConnection {
	return domain.AppConnection{
		Name:                  "Sonarr",
		Kind:                  domain.AppKindSonarr,
		BaseURL:               "http://sonarr:8989",
		APIKeyEncrypted:       "enc(app-key)",
		HarbrrURL:             "http://harbrr:8787",
		HarbrrAPIKeyID:        harbrrKeyID,
		HarbrrAPIKeyEncrypted: "enc(harbrr-key)",
		KeyID:                 "key-1",
		Enabled:               true,
		SyncLevel:             domain.SyncLevelFull,
		IndexScope:            domain.IndexScopeAll,
		Priority:              25,
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

func TestAppConnectionInsertGetList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AppConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	keyID := mintKey(t, db, "sonarr")
	id, err := repo.InsertConnection(ctx, db, sampleConnection(keyID, now))
	if err != nil {
		t.Fatalf("InsertConnection: %v", err)
	}

	got, err := repo.GetConnection(ctx, db, id)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	switch {
	case got.Name != "Sonarr", got.Kind != domain.AppKindSonarr, got.BaseURL != "http://sonarr:8989":
		t.Errorf("identity round-trip mismatch: %+v", got)
	case got.APIKeyEncrypted != "enc(app-key)" || got.HarbrrAPIKeyEncrypted != "enc(harbrr-key)":
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

func TestAppConnectionUniqueKindBaseURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AppConnections{}
	now := time.Now().UTC()

	conn := sampleConnection(mintKey(t, db, "a"), now)
	if _, err := repo.InsertConnection(ctx, db, conn); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	dup := sampleConnection(mintKey(t, db, "b"), now) // same kind+base_url
	_, err := repo.InsertConnection(ctx, db, dup)
	if !database.IsUniqueViolation(err) {
		t.Fatalf("duplicate (kind, base_url) error = %v, want unique violation", err)
	}
}

func TestAppConnectionUpdateAndEnable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AppConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	id, err := repo.InsertConnection(ctx, db, sampleConnection(mintKey(t, db, "k"), now))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	updated := sampleConnection(0, now)
	updated.ID = id
	updated.Name = "Sonarr 4K"
	updated.SyncLevel = domain.SyncLevelAddUpdate
	updated.IndexScope = domain.IndexScopeSelected
	updated.Priority = 10
	updated.UpdatedAt = now.Add(time.Minute)
	if err := repo.UpdateConnection(ctx, db, updated); err != nil {
		t.Fatalf("UpdateConnection: %v", err)
	}

	got, _ := repo.GetConnection(ctx, db, id)
	if got.Name != "Sonarr 4K" || got.SyncLevel != domain.SyncLevelAddUpdate ||
		got.IndexScope != domain.IndexScopeSelected || got.Priority != 10 {
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

	id, _ := repo.InsertConnection(ctx, db, sampleConnection(mintKey(t, db, "k"), now))
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

	connID, _ := repo.InsertConnection(ctx, db, sampleConnection(mintKey(t, db, "k"), now))
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

	connID, _ := repo.InsertConnection(ctx, db, sampleConnection(mintKey(t, db, "k"), now))
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
	connID, _ := repo.InsertConnection(ctx, db, sampleConnection(keyID, now))

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
