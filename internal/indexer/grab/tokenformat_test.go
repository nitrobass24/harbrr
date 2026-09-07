package grab

import (
	"encoding/json"
	"testing"
)

// preMoveDLToken was minted by the /dl token codec as it stood in
// internal/web/torznabhttp, BEFORE that codec moved into this package
// (autobrr/harbrr#552), sealed under dlTestKey for indexer "demo". Every harbrr
// consumer holds tokens like it: an *arr's cached feed, a cross-seed tool's saved
// URL, a browser's download link. Opening it here is what proves the extraction was
// a move and not a re-design — a token format that drifted would fail nowhere until
// somebody's saved URL stopped working.
const preMoveDLToken = "SEVuaHd2TDFPU1Q3ZnVIRkNtNU94MlZUOWtYaUw1R3ZVeTVacE1vSDdLVDVtTy9jQTZrOXFiaEQ3MDdPamhnUE5CUGphZ0hWcmdqMHpTMTVFQXVjR1RWWGNXbHR2dk1hUy9jcmJDaHV2ZFVtOUd0NFBMdXkrM0pEUXVCVmx3WUpKZTNIVHJ4K3dFanVFZjR0TTZMdDVYMGIwZVVmVENZUUNPTGdKaTlW"

// preMoveDLPayload is what preMoveDLToken seals. The link is synthetic; its
// passkey-shaped tail is exactly the kind of secret the token exists to keep out of a
// served feed.
var preMoveDLPayload = dlTokenPayload{
	CategoryID: 2000,
	Name:       "Release.Name.2026",
	Link:       "https://demo.test/download.php?id=1&passkey=deadbeef",
}

// TestDLTokenFormatIsStable pins both halves of the wire contract: a token minted by
// the pre-move code still opens, and the plaintext under the seal still marshals to
// the same bytes. The second half is what catches a silent break the first cannot —
// the AEAD nonce is random, so a hand-minted token can only ever be opened, never
// compared; a renamed or reordered JSON field would still round-trip within one
// binary while breaking every token minted by another.
func TestDLTokenFormatIsStable(t *testing.T) {
	t.Parallel()

	got, err := decodeDLToken(encryptedKeyring(t), "demo", preMoveDLToken)
	if err != nil {
		t.Fatalf("a token minted before the move no longer opens: %v", err)
	}
	if got != preMoveDLPayload {
		t.Errorf("pre-move token decoded to %+v, want %+v", got, preMoveDLPayload)
	}

	// encoding/json escapes '&' by default, so that escape is part of the sealed
	// bytes, not a transcription artifact.
	const wantJSON = `{"c":2000,"n":"Release.Name.2026","l":"https://demo.test/download.php?id=1\u0026passkey=deadbeef"}`
	b, err := json.Marshal(preMoveDLPayload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if string(b) != wantJSON {
		t.Errorf("sealed payload JSON changed:\n got %s\nwant %s", b, wantJSON)
	}
}
