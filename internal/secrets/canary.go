package secrets

import (
	"errors"
	"fmt"
)

// canaryPlaintext is the fixed known value sealed into the canary record. Its
// AAD uses a reserved instance id (0) and setting name that no real row can take.
const (
	canaryPlaintext  = "harbrr-secrets-canary-v1"
	canaryInstanceID = 0
	canarySetting    = "__secrets_canary__"
)

// EncryptCanary returns the canary blob to persist alongside KeyID() on first run.
// On later runs VerifyCanary checks the stored pair, failing loud if the active
// key differs from the one that wrote the data (docs/ideas.md §9 startup canary).
func (k *Keyring) EncryptCanary() (string, error) {
	return k.Encrypt(canaryInstanceID, canarySetting, canaryPlaintext)
}

// VerifyCanary checks a stored (key_id, blob) pair against the active keyring. It
// returns an error — so the daemon refuses to start and never touches secrets —
// when the key has changed, the data is corrupt, or the encryption mode has
// flipped between plaintext and encrypted (the key_id differs in that case too).
func (k *Keyring) VerifyCanary(storedKeyID, storedBlob string) error {
	if storedKeyID != k.keyID {
		return fmt.Errorf(
			"secrets: encryption key changed (stored key_id %q, active %q) — restore the original key/keyfile, or reset stored secrets; refusing to start",
			storedKeyID, k.keyID,
		)
	}
	got, err := k.Decrypt(canaryInstanceID, canarySetting, storedBlob)
	if err != nil {
		return fmt.Errorf("secrets: canary verification failed — the encryption key is wrong or the data is corrupt: %w", err)
	}
	if got != canaryPlaintext {
		return errors.New("secrets: canary mismatch — the encryption key is wrong")
	}
	return nil
}
