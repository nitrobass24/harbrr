package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

func sampleAnnounceConnection(harbrrKeyID int64, kind string, now time.Time) domain.AnnounceConnection {
	return domain.AnnounceConnection{
		Name: kind, Kind: kind, BaseURL: "http://" + kind + ":2468",
		APIKeyEncrypted: "enc(tool-key)", HarbrrAPIKeyID: harbrrKeyID,
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
			conn := sampleAnnounceConnection(mintKey(t, db, "k-"+kind), kind, now)
			id, err := repo.InsertAnnounceConnection(ctx, db, conn)
			if err != nil {
				t.Fatalf("InsertAnnounceConnection(%s): %v", kind, err)
			}
			got, err := repo.GetAnnounceConnection(ctx, db, id)
			if err != nil {
				t.Fatalf("GetAnnounceConnection(%s): %v", kind, err)
			}
			if got.Kind != kind || got.BaseURL != conn.BaseURL || !got.Enabled {
				t.Errorf("round-trip = %+v, want kind=%s baseURL=%s enabled", got, kind, conn.BaseURL)
			}
			if got.APIKeyEncrypted != "enc(tool-key)" || got.HarbrrAPIKeyEncrypted != "enc(harbrr-key)" {
				t.Error("encrypted secrets not round-tripped")
			}
		})
	}
}

func TestAnnounceConnectionEnableDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.AnnounceConnections{}
	now := time.Now().UTC().Truncate(time.Second)

	id, err := repo.InsertAnnounceConnection(ctx, db, sampleAnnounceConnection(mintKey(t, db, "k"), domain.AnnounceKindQui, now))
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
	id, err := repo.InsertAnnounceConnection(ctx, db, sampleAnnounceConnection(keyID, domain.AnnounceKindQui, now))
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
