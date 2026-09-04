package grab

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/spf13/pathologize"

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
	if payload.CategoryID != 2000 {
		t.Errorf("round trip category = %d, want 2000", payload.CategoryID)
	}
	if payload.Name != "Release.Name.2026" {
		t.Errorf("round trip name = %q, want %q", payload.Name, "Release.Name.2026")
	}
	if payload.Link != link {
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
			if got.CategoryID != tt.categoryID || got.Name != "" || got.Link != tt.wantLink {
				t.Error("legacy payload decoded incorrectly (link-shaped value withheld)")
			}
		})
	}
}

func TestDLToken_MalformedPayloadRejected(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	tests := []struct {
		name    string
		payload string
	}{
		{name: "truncated JSON", payload: `{"c":2000,"n":"Release`},
		{name: "negative category", payload: `{"c":-1,"l":"https://tracker.test/dl"}`},
		{name: "wrong field type", payload: `{"c":"2000","l":"https://tracker.test/dl"}`},
		{name: "missing link", payload: `{"c":2000,"n":"Release"}`},
		{name: "empty link", payload: `{"c":2000,"l":""}`},
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

// TestDLToken_OverlongTitleStillGrabs pins the rule that a title too long for the
// filename budget costs at most the filename, never the download. Cutting the stem to
// fit can expose a trailing dot or space that pathologize.Clean had already trimmed,
// and the decoder drops a stem it considers unclean — so a sloppy cut used to mint a
// token harbrr itself refused to open.
func TestDLToken_OverlongTitleStillGrabs(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	tests := []struct {
		name  string
		title string
	}{
		{name: "cut lands on a space", title: strings.Repeat("A", maxDownloadNameBytes-1) + " Extended.Cut.2160p"},
		{name: "cut lands on a dot", title: strings.Repeat("A", maxDownloadNameBytes-1) + ".2160p.WEB-DL.HEVC"},
		{name: "cut lands mid multi-byte rune", title: strings.Repeat("あ", maxDownloadNameBytes)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			token, err := encodeDLToken(kr, "mytracker", 2000, tt.title, dlTestLink)
			if err != nil {
				t.Fatalf("encodeDLToken: %v", err)
			}
			got, err := decodeDLToken(kr, "mytracker", token)
			if err != nil {
				t.Fatalf("decodeDLToken rejected a token harbrr just minted: %v", err)
			}
			if got.Link != dlTestLink {
				t.Error("round trip link differs from the sealed link (values withheld: link-shaped)")
			}
			if got.Name == "" {
				t.Error("stem dropped entirely; a truncated title should still name the file")
			}
			if len(got.Name) > maxDownloadNameBytes {
				t.Errorf("stem is %d bytes, over the %d budget", len(got.Name), maxDownloadNameBytes)
			}
			if !pathologize.IsClean(got.Name) {
				t.Errorf("truncated stem is not a portable filename: %q", got.Name)
			}
		})
	}
}

// TestDLToken_UncleanSealedNameDrops proves an unclean stem costs the filename and
// nothing else: the payload is AEAD-authenticated under our own keyring, so it can only
// come from harbrr minting it wrong, and failing the token would kill a live download.
func TestDLToken_UncleanSealedNameDrops(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	for _, stem := range []string{"trailing dot.", "trailing space ", "CON", "delete\x7fcontrol", strings.Repeat("A", maxDownloadNameBytes+1)} {
		t.Run(stem[:min(len(stem), 16)], func(t *testing.T) {
			t.Parallel()
			payload, err := json.Marshal(dlTokenPayload{CategoryID: 2000, Name: stem, Link: dlTestLink})
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}
			got, err := decodeDLToken(kr, "mytracker", sealedDLTestPayload(t, kr, "mytracker", string(payload)))
			if err != nil {
				t.Fatalf("decodeDLToken: %v", err)
			}
			if got.Name != "" {
				t.Errorf("unclean stem %q survived as %q, want it dropped", stem, got.Name)
			}
			if got.Link != dlTestLink {
				t.Error("round trip link differs from the sealed link (values withheld: link-shaped)")
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
	if payload.CategoryID != 2000 {
		t.Errorf("round trip category = %d, want 2000", payload.CategoryID)
	}
	if payload.Name != "Release.Name.2026" {
		t.Errorf("round trip name = %q, want %q", payload.Name, "Release.Name.2026")
	}
	if payload.Link != dlTestLink {
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

// TestDownloadAttachmentName_OversizedIndexerID pins the aggregate-feed edge:
// `profile:<name>` ids have no length cap, so an id whose suffix alone would
// exceed the filename budget must drop the suffix (never hand
// truncateDownloadName a limit below Clean's non-empty floor, where the trim
// loop cannot terminate), and the titleless fallback must come back cleaned and
// bounded rather than as the raw id.
func TestDownloadAttachmentName_OversizedIndexerID(t *testing.T) {
	t.Parallel()
	longID := "profile:" + strings.Repeat("p", 300)
	tests := []struct {
		name string
		stem string
		id   string
		want func(t *testing.T, got string)
	}{
		{name: "normal slug keeps the suffix", stem: "Release.Name.2026", id: "demo", want: func(t *testing.T, got string) {
			if got != "Release.Name.2026 [demo]" {
				t.Errorf("got %q, want the suffixed stem", got)
			}
		}},
		{name: "oversized id drops the suffix", stem: "Release.Name.2026", id: longID, want: func(t *testing.T, got string) {
			if got != "Release.Name.2026" {
				t.Errorf("got %q, want the bare stem (suffix dropped)", got)
			}
		}},
		{name: "titleless oversized id is cleaned and bounded", stem: "", id: longID, want: func(t *testing.T, got string) {
			if len(got) > maxDownloadNameBytes {
				t.Errorf("len = %d, over the %d budget", len(got), maxDownloadNameBytes)
			}
			if !pathologize.IsClean(got) {
				t.Errorf("titleless fallback %q is not a portable filename", got)
			}
		}},
		{name: "titleless slug is unchanged", stem: "", id: "demo", want: func(t *testing.T, got string) {
			if got != "demo" {
				t.Errorf("got %q, want the bare slug", got)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := downloadAttachmentName(tt.stem, tt.id)
			if len(got) > maxDownloadNameBytes {
				t.Errorf("len = %d, over the %d budget", len(got), maxDownloadNameBytes)
			}
			tt.want(t, got)
		})
	}
}
