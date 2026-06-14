package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// keyLen is the AES-256 key length in bytes.
const keyLen = 32

// aad builds the additional-authenticated-data binding a ciphertext to one
// (instance, setting) row, so a stored blob cannot be copied or replayed across
// rows or fields. The form is the bytes of "<instanceID>\x00<setting>" — a NUL
// separator that cannot appear in a decimal id or a YAML setting name. This is
// harbrr's hardening over qui, which passes no AAD (docs/ideas.md §9).
func aad(instanceID int64, setting string) []byte {
	return fmt.Appendf(nil, "%d\x00%s", instanceID, setting)
}

// seal encrypts plaintext with AES-256-GCM under key, authenticating ad, and
// returns base64(nonce‖ciphertext‖tag) — qui's construction, with a fresh random
// nonce prepended.
func seal(key, ad, plaintext []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("secrets: read nonce: %w", err)
	}
	blob := gcm.Seal(nonce, nonce, plaintext, ad)
	return base64.StdEncoding.EncodeToString(blob), nil
}

// open reverses seal. Its error never includes the plaintext or key material — a
// decryption failure means a wrong key, AAD mismatch, or tampering, and the caller
// must fail loud, not retry.
func open(key, ad []byte, blob string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return nil, fmt.Errorf("secrets: decode ciphertext: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("secrets: ciphertext shorter than the nonce")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, ad)
	if err != nil {
		return nil, errors.New("secrets: decryption failed (wrong key, AAD mismatch, or tampering)")
	}
	return pt, nil
}

// newGCM builds the AES-256-GCM AEAD for a 32-byte key.
func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("secrets: key must be %d bytes, got %d", keyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: new gcm: %w", err)
	}
	return gcm, nil
}
