package secrets

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// tokenBytes is the entropy of a generated API key (32 bytes = 256 bits).
const tokenBytes = 32

// GenerateAPIKey returns a new high-entropy API key as a URL-safe base64 string.
// The caller shows it to the user exactly once and stores only HashToken(it):
// the plaintext is never persisted (docs/ideas.md §9, bearer-token class).
func GenerateAPIKey() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("secrets: generate api key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken returns the SHA-256 hex digest stored for an API key or session
// token. A plain SHA-256 (no salt) is correct here because the input is
// already high-entropy random, unlike a user-chosen password.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// VerifyToken reports whether a presented token matches a stored SHA-256 hex
// hash, comparing in constant time.
func VerifyToken(token, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(storedHash)) == 1
}
