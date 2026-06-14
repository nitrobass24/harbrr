// Package secrets implements harbrr's three-class credential model (docs/ideas.md
// §9): tracker credentials encrypted at rest with AES-256-GCM (Keyring), the
// web-UI password hashed with argon2id (HashPassword/VerifyPassword), and API
// keys / session tokens hashed with SHA-256 (GenerateAPIKey/HashToken/VerifyToken).
// It also carries the <redacted> sentinel used so a stored secret is never echoed
// back to a client.
//
// The rule of thumb: anything harbrr must replay to a tracker is encrypted;
// anything it only needs to verify is hashed and never recoverable.
package secrets

// Redacted is the placeholder returned in place of a stored secret in management
// API responses and config exports. Re-submitting it on an update means "keep the
// stored value unchanged" (see the registry). It mirrors qui's domain sentinel.
//
// It is reserved: a user cannot store the literal string "<redacted>" as a secret
// value, because doing so would be indistinguishable from "leave unchanged".
const Redacted = "<redacted>"

// IsRedacted reports whether s is the redaction sentinel.
func IsRedacted(s string) bool { return s == Redacted }
