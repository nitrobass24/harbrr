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
// the plaintext is never persisted (docs/security.md, bearer-token class).
func GenerateAPIKey() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("secrets: generate api key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken returns the SHA-256 hex digest stored for an API key or session
// token. A plain SHA-256 (no salt, no slow KDF) is the correct construction here
// because the input is a 256-bit cryptographically-random token, not a
// low-entropy user-chosen secret — a slow hash buys nothing against a value that
// cannot be brute-forced or guessed. Passwords are NEVER hashed here; they go
// through argon2id (HashPassword). This is why a static analyzer flagging SHA-256
// on "sensitive data" is a false positive for this function.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// VerifyToken reports whether a presented token matches a stored SHA-256 hex
// hash, comparing in constant time.
func VerifyToken(token, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(storedHash)) == 1
}
