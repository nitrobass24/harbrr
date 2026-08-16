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

func sealedDLTestPayload(t *testing.T, kr *secrets.Keyring, indexerID, payload string) string {
	t.Helper()
	blob, err := kr.SealToken(dlTokenPurpose(indexerID), payload)
	if err != nil {
		t.Fatalf("SealToken: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(blob))
}

func TestDLToken_RoundTrip(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	link := dlTestLink + ";part=2"
	token, err := encodeDLToken(kr, "mytracker", 2000, "Release.Name.2026", link)
	if err != nil {
		t.Fatalf("encodeDLToken: %v", err)
	}
	payload, err := decodeDLToken(kr, "mytracker", token)
	if err != nil {
		t.Fatalf("decodeDLToken: %v", err)
	}
	if payload.categoryID != 2000 {
		t.Errorf("round trip category = %d, want 2000", payload.categoryID)
	}
	if payload.name != "Release.Name.2026" {
		t.Errorf("round trip name = %q, want %q", payload.name, "Release.Name.2026")
	}
	if payload.link != link {
		t.Error("round trip link differs from the sealed link (values withheld: link-shaped)")
	}
}

func TestDLToken_LegacyPayloads(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	tests := []struct {
		name       string
		payload    string
		categoryID int
		wantLink   string
	}{
		{name: "category and link", payload: "2000;" + dlTestLink, categoryID: 2000, wantLink: dlTestLink},
		{name: "bare link", payload: dlTestLink, wantLink: dlTestLink},
		{name: "bare link with semicolon", payload: dlTestLink + ";part=2", wantLink: dlTestLink + ";part=2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeDLToken(kr, "mytracker", sealedDLTestPayload(t, kr, "mytracker", tt.payload))
			if err != nil {
				t.Fatalf("decodeDLToken: %v", err)
			}
			if got.categoryID != tt.categoryID || got.name != "" || got.link != tt.wantLink {
				t.Error("legacy payload decoded incorrectly (link-shaped value withheld)")
			}
		})
	}
}

func TestDLToken_MalformedV2PayloadRejected(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	tests := []struct {
		name    string
		payload string
	}{
		{name: "unsupported version", payload: "v3;2000;;https://tracker.test/dl"},
		{name: "invalid category", payload: "v2;invalid;;https://tracker.test/dl"},
		{name: "invalid base64url name", payload: "v2;2000;***;https://tracker.test/dl"},
		{name: "missing link", payload: "v2;2000;UmVsZWFzZQ;"},
		{name: "missing fields", payload: "v2;2000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			token := sealedDLTestPayload(t, kr, "mytracker", tt.payload)
			if _, err := decodeDLToken(kr, "mytracker", token); err == nil {
				t.Error("expected malformed payload to fail")
			}
		})
	}
}

// TestDLToken_URLSafeAndOpaque confirms the token is URL-safe (no +, /, =) and that
// the passkey never appears in the clear inside it.
func TestDLToken_URLSafeAndOpaque(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	token, err := encodeDLToken(kr, "mytracker", 2000, "Release.Name.2026", dlTestLink)
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

	tests := []struct {
		name    string
		keyring func(*testing.T) *secrets.Keyring
	}{
		{name: "encrypted", keyring: encryptedKeyring},
		{name: "plaintext credentials", keyring: plaintextKeyringForTest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			kr := test.keyring(t)
			token, err := encodeDLToken(kr, "indexerA", 2000, "Release.Name.2026", dlTestLink)
			if err != nil {
				t.Fatalf("encodeDLToken: %v", err)
			}
			if _, err := decodeDLToken(kr, "indexerB", token); err == nil {
				t.Error("expected decode under a different indexer to fail")
			}
		})
	}
}

// TestDLToken_TamperRejected confirms a flipped ciphertext byte fails GCM
// authentication. It flips a byte of the DECODED blob (not the last base64url
// character, which can carry only discarded padding bits and would be a no-op) so
// the mutation is always a real change the AEAD must reject.
func TestDLToken_TamperRejected(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	token, err := encodeDLToken(kr, "mytracker", 2000, "Release.Name.2026", dlTestLink)
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

// TestDLToken_PlaintextModeRoundTrips confirms the codec still works when credential
// storage is plaintext while keeping the network token authenticated and opaque.
func TestDLToken_PlaintextModeRoundTrips(t *testing.T) {
	t.Parallel()
	kr := plaintextKeyringForTest(t)
	token, err := encodeDLToken(kr, "mytracker", 2000, "Release.Name.2026", dlTestLink)
	if err != nil {
		t.Fatalf("encodeDLToken: %v", err)
	}
	if strings.Contains(token, "passkey") {
		t.Errorf("plaintext-mode token shows the passkey literally: %q", token)
	}
	payload, err := decodeDLToken(kr, "mytracker", token)
	if err != nil {
		t.Fatalf("decodeDLToken: %v", err)
	}
	if payload.categoryID != 2000 {
		t.Errorf("round trip category = %d, want 2000", payload.categoryID)
	}
	if payload.name != "Release.Name.2026" {
		t.Errorf("round trip name = %q, want %q", payload.name, "Release.Name.2026")
	}
	if payload.link != dlTestLink {
		t.Error("round trip link differs from the sealed link (values withheld: link-shaped)")
	}
}

// TestDLToken_PlaintextModeRejectsForgery proves a feed API-key holder cannot forge
// a token by base64url-encoding an attacker-selected URL when credentials are stored
// in plaintext mode.
func TestDLToken_PlaintextModeRejectsForgery(t *testing.T) {
	t.Parallel()

	kr := plaintextKeyringForTest(t)
	forged := base64.RawURLEncoding.EncodeToString([]byte("http://127.0.0.1/private"))
	if _, err := decodeDLToken(kr, "mytracker", forged); err == nil {
		t.Fatal("plaintext-mode forged token decoded successfully")
	}
}
