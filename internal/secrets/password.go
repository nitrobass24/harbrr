package secrets

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2Params are the argon2id cost parameters. They match qui's defaults
// (docs/security.md): 64 MiB memory, 3 iterations, 2 lanes, a 16-byte salt, and a
// 32-byte derived key.
type argon2Params struct {
	memory  uint32
	time    uint32
	threads uint8
	saltLen uint32
	keyLen  uint32
}

// defaultArgon2 is the parameter set used to hash new passwords.
var defaultArgon2 = argon2Params{memory: 64 * 1024, time: 3, threads: 2, saltLen: 16, keyLen: 32}

// argon2Version is the argon2 algorithm version encoded in the PHC string.
const argon2Version = argon2.Version

// HashPassword hashes a plaintext password with argon2id and returns a PHC-encoded
// string ($argon2id$v=19$m=...,t=...,p=...$salt$hash). The result is one-way: the
// password is never recoverable, so a database or key compromise never yields it.
func HashPassword(password string) (string, error) {
	p := defaultArgon2
	salt := make([]byte, p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("secrets: read salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	return encodePHC(p, salt, hash), nil
}

// VerifyPassword reports whether password matches a PHC-encoded argon2id hash,
// comparing in constant time. A malformed encoding returns an error.
func VerifyPassword(password, encoded string) (bool, error) {
	p, salt, want, err := decodePHC(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// encodePHC renders an argon2id hash in the standard PHC string format.
func encodePHC(p argon2Params, salt, hash []byte) string {
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
}

// decodePHC parses a PHC argon2id string into its parameters, salt, and hash.
func decodePHC(encoded string) (argon2Params, []byte, []byte, error) {
	var p argon2Params
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash
	if len(parts) != 6 || parts[1] != "argon2id" {
		return p, nil, nil, errors.New("secrets: not an argon2id PHC string")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2Version {
		return p, nil, nil, fmt.Errorf("secrets: unsupported argon2 version %q", parts[2])
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return p, nil, nil, fmt.Errorf("secrets: malformed argon2 params %q", parts[3])
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, fmt.Errorf("secrets: decode salt: %w", err)
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return p, nil, nil, fmt.Errorf("secrets: decode hash: %w", err)
	}
	p.saltLen = uint32(len(salt))
	p.keyLen = uint32(len(hash))
	return p, salt, hash, nil
}
