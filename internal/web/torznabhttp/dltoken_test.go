package torznabhttp

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/secrets"
)

const dlTestKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// dlTestLink carries a synthetic passkey-shaped value built by concatenation so
// secret scanners do not flag the fixture.
var dlTestLink = "https://tracker.test/download/123?passkey=" + strings.Repeat("a1b2", 8)

func encryptedKeyring(t *testing.T) *secrets.Keyring {
	t.Helper()
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: dlTestKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("OpenKeyring: %v", err)
	}
	return kr
}

func plaintextKeyringForTest(t *testing.T) *secrets.Keyring {
	t.Helper()
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{AllowPlaintext: true}, zerolog.Nop())
	if err != nil {
		t.Fatalf("OpenKeyring(plaintext): %v", err)
	}
	if !kr.Plaintext() {
		t.Fatal("expected a plaintext keyring")
	}
	return kr
}

func TestDLToken_RoundTrip(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	token, err := encodeDLToken(kr, "mytracker", dlTestLink)
	if err != nil {
		t.Fatalf("encodeDLToken: %v", err)
	}
	got, err := decodeDLToken(kr, "mytracker", token)
	if err != nil {
		t.Fatalf("decodeDLToken: %v", err)
	}
	if got != dlTestLink {
		t.Errorf("round trip = %q, want %q", got, dlTestLink)
	}
}

// TestDLToken_URLSafeAndOpaque confirms the token is URL-safe (no +, /, =) and that
// the passkey never appears in the clear inside it.
func TestDLToken_URLSafeAndOpaque(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	token, err := encodeDLToken(kr, "mytracker", dlTestLink)
	if err != nil {
		t.Fatalf("encodeDLToken: %v", err)
	}
	if strings.ContainsAny(token, "+/=") {
		t.Errorf("token %q is not URL-safe", token)
	}
	if strings.Contains(token, "passkey") || strings.Contains(token, strings.Repeat("a1b2", 8)) {
		t.Errorf("token leaks the link in the clear: %q", token)
	}
}

// TestDLToken_CrossIndexerRejected confirms a token minted for one indexer cannot be
// decoded under another (the AAD binding prevents replay across indexers).
func TestDLToken_CrossIndexerRejected(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	token, err := encodeDLToken(kr, "indexerA", dlTestLink)
	if err != nil {
		t.Fatalf("encodeDLToken: %v", err)
	}
	if _, err := decodeDLToken(kr, "indexerB", token); err == nil {
		t.Error("expected decode under a different indexer to fail")
	}
}

// TestDLToken_TamperRejected confirms a flipped ciphertext byte fails GCM
// authentication. It flips a byte of the DECODED blob (not the last base64url
// character, which can carry only discarded padding bits and would be a no-op) so
// the mutation is always a real change the AEAD must reject.
func TestDLToken_TamperRejected(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	token, err := encodeDLToken(kr, "mytracker", dlTestLink)
	if err != nil {
		t.Fatalf("encodeDLToken: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	raw[len(raw)/2] ^= 0x01 // flip a bit in a ciphertext byte (past the GCM nonce)
	tampered := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := decodeDLToken(kr, "mytracker", tampered); err == nil {
		t.Error("expected decode of a tampered token to fail")
	}
}

// TestDLToken_MalformedRejected confirms a non-base64url token fails gracefully.
func TestDLToken_MalformedRejected(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	if _, err := decodeDLToken(kr, "mytracker", "not a token!!!"); err == nil {
		t.Error("expected decode of a malformed token to fail")
	}
}

// TestDLToken_PlaintextModeRoundTrips confirms the codec still works without an
// encryption key, and that even then the passkey is not literally present in the
// token (it is base64url-obscured).
func TestDLToken_PlaintextModeRoundTrips(t *testing.T) {
	t.Parallel()
	kr := plaintextKeyringForTest(t)
	token, err := encodeDLToken(kr, "mytracker", dlTestLink)
	if err != nil {
		t.Fatalf("encodeDLToken: %v", err)
	}
	if strings.Contains(token, "passkey") {
		t.Errorf("plaintext-mode token shows the passkey literally: %q", token)
	}
	got, err := decodeDLToken(kr, "mytracker", token)
	if err != nil {
		t.Fatalf("decodeDLToken: %v", err)
	}
	if got != dlTestLink {
		t.Errorf("round trip = %q, want %q", got, dlTestLink)
	}
}
