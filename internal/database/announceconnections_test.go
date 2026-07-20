package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

// sampleAnnounceConnection builds a fully-populated connection referencing appID (nil is
// valid — the partial unique index on app_id only applies to non-NULL values). Identity
// (base_url, the tool's own api key) is no longer stored on this row (#269) — it lives on
// the referenced App.
func sampleAnnounceConnection(appID *int64, harbrrKeyID int64, kind string, now time.Time) domain.AnnounceConnection {
	return domain.AnnounceConnection{
		Name: kind, Kind: kind, AppID: appID, HarbrrAPIKeyID: harbrrKeyID,
		HarbrrAPIKeyEncrypted: "enc(harbrr-key)", KeyID: "key-1", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
}

func TestAnnounceConnectionRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AnnounceConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	for _, kind := range []string{domain.AnnounceKindQui, domain.AnnounceKindCrossSeedV6} {
		t.Run(kind, func(t *testing.T) {
			appID := mintApp(t, db, kind, "http://"+kind+":2468")
			conn := sampleAnnounceConnection(&appID, mintKey(t, db, "k-"+kind), kind, now)
			id, err := repo.InsertAnnounceConnection(ctx, db, conn)
			if err != nil {
				t.Fatalf("InsertAnnounceConnection(%s): %v", kind, err)
			}
			got, err := repo.GetAnnounceConnection(ctx, db, id)
			if err != nil {
				t.Fatalf("GetAnnounceConnection(%s): %v", kind, err)
			}
			if got.Kind != kind || got.AppID == nil || *got.AppID != appID || !got.Enabled {
				t.Errorf("round-trip = %+v, want kind=%s appID=%d enabled", got, kind, appID)
			}
			if got.HarbrrAPIKeyEncrypted != "enc(harbrr-key)" {
				t.Error("encrypted harbrr key not round-tripped")
			}
		})
	}
}

// TestAnnounceConnectionUniqueAppID proves the partial UNIQUE(app_id) index (#269 —
// replacing the old UNIQUE(kind, base_url)) rejects a second row at the same non-NULL
// app_id, but exempts NULL.
func TestAnnounceConnectionUniqueAppID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AnnounceConnections{}
	now := time.Now().UTC()

	appID := mintApp(t, db, domain.AnnounceKindQui, "http://qui:7476")
	conn := sampleAnnounceConnection(&appID, mintKey(t, db, "a"), domain.AnnounceKindQui, now)
	if _, err := repo.InsertAnnounceConnection(ctx, db, conn); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	dup := sampleAnnounceConnection(&appID, mintKey(t, db, "b"), domain.AnnounceKindQui, now)
	if _, err := repo.InsertAnnounceConnection(ctx, db, dup); !database.IsUniqueViolation(err) {
		t.Fatalf("duplicate app_id error = %v, want unique violation", err)
	}
}

func TestAnnounceConnectionEnableDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AnnounceConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	id, err := repo.InsertAnnounceConnection(ctx, db, sampleAnnounceConnection(nil, mintKey(t, db, "k"), domain.AnnounceKindQui, now))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := repo.SetAnnounceConnectionEnabled(ctx, db, id, false, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	if got, _ := repo.GetAnnounceConnection(ctx, db, id); got.Enabled {
		t.Error("connection still enabled after disable")
	}

	if err := repo.DeleteAnnounceConnection(ctx, db, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetAnnounceConnection(ctx, db, id); err == nil {
		t.Error("connection still present after delete")
	}
}

// TestAnnounceConnectionKeyRevocationSetsNull proves the harbrr_api_key_id FK is
// ON DELETE SET NULL (a revoked key leaves the connection row, with id zeroed).
func TestAnnounceConnectionKeyRevocationSetsNull(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AnnounceConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	keyID := mintKey(t, db, "k")
	id, err := repo.InsertAnnounceConnection(ctx, db, sampleAnnounceConnection(nil, keyID, domain.AnnounceKindQui, now))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := (database.APIKeys{}).Delete(ctx, db, keyID); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	got, err := repo.GetAnnounceConnection(ctx, db, id)
	if err != nil {
		t.Fatalf("get after revoke: %v", err)
	}
	if got.HarbrrAPIKeyID != 0 {
		t.Errorf("harbrr_api_key_id = %d, want 0 (SET NULL)", got.HarbrrAPIKeyID)
	}
}
