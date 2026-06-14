package secrets

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
)

const (
	// keyDirName / keyFileName locate the auto-generated keyfile under the data dir.
	keyDirName  = ".keys"
	keyFileName = "harbrr.key"
	// plaintextKeyID is the key_id recorded in plaintext mode, so a later switch to
	// encryption (or vice versa) trips the canary mismatch and fails loud.
	plaintextKeyID = "plaintext"
)

// KeyringOptions selects the at-rest encryption key source. It is the secrets
// package's own input (mapped from config by the server), so this package does
// not depend on internal/config.
type KeyringOptions struct {
	// EncryptionKey is an inline/env 32-byte key, hex (64 chars) or base64.
	EncryptionKey string
	// KeyFile is a path to a key file (raw 32 bytes or an encoded 32-byte key).
	KeyFile string
	// AllowPlaintext opts into UNENCRYPTED storage when no key is configured and
	// none has been auto-generated. Without it, encryption is always on.
	AllowPlaintext bool
	// DataDir is where the keyfile is auto-generated (<DataDir>/.keys/harbrr.key).
	DataDir string
}

// Keyring holds the resolved encryption key and its id. It encrypts/decrypts
// tracker credentials; the key never leaves this struct.
type Keyring struct {
	key       []byte
	keyID     string
	plaintext bool
}

// KeyID returns the identifier of the active key, stored with every encrypted
// record so a wrong/changed key is detected (and rotation is possible later). It
// is "plaintext" in plaintext mode.
func (k *Keyring) KeyID() string { return k.keyID }

// Plaintext reports whether the keyring stores values unencrypted (the explicit
// allow_plaintext opt-in).
func (k *Keyring) Plaintext() bool { return k.plaintext }

// Encrypt returns the stored representation of a setting's secret value, bound to
// the (instanceID, setting) AAD. In plaintext mode it returns the value unchanged.
func (k *Keyring) Encrypt(instanceID int64, setting, plaintext string) (string, error) {
	if k.plaintext {
		return plaintext, nil
	}
	return seal(k.key, aad(instanceID, setting), []byte(plaintext))
}

// Decrypt reverses Encrypt for the same (instanceID, setting). A failure (wrong
// key, AAD mismatch, tampering) is returned, never swallowed.
func (k *Keyring) Decrypt(instanceID int64, setting, blob string) (string, error) {
	if k.plaintext {
		return blob, nil
	}
	pt, err := open(k.key, aad(instanceID, setting), blob)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// OpenKeyring resolves the encryption key in precedence order: an inline
// EncryptionKey, then a KeyFile, then the auto-generated keyfile under DataDir. If
// none exists, encryption is on by default (a keyfile is generated) unless
// AllowPlaintext is set, in which case storage is plaintext. A configured KeyFile
// that is missing/unreadable is fatal — never a silent fallback to plaintext.
func OpenKeyring(opts KeyringOptions, log zerolog.Logger) (*Keyring, error) {
	key, err := resolveConfiguredKey(opts)
	if err != nil {
		return nil, err
	}
	if key != nil {
		return newKeyring(key), nil
	}
	return openAutoKeyring(opts, log)
}

// resolveConfiguredKey returns an explicitly configured key (inline or file), or
// nil when none is configured.
func resolveConfiguredKey(opts KeyringOptions) ([]byte, error) {
	if opts.EncryptionKey != "" {
		key, err := decodeKey(opts.EncryptionKey)
		if err != nil {
			return nil, fmt.Errorf("secrets: encryption_key: %w", err)
		}
		return key, nil
	}
	if opts.KeyFile != "" {
		key, err := readKeyFile(opts.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("secrets: key_file %q: %w", opts.KeyFile, err)
		}
		return key, nil
	}
	return nil, nil
}

// openAutoKeyring uses the existing auto-keyfile if present (encryption takes
// precedence — to downgrade to plaintext, delete the keyfile first), else either
// opts into plaintext or generates a new keyfile (encryption on). An empty DataDir
// has nowhere stable to anchor a keyfile, so it is rejected unless plaintext is
// explicitly allowed — never a relative-path keyfile whose key_id drifts with cwd.
func openAutoKeyring(opts KeyringOptions, log zerolog.Logger) (*Keyring, error) {
	if opts.DataDir == "" {
		if opts.AllowPlaintext {
			return plaintextKeyring(log), nil
		}
		return nil, errors.New("secrets: no encryption key configured and data_dir is empty — set secrets.encryption_key or secrets.key_file, provide a data_dir for the auto-generated keyfile, or set secrets.allow_plaintext to store unencrypted")
	}

	autoPath := filepath.Join(opts.DataDir, keyDirName, keyFileName)
	key, ok, err := tryReadKeyFile(autoPath)
	if err != nil {
		return nil, fmt.Errorf("secrets: read auto keyfile %q: %w", autoPath, err)
	}
	if ok {
		return newKeyring(key), nil
	}

	if opts.AllowPlaintext {
		return plaintextKeyring(log), nil
	}

	key, err = generateKeyFile(autoPath)
	if err != nil {
		return nil, err
	}
	log.Info().
		Str("keyfile", autoPath).
		Msg("secrets: generated a new encryption keyfile — BACK IT UP separately from the database; losing it means re-entering tracker credentials")
	return newKeyring(key), nil
}

// plaintextKeyring builds the unencrypted keyring, warning loudly (the explicit
// allow_plaintext opt-in).
func plaintextKeyring(log zerolog.Logger) *Keyring {
	log.Warn().Msg("secrets: allow_plaintext is set and no encryption key is configured — tracker credentials will be stored UNENCRYPTED")
	return &Keyring{plaintext: true, keyID: plaintextKeyID}
}

// newKeyring builds an encrypting Keyring with a key_id derived from the key.
func newKeyring(key []byte) *Keyring {
	return &Keyring{key: key, keyID: deriveKeyID(key)}
}

// deriveKeyID is a stable, one-way identifier for a key: the first 64 bits of its
// SHA-256, hex-encoded. It reveals nothing usable about the key, is identical for
// the same key across runs (so the canary verifies), and differs for a changed key
// (so a swap is caught).
func deriveKeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8])
}

// decodeKey parses a 32-byte key from a hex (64-char) or base64 string.
func decodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if len(s) == hex.EncodedLen(keyLen) {
		if b, err := hex.DecodeString(s); err == nil && len(b) == keyLen {
			return b, nil
		}
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == keyLen {
			return b, nil
		}
	}
	return nil, fmt.Errorf("must be a %d-byte key as hex (%d chars) or base64", keyLen, hex.EncodedLen(keyLen))
}

// readKeyFile reads a key from path: raw 32 bytes, or an encoded 32-byte key.
func readKeyFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-configured.
	if err != nil {
		return nil, err //nolint:wrapcheck // callers wrap with the source label.
	}
	if len(data) == keyLen {
		return data, nil
	}
	return decodeKey(string(data))
}

// tryReadKeyFile reads path if it exists; ok=false (no error) when it does not.
func tryReadKeyFile(path string) (key []byte, ok bool, err error) {
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, false, nil
		}
		return nil, false, statErr //nolint:wrapcheck // caller wraps with the path.
	}
	key, err = readKeyFile(path)
	return key, err == nil, err
}

// generateKeyFile creates a fresh 32-byte key, writes it 0600 under a 0700 key
// directory, and returns it.
func generateKeyFile(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("secrets: create key dir: %w", err)
	}
	key := make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("secrets: generate key: %w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("secrets: write keyfile: %w", err)
	}
	return key, nil
}
