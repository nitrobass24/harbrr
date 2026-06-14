package secrets_test

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/secrets"
)

// hexKey is a synthetic 32-byte key in hex (test-only).
const hexKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func openKeyring(t *testing.T, opts secrets.KeyringOptions) *secrets.Keyring {
	t.Helper()
	k, err := secrets.OpenKeyring(opts, zerolog.Nop())
	if err != nil {
		t.Fatalf("OpenKeyring: %v", err)
	}
	return k
}

func TestKeyringInlineKeyRoundTrip(t *testing.T) {
	t.Parallel()

	k := openKeyring(t, secrets.KeyringOptions{EncryptionKey: hexKey})
	if k.Plaintext() {
		t.Fatal("inline key should not be plaintext mode")
	}

	blob, err := k.Encrypt(7, "passkey", "p4ssk3y")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if blob == "p4ssk3y" {
		t.Error("encrypted blob equals plaintext")
	}
	got, err := k.Decrypt(7, "passkey", blob)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != "p4ssk3y" {
		t.Errorf("round-trip = %q, want p4ssk3y", got)
	}
}

func TestKeyringAADBoundToRow(t *testing.T) {
	t.Parallel()

	k := openKeyring(t, secrets.KeyringOptions{EncryptionKey: hexKey})
	blob, err := k.Encrypt(7, "passkey", "value")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Decrypting with a different instance id or setting must fail (AAD binding).
	if _, err := k.Decrypt(8, "passkey", blob); err == nil {
		t.Error("decrypt with wrong instance id succeeded")
	}
	if _, err := k.Decrypt(7, "cookie", blob); err == nil {
		t.Error("decrypt with wrong setting succeeded")
	}
}

func TestKeyringWrongKeyFails(t *testing.T) {
	t.Parallel()

	k1 := openKeyring(t, secrets.KeyringOptions{EncryptionKey: hexKey})
	blob, err := k1.Encrypt(1, "x", "v")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	otherKey := strings.Repeat("ab", 32) // different 32-byte hex key
	k2 := openKeyring(t, secrets.KeyringOptions{EncryptionKey: otherKey})
	if k1.KeyID() == k2.KeyID() {
		t.Fatal("different keys produced the same key_id")
	}
	if _, err := k2.Decrypt(1, "x", blob); err == nil {
		t.Error("decrypt with a different key succeeded")
	}
}

func TestKeyIDStableForSameKey(t *testing.T) {
	t.Parallel()

	a := openKeyring(t, secrets.KeyringOptions{EncryptionKey: hexKey})
	b := openKeyring(t, secrets.KeyringOptions{EncryptionKey: hexKey})
	if a.KeyID() != b.KeyID() {
		t.Errorf("key_id not stable: %q vs %q", a.KeyID(), b.KeyID())
	}
	if len(a.KeyID()) != 16 { // hex of 8 bytes
		t.Errorf("key_id len = %d, want 16", len(a.KeyID()))
	}
}

func TestKeyringAutoGeneratesKeyfile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	k := openKeyring(t, secrets.KeyringOptions{DataDir: dir})
	if k.Plaintext() {
		t.Fatal("auto-gen should enable encryption, not plaintext")
	}

	keyPath := filepath.Join(dir, ".keys", "harbrr.key")
	fi, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("keyfile not created: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("keyfile mode = %o, want 600", fi.Mode().Perm())
	}

	// A second open reuses the same keyfile (stable key_id across restarts).
	k2 := openKeyring(t, secrets.KeyringOptions{DataDir: dir})
	if k.KeyID() != k2.KeyID() {
		t.Error("auto keyfile not reused on second open (key_id changed)")
	}
}

func TestKeyringAllowPlaintext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	k := openKeyring(t, secrets.KeyringOptions{DataDir: dir, AllowPlaintext: true})
	if !k.Plaintext() {
		t.Fatal("allow_plaintext did not enable plaintext mode")
	}
	// In plaintext mode Encrypt is a passthrough (the opt-in the user accepted).
	blob, err := k.Encrypt(1, "x", "raw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if blob != "raw" {
		t.Errorf("plaintext Encrypt = %q, want raw", blob)
	}
	// No keyfile is generated in plaintext mode.
	if _, err := os.Stat(filepath.Join(dir, ".keys", "harbrr.key")); !os.IsNotExist(err) {
		t.Error("plaintext mode should not generate a keyfile")
	}
}

func TestKeyringMissingKeyFileIsFatal(t *testing.T) {
	t.Parallel()

	_, err := secrets.OpenKeyring(
		secrets.KeyringOptions{KeyFile: filepath.Join(t.TempDir(), "nope.key")},
		zerolog.Nop(),
	)
	if err == nil {
		t.Fatal("a configured but missing key_file must fail, not fall back to plaintext")
	}
}

func TestKeyringReadsRawKeyfile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "k.key")
	raw, err := hex.DecodeString(hexKey)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := os.WriteFile(keyPath, raw, 0o600); err != nil {
		t.Fatalf("write keyfile: %v", err)
	}

	k := openKeyring(t, secrets.KeyringOptions{KeyFile: keyPath})
	inline := openKeyring(t, secrets.KeyringOptions{EncryptionKey: hexKey})
	if k.KeyID() != inline.KeyID() {
		t.Error("raw keyfile and inline hex of the same key produced different key_ids")
	}
}

func TestKeyringRejectsBadInlineKey(t *testing.T) {
	t.Parallel()

	if _, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: "too-short"}, zerolog.Nop()); err == nil {
		t.Error("a malformed encryption_key must error")
	}
}

func TestKeyringEmptyDataDirNoKeyIsFatal(t *testing.T) {
	t.Parallel()

	// No key, no data dir, no plaintext opt-in: must refuse rather than anchor a
	// relative-path keyfile whose key_id would drift with the working directory.
	if _, err := secrets.OpenKeyring(secrets.KeyringOptions{}, zerolog.Nop()); err == nil {
		t.Error("empty data_dir without a key or allow_plaintext must error")
	}
}

func TestKeyringEmptyDataDirPlaintextOK(t *testing.T) {
	t.Parallel()

	k := openKeyring(t, secrets.KeyringOptions{AllowPlaintext: true})
	if !k.Plaintext() {
		t.Error("allow_plaintext with empty data_dir should yield plaintext mode")
	}
}
