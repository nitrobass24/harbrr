package connresource

import (
	"crypto/rand"
	"errors"
	"testing"
)

// TestSeal covers the properties Lifecycle.Create and backup/restore both rely on:
// the secret is encrypted under the caller's id and its own discriminator (and
// decrypts under neither a different id nor a different discriminator), and the
// keyring's key id is passed through unchanged.
func TestSeal(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t)

	tests := []struct {
		name string
		id   int64
		sec  Secret
	}{
		{name: "url secret", id: 1, sec: Secret{Discriminator: "url", Plaintext: "https://example.test/secret"}},
		{name: "app secret", id: 2, sec: Secret{Discriminator: "app", Plaintext: "app-plaintext"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			encrypted, keyID, err := Seal(kr, tt.id, tt.sec)
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			if keyID != kr.KeyID() {
				t.Errorf("keyID = %q, want %q (passthrough)", keyID, kr.KeyID())
			}
			dec, err := kr.Decrypt(tt.id, tt.sec.Discriminator, encrypted)
			if err != nil {
				t.Fatalf("decrypt under (%d, %s): %v", tt.id, tt.sec.Discriminator, err)
			}
			if dec != tt.sec.Plaintext {
				t.Errorf("decrypted = %q, want %q", dec, tt.sec.Plaintext)
			}
			if _, err := kr.Decrypt(tt.id+1, tt.sec.Discriminator, encrypted); err == nil {
				t.Error("decrypt under a different id should fail (AAD not bound to that id)")
			}
			if _, err := kr.Decrypt(tt.id, tt.sec.Discriminator+"-other", encrypted); err == nil {
				t.Error("decrypt under a different discriminator should fail")
			}
		})
	}
}

// errReader is an io.Reader that always fails, used below to force a genuine
// Keyring.Encrypt failure (crypto/rand.Reader is a package var precisely so it can
// be substituted like this) rather than inventing a keyring fake.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, errors.New("errReader: forced failure") }

// TestSealEncryptFailurePropagates forces the keyring's nonce read to fail so a
// real Encrypt error surfaces, and checks Seal wraps it (never swallows it) and
// returns zero values rather than a partial ciphertext. It swaps the
// package-level crypto/rand.Reader for its duration, so — unlike every other test
// in this file — it must not run in parallel with them.
func TestSealEncryptFailurePropagates(t *testing.T) {
	kr := newTestKeyring(t)

	orig := rand.Reader
	rand.Reader = errReader{}
	t.Cleanup(func() { rand.Reader = orig })

	encrypted, keyID, err := Seal(kr, 1, Secret{Discriminator: "url", Plaintext: "s3cret"})
	if err == nil {
		t.Fatal("Seal did not propagate the Encrypt failure")
	}
	if encrypted != "" {
		t.Errorf("encrypted = %q, want empty on failure", encrypted)
	}
	if keyID != "" {
		t.Errorf("keyID = %q, want empty on failure", keyID)
	}
}
