package database_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// hexKeyA / hexKeyB are two distinct synthetic 32-byte keys (hex) used to drive a
// real key rotation. Synthetic test secrets only.
const (
	hexKeyA = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	hexKeyB = "2122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f40"
)

// TestUpdateSecretRotationInvariant drives a full key rotation through UpdateSecret
// and pins the invariant: the plaintext recovered after rotation is identical to
// the original, the row now decrypts only under the NEW key (not the old one), and
// the stored key_id advances. UpdateSecret persists ciphertext+key_id verbatim, so
// this proves the rotation round-trips end to end.
func TestUpdateSecretRotationInvariant(t *testing.T) {
	t.Parallel()
	db := openMigrated(t, filepath.Join(t.TempDir(), "rotinv.db"))
	ctx := context.Background()
	instanceID := seedInstance(t, db, "tt")

	const setting = "passkey"
	const secretPlaintext = "super-secret-passkey-value"

	oldRing, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: hexKeyA}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open old keyring: %v", err)
	}
	newRing, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: hexKeyB}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open new keyring: %v", err)
	}

	// Seed the encrypted secret under the OLD key.
	encOld, err := oldRing.Encrypt(instanceID, setting, secretPlaintext)
	if err != nil {
		t.Fatalf("encrypt under old key: %v", err)
	}
	ins := database.Instances{}
	if err := ins.InsertSetting(ctx, db, instanceID,
		domain.IndexerSetting{Name: setting, ValueEncrypted: encOld, KeyID: oldRing.KeyID(), IsSecret: true}); err != nil {
		t.Fatalf("insert secret: %v", err)
	}

	rot := database.Rotation{}
	rows, err := rot.AllSecrets(ctx, db)
	if err != nil {
		t.Fatalf("AllSecrets: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d secret rows, want 1", len(rows))
	}
	row := rows[0]

	// Rotate: decrypt under old key, re-encrypt under new key, persist.
	pt, err := oldRing.Decrypt(row.InstanceID, row.Name, row.ValueEncrypted)
	if err != nil {
		t.Fatalf("decrypt under old key: %v", err)
	}
	encNew, err := newRing.Encrypt(row.InstanceID, row.Name, pt)
	if err != nil {
		t.Fatalf("re-encrypt under new key: %v", err)
	}
	if err := rot.UpdateSecret(ctx, db, row.ID, encNew, newRing.KeyID()); err != nil {
		t.Fatalf("UpdateSecret: %v", err)
	}

	// Read the rotated row back.
	after, err := rot.AllSecrets(ctx, db)
	if err != nil {
		t.Fatalf("AllSecrets after rotate: %v", err)
	}
	rotated := after[0]
	if rotated.ValueEncrypted == encOld {
		t.Error("ciphertext unchanged after rotation, want re-encrypted blob")
	}

	// Invariant: the new key recovers the ORIGINAL plaintext.
	got, err := newRing.Decrypt(rotated.InstanceID, rotated.Name, rotated.ValueEncrypted)
	if err != nil {
		t.Fatalf("decrypt under new key: %v", err)
	}
	if got != secretPlaintext {
		t.Errorf("post-rotation plaintext = %q, want %q (invariant broken)", got, secretPlaintext)
	}

	// The old key must no longer open the rotated blob.
	if _, err := oldRing.Decrypt(rotated.InstanceID, rotated.Name, rotated.ValueEncrypted); err == nil {
		t.Error("old key still decrypts after rotation, want failure")
	}

	// The stored key_id advanced to the new key.
	settings, err := ins.Settings(ctx, db, instanceID)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if len(settings) != 1 || settings[0].KeyID != newRing.KeyID() {
		t.Errorf("stored key_id = %q, want %q", settings[0].KeyID, newRing.KeyID())
	}
}

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
