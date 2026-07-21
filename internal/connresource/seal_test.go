package connresource

import (
	"crypto/rand"
	"errors"
	"testing"
)

// TestSeal covers the three properties Lifecycle.Create and backup/restore both
// rely on: secrets are encrypted in the given order, under the caller's id, and
// the keyring's key id is passed through unchanged.
func TestSeal(t *testing.T) {
	t.Parallel()
	kr := newTestKeyring(t)

	tests := []struct {
		name  string
		id    int64
		plain []Secret
	}{
		{
			name:  "single secret",
			id:    1,
			plain: []Secret{{Discriminator: "url", Plaintext: "https://example.test/secret"}},
		},
		{
			name: "multi secret ordering",
			id:   2,
			plain: []Secret{
				{Discriminator: "app", Plaintext: "app-plaintext"},
				{Discriminator: "harbrr", Plaintext: "harbrr-plaintext"},
			},
		},
		{
			name:  "no secrets",
			id:    3,
			plain: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			encrypted, keyID, err := Seal(kr, tt.id, tt.plain)
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			if keyID != kr.KeyID() {
				t.Errorf("keyID = %q, want %q (passthrough)", keyID, kr.KeyID())
			}
			if len(encrypted) != len(tt.plain) {
				t.Fatalf("encrypted len = %d, want %d", len(encrypted), len(tt.plain))
			}
			for i, sec := range tt.plain {
				// Same order: encrypted[i] must decrypt under plain[i]'s own
				// discriminator, not any other secret's.
				dec, err := kr.Decrypt(tt.id, sec.Discriminator, encrypted[i])
				if err != nil {
					t.Fatalf("decrypt index %d (%s): %v", i, sec.Discriminator, err)
				}
				if dec != sec.Plaintext {
					t.Errorf("index %d (%s): decrypted = %q, want %q", i, sec.Discriminator, dec, sec.Plaintext)
				}
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
// returns zero values rather than a partial ciphertext list. It swaps the
// package-level crypto/rand.Reader for its duration, so — unlike every other test
// in this file — it must not run in parallel with them.
func TestSealEncryptFailurePropagates(t *testing.T) {
	kr := newTestKeyring(t)

	orig := rand.Reader
	rand.Reader = errReader{}
	t.Cleanup(func() { rand.Reader = orig })

	encrypted, keyID, err := Seal(kr, 1, []Secret{{Discriminator: "url", Plaintext: "s3cret"}})
	if err == nil {
		t.Fatal("Seal did not propagate the Encrypt failure")
	}
	if encrypted != nil {
		t.Errorf("encrypted = %v, want nil on failure", encrypted)
	}
	if keyID != "" {
		t.Errorf("keyID = %q, want empty on failure", keyID)
	}
}
