package secrets_test

import (
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/secrets"
)

func TestCanarySameKeyVerifies(t *testing.T) {
	t.Parallel()

	k := openKeyring(t, secrets.KeyringOptions{EncryptionKey: hexKey})
	blob, err := k.EncryptCanary()
	if err != nil {
		t.Fatalf("EncryptCanary: %v", err)
	}
	if err := k.VerifyCanary(k.KeyID(), blob); err != nil {
		t.Errorf("VerifyCanary with the same key failed: %v", err)
	}
}

func TestCanaryChangedKeyFailsLoud(t *testing.T) {
	t.Parallel()

	k1 := openKeyring(t, secrets.KeyringOptions{EncryptionKey: hexKey})
	blob, err := k1.EncryptCanary()
	if err != nil {
		t.Fatalf("EncryptCanary: %v", err)
	}

	// A different key must fail — both by the key_id guard...
	k2, err := secrets.OpenKeyring(secrets.KeyringOptions{
		EncryptionKey: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	}, zerolog.Nop())
	if err != nil {
		t.Fatalf("OpenKeyring k2: %v", err)
	}
	if err := k2.VerifyCanary(k1.KeyID(), blob); err == nil {
		t.Error("VerifyCanary accepted a stored key_id from a different key")
	}
	// ...and even if an attacker forged the key_id, the AEAD open fails.
	if err := k2.VerifyCanary(k2.KeyID(), blob); err == nil {
		t.Error("VerifyCanary decrypted a canary sealed under a different key")
	}
}

func TestCanaryPlaintextEncryptedFlipFails(t *testing.T) {
	t.Parallel()

	enc := openKeyring(t, secrets.KeyringOptions{EncryptionKey: hexKey})
	blob, err := enc.EncryptCanary()
	if err != nil {
		t.Fatalf("EncryptCanary: %v", err)
	}

	// Switching to plaintext mode changes the key_id → mismatch → fail loud.
	plain := openKeyring(t, secrets.KeyringOptions{DataDir: t.TempDir(), AllowPlaintext: true})
	if err := plain.VerifyCanary(enc.KeyID(), blob); err == nil {
		t.Error("VerifyCanary accepted an encrypted canary in plaintext mode")
	}
}
