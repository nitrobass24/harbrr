package secrets

import (
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// PassphraseSaltLen is the salt length, in bytes, for DeriveKeyFromPassphrase. A fresh
// random salt per encryption makes the derived key unique even for a reused passphrase.
const PassphraseSaltLen = 16

// PassphraseKDF self-describes how DeriveKeyFromPassphrase derived a key: the algorithm
// and its argon2id cost parameters. A passphrase-sealed payload carries this alongside
// the salt so an importer re-derives the exact same key — and a future parameter change
// stays backward-decryptable (the importer reads the params it was sealed with).
type PassphraseKDF struct {
	Algorithm string `json:"algorithm"` // always "argon2id"
	Memory    uint32 `json:"memory"`    // KiB
	Time      uint32 `json:"time"`      // iterations
	Threads   uint8  `json:"threads"`   // lanes
}

// Argon2id cost bounds a bundle's recorded KDF params must fall within. The default set
// sits inside this window, and a future cost bump stays inside it too, so old bundles
// keep decrypting; a corrupt or hostile envelope can neither force a trivially weak key
// nor an OOM-sized derivation. Memory is in KiB.
const (
	minArgon2Memory  = 8 * 1024    // 8 MiB
	maxArgon2Memory  = 1024 * 1024 // 1 GiB
	minArgon2Time    = 1
	maxArgon2Time    = 32
	minArgon2Threads = 1
	maxArgon2Threads = 16
)

// DefaultPassphraseKDF returns the KDF descriptor a new export seals with (harbrr's
// standard argon2id cost set, shared with password hashing).
func DefaultPassphraseKDF() PassphraseKDF {
	p := defaultArgon2
	return PassphraseKDF{Algorithm: "argon2id", Memory: p.memory, Time: p.time, Threads: p.threads}
}

// Valid reports whether the KDF descriptor is a supported algorithm with in-bounds cost
// parameters (so an importer can reject a corrupt/hostile envelope before deriving).
func (k PassphraseKDF) Valid() bool {
	return k.Algorithm == "argon2id" &&
		k.Memory >= minArgon2Memory && k.Memory <= maxArgon2Memory &&
		k.Time >= minArgon2Time && k.Time <= maxArgon2Time &&
		k.Threads >= minArgon2Threads && k.Threads <= maxArgon2Threads
}

// NewPassphraseSalt returns a fresh random salt for DeriveKeyFromPassphrase.
func NewPassphraseSalt() ([]byte, error) {
	salt := make([]byte, PassphraseSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("secrets: read passphrase salt: %w", err)
	}
	return salt, nil
}

// DeriveKeyFromPassphrase derives a 32-byte AES-256 key from a passphrase and salt using
// argon2id with the cost parameters in kdf. It is deterministic in (passphrase, salt,
// kdf): the same inputs always yield the same key. The importer passes the kdf RECORDED
// in the bundle — not a compile-time default — so a bundle stays decryptable even after
// harbrr's default cost is raised. Independent of the at-rest keyring, so a backup is
// portable across hosts and at-rest keys. An unsupported algorithm or out-of-bounds
// params is an error rather than a silently-wrong key.
func DeriveKeyFromPassphrase(passphrase string, salt []byte, kdf PassphraseKDF) ([]byte, error) {
	if !kdf.Valid() {
		return nil, fmt.Errorf("secrets: unsupported key-derivation parameters (%s m=%d t=%d p=%d)",
			kdf.Algorithm, kdf.Memory, kdf.Time, kdf.Threads)
	}
	return argon2.IDKey([]byte(passphrase), salt, kdf.Time, kdf.Memory, kdf.Threads, keyLen), nil
}

// EncryptWithKey seals plaintext under a caller-supplied 32-byte key with AES-256-GCM,
// authenticating aad, and returns base64(nonce‖ciphertext‖tag). Unlike Keyring.Encrypt
// (which binds the AAD to a database row and uses the at-rest key), this takes the key
// and AAD directly — for a payload sealed under a passphrase-derived key. The key must
// be exactly 32 bytes; DeriveKeyFromPassphrase always produces that.
func EncryptWithKey(key, aad, plaintext []byte) (string, error) {
	return seal(key, aad, plaintext)
}

// DecryptWithKey reverses EncryptWithKey. A wrong passphrase (hence wrong derived key)
// or a tampered payload fails the GCM tag check and returns the leak-free open error, so
// a wrong passphrase is a clean, distinguishable failure rather than garbage output.
func DecryptWithKey(key, aad []byte, blob string) ([]byte, error) {
	return open(key, aad, blob)
}
