package database_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

func TestRotationAllSecretsAndUpdate(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, filepath.Join(t.TempDir(), "rot.db"))
	ctx := context.Background()
	id := seedInstance(t, db, "tt")
	ins := database.Instances{}

	mustInsert := func(s domain.IndexerSetting) {
		if err := ins.InsertSetting(ctx, db, id, s); err != nil {
			t.Fatalf("insert %q: %v", s.Name, err)
		}
	}
	mustInsert(domain.IndexerSetting{Name: "apikey", ValueEncrypted: "blobA", KeyID: "k1", IsSecret: true})
	mustInsert(domain.IndexerSetting{Name: "cookie", ValueEncrypted: "blobB", KeyID: "k1", IsSecret: true})
	mustInsert(domain.IndexerSetting{Name: "sort", Value: "seeders"}) // plaintext, excluded

	rot := database.Rotation{}
	secretRows, err := rot.AllSecrets(ctx, db)
	if err != nil {
		t.Fatalf("AllSecrets: %v", err)
	}
	if len(secretRows) != 2 {
		t.Fatalf("got %d secret rows, want 2 (plaintext excluded)", len(secretRows))
	}

	if err := rot.UpdateSecret(ctx, db, secretRows[0].ID, "blobA2", "k2"); err != nil {
		t.Fatalf("UpdateSecret: %v", err)
	}
	all, err := ins.Settings(ctx, db, id)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	found := false
	for _, s := range all {
		if s.Name == secretRows[0].Name {
			found = true
			if s.ValueEncrypted != "blobA2" || s.KeyID != "k2" {
				t.Errorf("after update: value_encrypted=%q key_id=%q, want blobA2/k2", s.ValueEncrypted, s.KeyID)
			}
		}
	}
	if !found {
		t.Fatalf("updated setting %q not found", secretRows[0].Name)
	}
}
