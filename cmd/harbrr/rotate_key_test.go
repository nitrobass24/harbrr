package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// keyring builds an encrypting keyring from a synthetic 32-byte hex key (allowed
// in *_test.go per AGENTS.md). b is a 2-hex-char byte repeated to 32 bytes.
func keyring(t *testing.T, b string) *secrets.Keyring {
	t.Helper()
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: strings.Repeat(b, 32)}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open keyring: %v", err)
	}
	return kr
}

// seedEncryptedStore builds a migrated DB with one instance, a real + an empty
// secret encrypted under kr, a plaintext setting, and the canary/key_id under kr.
func seedEncryptedStore(t *testing.T, kr *secrets.Keyring) *database.DB {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(filepath.Join(t.TempDir(), "rot.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ins := database.Instances{}
	id, err := ins.Insert(ctx, db, domain.IndexerInstance{
		Slug: "tt", DefinitionID: "def", Name: "tt", Enabled: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	blob, err := kr.Encrypt(id, "apikey", "SECRET-VALUE")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	for _, s := range []domain.IndexerSetting{
		{Name: "apikey", ValueEncrypted: blob, KeyID: kr.KeyID(), IsSecret: true},
		{Name: "cookie", ValueEncrypted: "", KeyID: kr.KeyID(), IsSecret: true}, // empty secret
		{Name: "sort", Value: "seeders"},
	} {
		if err := ins.InsertSetting(ctx, db, id, s); err != nil {
			t.Fatalf("insert %q: %v", s.Name, err)
		}
	}
	meta := database.AppMeta{}
	canary, err := kr.EncryptCanary()
	if err != nil {
		t.Fatalf("canary: %v", err)
	}
	if err := meta.Set(ctx, db, canaryBlobKey, canary); err != nil {
		t.Fatalf("set canary: %v", err)
	}
	if err := meta.Set(ctx, db, canaryIDKey, kr.KeyID()); err != nil {
		t.Fatalf("set key id: %v", err)
	}
	return db
}

func TestRotateKeys_Success(t *testing.T) {
	t.Parallel()
	krA, krB := keyring(t, "a1"), keyring(t, "b2")
	db := seedEncryptedStore(t, krA)
	ctx := context.Background()

	rep, err := rotateKeys(ctx, db, krA, krB, false)
	if err != nil {
		t.Fatalf("rotateKeys: %v", err)
	}
	if rep.rows != 2 {
		t.Errorf("rotated rows = %d, want 2 (apikey + empty cookie)", rep.rows)
	}

	rows, err := (database.Rotation{}).AllSecrets(ctx, db)
	if err != nil {
		t.Fatalf("AllSecrets: %v", err)
	}
	for _, r := range rows {
		if r.ValueEncrypted == "" {
			continue // empty secret stays empty
		}
		pt, derr := krB.Decrypt(r.InstanceID, r.Name, r.ValueEncrypted)
		if derr != nil {
			t.Fatalf("decrypt %q under new key: %v", r.Name, derr)
		}
		if r.Name == "apikey" && pt != "SECRET-VALUE" {
			t.Errorf("apikey = %q, want SECRET-VALUE", pt)
		}
		// must NOT still decrypt under the old key
		if _, oerr := krA.Decrypt(r.InstanceID, r.Name, r.ValueEncrypted); oerr == nil {
			t.Errorf("%q still decrypts under the OLD key after rotation", r.Name)
		}
	}

	meta := database.AppMeta{}
	storedID, _, _ := meta.Get(ctx, db, canaryIDKey)
	if storedID != krB.KeyID() {
		t.Errorf("stored key id = %q, want new %q", storedID, krB.KeyID())
	}
	blob, _, _ := meta.Get(ctx, db, canaryBlobKey)
	if err := krB.VerifyCanary(storedID, blob); err != nil {
		t.Errorf("canary fails to verify under new key: %v", err)
	}
}

func TestRotateKeys_WrongOldKeyFailsBeforeWrite(t *testing.T) {
	t.Parallel()
	krA, krB, krC := keyring(t, "a1"), keyring(t, "b2"), keyring(t, "c3")
	db := seedEncryptedStore(t, krA)
	ctx := context.Background()

	if _, err := rotateKeys(ctx, db, krC, krB, false); err == nil {
		t.Fatal("want error for a wrong old key")
	}
	storedID, _, _ := (database.AppMeta{}).Get(ctx, db, canaryIDKey)
	if storedID != krA.KeyID() {
		t.Errorf("store mutated on wrong-key rotate: key id = %q, want old", storedID)
	}
}

func TestRotateKeys_DryRunNoWrites(t *testing.T) {
	t.Parallel()
	krA, krB := keyring(t, "a1"), keyring(t, "b2")
	db := seedEncryptedStore(t, krA)
	ctx := context.Background()

	rep, err := rotateKeys(ctx, db, krA, krB, true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if rep.rows != 2 {
		t.Errorf("dry-run rows = %d, want 2", rep.rows)
	}
	storedID, _, _ := (database.AppMeta{}).Get(ctx, db, canaryIDKey)
	if storedID != krA.KeyID() {
		t.Errorf("dry-run mutated the store: key id = %q, want old", storedID)
	}
}

func TestRotateKeys_PlaintextRejected(t *testing.T) {
	t.Parallel()
	krA := keyring(t, "a1")
	db := seedEncryptedStore(t, krA)
	plain, err := secrets.OpenKeyring(secrets.KeyringOptions{AllowPlaintext: true}, zerolog.Nop())
	if err != nil {
		t.Fatalf("plaintext keyring: %v", err)
	}
	if _, err := rotateKeys(context.Background(), db, plain, krA, false); err == nil {
		t.Fatal("want error rotating in plaintext mode")
	}
}
