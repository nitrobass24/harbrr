package torznabhttp

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/secrets"
)

func dlTokenPurpose(indexerID string) string {
	return "dl-proxy:" + indexerID
}

// dlTokenPayload is what the token seals: the release's parent category id, a
// semicolon, then the pre-resolution link ("2000;https://…"). The category rides along
// so a grab can be tallied under the right family (autobrr/harbrr#403) without a
// lookup at grab time. A link always starts with a scheme, never digits + ';', so
// splitting on that prefix is unambiguous — and a payload without one is read as a bare
// link, so a token minted before this field existed still grabs (uncategorised).
func dlTokenPayload(categoryID int, link string) string {
	return strconv.Itoa(categoryID) + ";" + link
}

// splitDLTokenPayload reverses dlTokenPayload, returning the category id (0 when the
// payload carries none) and the link.
func splitDLTokenPayload(payload string) (categoryID int, link string) {
	prefix, rest, ok := strings.Cut(payload, ";")
	if !ok {
		return 0, payload
	}
	id, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, payload
	}
	return id, rest
}

// encodeDLToken seals the pre-resolution download link and the release's parent
// category into an opaque, URL-safe token bound to indexerID, for the grab-time /dl
// proxy. The link may carry a passkey, so it must never reach the served feed in the
// clear:
//
// The token is always AEAD ciphertext, including when credential storage explicitly
// runs in plaintext mode. Plaintext mode uses a process-local transient token key, so
// its tokens expire across restarts instead of becoming forgeable. The AEAD purpose
// binds the token to indexerID, preventing cross-indexer replay.
//
// The result is base64url so it drops straight into a query parameter without
// escaping.
func encodeDLToken(kr *secrets.Keyring, indexerID string, categoryID int, link string) (string, error) {
	blob, err := kr.SealToken(dlTokenPurpose(indexerID), dlTokenPayload(categoryID, link))
	if err != nil {
		return "", fmt.Errorf("dl token: encrypt: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(blob)), nil
}

// decodeDLToken reverses encodeDLToken, returning the pre-resolution link and the
// release's parent category (0 when the token carries none). It fails when the token is
// malformed or was not minted for indexerID (an AAD mismatch, so a token cannot be
// replayed across indexers). The error never carries the link.
func decodeDLToken(kr *secrets.Keyring, indexerID, token string) (categoryID int, link string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, "", fmt.Errorf("dl token: decode: %w", err)
	}
	payload, err := kr.OpenToken(dlTokenPurpose(indexerID), string(raw))
	if err != nil {
		return 0, "", fmt.Errorf("dl token: decrypt: %w", err)
	}
	categoryID, link = splitDLTokenPayload(payload)
	return categoryID, link, nil
}
