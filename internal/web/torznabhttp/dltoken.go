package torznabhttp

import (
	"encoding/base64"
	"fmt"

	"github.com/autobrr/harbrr/internal/secrets"
)

// dlTokenInstance is the fixed AEAD instance id for /dl tokens. The per-indexer
// binding lives in the setting string (dlTokenSetting), so a token minted for one
// indexer cannot be replayed against another: decoding under a different indexer id
// is an AAD mismatch and fails.
const dlTokenInstance = 0

func dlTokenSetting(indexerID string) string {
	return "dl-proxy:" + indexerID
}

// encodeDLToken seals the pre-resolution download link into an opaque, URL-safe
// token bound to indexerID, for the grab-time /dl proxy. The link may carry a
// passkey, so it must never reach the served feed in the clear:
//
//   - with an encryption key configured the token is AEAD ciphertext (the link is
//     unrecoverable without the key);
//   - in plaintext mode (no key — only under harbrr's loud startup warning) it
//     degrades to base64url(link): obscured from logs and URL secret-scanners but
//     recoverable, consistent with the plaintext-at-rest threat model that mode
//     already accepts.
//
// The result is base64url so it drops straight into a query parameter without
// escaping.
func encodeDLToken(kr *secrets.Keyring, indexerID, link string) (string, error) {
	blob, err := kr.Encrypt(dlTokenInstance, dlTokenSetting(indexerID), link)
	if err != nil {
		return "", fmt.Errorf("dl token: encrypt: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(blob)), nil
}

// decodeDLToken reverses encodeDLToken, returning the pre-resolution link. It fails
// when the token is malformed or was not minted for indexerID (an AAD mismatch, so
// a token cannot be replayed across indexers). The error never carries the link.
func decodeDLToken(kr *secrets.Keyring, indexerID, token string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("dl token: decode: %w", err)
	}
	link, err := kr.Decrypt(dlTokenInstance, dlTokenSetting(indexerID), string(raw))
	if err != nil {
		return "", fmt.Errorf("dl token: decrypt: %w", err)
	}
	return link, nil
}
