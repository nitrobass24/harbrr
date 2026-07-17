package secrets

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

const backupAAD = "harbrr-backup/v1"

// mustDerive derives a key with the default KDF, failing the test on error.
func mustDerive(t *testing.T, passphrase string, salt []byte) []byte {
	t.Helper()
	key, err := DeriveKeyFromPassphrase(passphrase, salt, DefaultPassphraseKDF())
	if err != nil {
		t.Fatalf("DeriveKeyFromPassphrase: %v", err)
	}
	return key
}

func TestPassphraseRoundTrip(t *testing.T) {
	t.Parallel()
	salt, err := NewPassphraseSalt()
	if err != nil {
		t.Fatalf("NewPassphraseSalt: %v", err)
	}
	key := mustDerive(t, "correct horse battery staple", salt)
	if len(key) != keyLen {
		t.Fatalf("derived key len = %d, want %d", len(key), keyLen)
	}

	plaintext := []byte(`{"tables":{"proxies":[{"url":"http://user:pw@proxy:8080"}]}}`)
	blob, err := EncryptWithKey(key, []byte(backupAAD), plaintext)
	if err != nil {
		t.Fatalf("EncryptWithKey: %v", err)
	}
	if strings.Contains(blob, "proxy:8080") || strings.Contains(blob, "user:pw") {
		t.Fatalf("ciphertext leaked plaintext: %q", blob)
	}

	got, err := DecryptWithKey(key, []byte(backupAAD), blob)
	if err != nil {
		t.Fatalf("DecryptWithKey: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round-trip mismatch: got %q, want %q", got, plaintext)
	}
}

func TestPassphraseWrongPassphraseFailsCleanly(t *testing.T) {
	t.Parallel()
	salt, _ := NewPassphraseSalt()
	blob, err := EncryptWithKey(mustDerive(t, "right", salt), []byte(backupAAD), []byte("secret data"))
	if err != nil {
		t.Fatalf("EncryptWithKey: %v", err)
	}

	// A wrong passphrase derives a different key, so the GCM tag check fails — a clean
	// error, never garbage plaintext.
	got, err := DecryptWithKey(mustDerive(t, "wrong", salt), []byte(backupAAD), blob)
	if err == nil {
		t.Fatalf("DecryptWithKey with wrong passphrase = %q, want error", got)
	}
	if got != nil {
		t.Errorf("failed decrypt returned non-nil plaintext: %q", got)
	}
	if strings.Contains(err.Error(), "secret data") {
		t.Errorf("error leaked plaintext: %v", err)
	}
}

func TestDeriveKeyDeterministicAndSaltUnique(t *testing.T) {
	t.Parallel()
	salt, _ := NewPassphraseSalt()
	// Deterministic in (passphrase, salt): an importer must re-derive the same key.
	if !bytes.Equal(mustDerive(t, "pw", salt), mustDerive(t, "pw", salt)) {
		t.Error("DeriveKeyFromPassphrase not deterministic for the same (passphrase, salt)")
	}
	// A different salt yields a different key (so a reused passphrase is not a reused key).
	other, _ := NewPassphraseSalt()
	if bytes.Equal(salt, other) {
		t.Fatal("NewPassphraseSalt returned identical salts")
	}
	if bytes.Equal(mustDerive(t, "pw", salt), mustDerive(t, "pw", other)) {
		t.Error("same passphrase with different salts derived the same key")
	}
}

func TestDecryptWithKeyRejectsTamperAndAADMismatch(t *testing.T) {
	t.Parallel()
	salt, _ := NewPassphraseSalt()
	key := mustDerive(t, "pw", salt)
	blob, err := EncryptWithKey(key, []byte(backupAAD), []byte("payload"))
	if err != nil {
		t.Fatalf("EncryptWithKey: %v", err)
	}

	// A payload sealed under one AAD cannot be opened under another (splice protection).
	if _, err := DecryptWithKey(key, []byte("harbrr-backup/v2"), blob); err == nil {
		t.Error("DecryptWithKey accepted a mismatched AAD")
	}

	// A flipped GCM-tag byte fails authentication. Flip a decoded byte (not a base64
	// char, whose low bits can be discarded on decode) so a real ciphertext byte changes.
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		t.Fatalf("decode blob: %v", err)
	}
	raw[len(raw)-1] ^= 0x01
	if _, err := DecryptWithKey(key, []byte(backupAAD), base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Error("DecryptWithKey accepted a tampered blob")
	}
}

func TestDeriveKeyHonorsRecordedKDFParams(t *testing.T) {
	t.Parallel()
	salt, _ := NewPassphraseSalt()
	custom := PassphraseKDF{Algorithm: "argon2id", Memory: 32 * 1024, Time: 2, Threads: 1}

	defKey := mustDerive(t, "pw", salt)
	customKey, err := DeriveKeyFromPassphrase("pw", salt, custom)
	if err != nil {
		t.Fatalf("custom derive: %v", err)
	}
	// Different params must derive a different key (proof the params are actually used).
	if bytes.Equal(defKey, customKey) {
		t.Fatal("different KDF params derived the same key — params ignored")
	}
	// A payload sealed under the recorded params re-derives + opens with those same params
	// — the self-describing guarantee that survives a future default-cost bump.
	blob, err := EncryptWithKey(customKey, []byte(backupAAD), []byte("payload"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	reKey, _ := DeriveKeyFromPassphrase("pw", salt, custom)
	got, err := DecryptWithKey(reKey, []byte(backupAAD), blob)
	if err != nil || string(got) != "payload" {
		t.Errorf("re-derive+open with recorded params = %q, err %v; want payload", got, err)
	}
}

func TestDeriveKeyRejectsBadKDF(t *testing.T) {
	t.Parallel()
	salt, _ := NewPassphraseSalt()
	cases := map[string]PassphraseKDF{
		"unknown algorithm": {Algorithm: "scrypt", Memory: 65536, Time: 3, Threads: 2},
		"memory too low":    {Algorithm: "argon2id", Memory: 1, Time: 3, Threads: 2},
		"memory too high":   {Algorithm: "argon2id", Memory: 4 * 1024 * 1024, Time: 3, Threads: 2},
		"time zero":         {Algorithm: "argon2id", Memory: 65536, Time: 0, Threads: 2},
		"threads too high":  {Algorithm: "argon2id", Memory: 65536, Time: 3, Threads: 200},
	}
	for name, kdf := range cases {
		if _, err := DeriveKeyFromPassphrase("pw", salt, kdf); err == nil {
			t.Errorf("DeriveKeyFromPassphrase(%s) = nil error, want rejection", name)
		}
	}
	if !DefaultPassphraseKDF().Valid() {
		t.Error("DefaultPassphraseKDF() is not Valid() — bounds exclude the default")
	}
}

func TestDefaultPassphraseKDF(t *testing.T) {
	t.Parallel()
	kdf := DefaultPassphraseKDF()
	if kdf.Algorithm != "argon2id" {
		t.Errorf("Algorithm = %q, want argon2id", kdf.Algorithm)
	}
	// Must match the shared cost set (defaultArgon2) so the envelope self-describes it.
	if kdf.Memory != defaultArgon2.memory || kdf.Time != defaultArgon2.time || kdf.Threads != defaultArgon2.threads {
		t.Errorf("KDF params = %+v, want memory=%d time=%d threads=%d",
			kdf, defaultArgon2.memory, defaultArgon2.time, defaultArgon2.threads)
	}
}
