package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/autobrr/harbrr/internal/config"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/secrets"
)

// newRotateKeyCmd rotates the at-rest encryption key: it re-encrypts every stored
// tracker secret (and the canary) from an old key to a new key. Offline — run with
// the daemon stopped. It dry-runs (decrypts every row under the old key) before any
// write, and applies the rewrite in a single transaction, so a wrong old key fails
// loud with the store untouched.
func newRotateKeyCmd() *cobra.Command {
	var oldKey, oldKeyFile, newKey, newKeyFile string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "rotate-key",
		Short: "Re-encrypt all stored tracker secrets under a new key (run with the daemon stopped)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgFile, err := cmd.Flags().GetString("config")
			if err != nil {
				return fmt.Errorf("read --config flag: %w", err)
			}
			cfg, err := config.Load(cfgFile, cmd.Flags())
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			oldKR, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: oldKey, KeyFile: oldKeyFile}, zerolog.Nop())
			if err != nil {
				return fmt.Errorf("rotate-key: old key: %w", err)
			}
			newKR, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: newKey, KeyFile: newKeyFile}, zerolog.Nop())
			if err != nil {
				return fmt.Errorf("rotate-key: new key: %w", err)
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			db, err := openDatabase(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()

			rep, err := rotateKeys(ctx, db, oldKR, newKR, dryRun)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if dryRun {
				fmt.Fprintf(out, "rotate-key dry-run OK: %d secret row(s) decrypt under the old key and would re-encrypt under key_id %s. Re-run without --dry-run to apply.\n", rep.rows, newKR.KeyID())
			} else {
				fmt.Fprintf(out, "rotate-key done: re-encrypted %d secret row(s) + canary under key_id %s.\n", rep.rows, newKR.KeyID())
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&oldKey, "old-key", "", "current encryption key (hex/base64); or use --old-key-file")
	f.StringVar(&oldKeyFile, "old-key-file", "", "path to the current key file")
	f.StringVar(&newKey, "new-key", "", "new encryption key (hex/base64); or use --new-key-file")
	f.StringVar(&newKeyFile, "new-key-file", "", "path to the new key file")
	f.BoolVar(&dryRun, "dry-run", false, "validate (decrypt every row under the old key) without writing")
	return cmd
}

// rotateReport summarizes a rotation (no secret material).
type rotateReport struct{ rows int }

// rotateKeys validates the old key against the stored key_id + canary and every
// secret row, then (unless dryRun) atomically re-encrypts every secret row, the
// canary, and the stored key_id under the new key. It never logs or returns a
// decrypted credential or key material.
func rotateKeys(ctx context.Context, db *database.DB, oldKR, newKR *secrets.Keyring, dryRun bool) (rotateReport, error) {
	if oldKR.Plaintext() || newKR.Plaintext() {
		return rotateReport{}, errors.New("rotate-key: plaintext mode has no key to rotate (configure encryption keys)")
	}
	meta := database.AppMeta{}
	storedID, haveID, err := meta.Get(ctx, db, canaryIDKey)
	if err != nil {
		return rotateReport{}, fmt.Errorf("rotate-key: read stored key id: %w", err)
	}
	switch {
	case !haveID:
		return rotateReport{}, errors.New("rotate-key: no stored secrets_key_id (nothing to rotate)")
	case storedID == "plaintext":
		return rotateReport{}, errors.New("rotate-key: the store is in plaintext mode (nothing to rotate)")
	case storedID != oldKR.KeyID():
		return rotateReport{}, errors.New("rotate-key: --old-key does not match the store's key id (wrong old key)")
	}
	if blob, ok, cerr := meta.Get(ctx, db, canaryBlobKey); cerr != nil {
		return rotateReport{}, fmt.Errorf("rotate-key: read canary: %w", cerr)
	} else if ok {
		if verr := oldKR.VerifyCanary(storedID, blob); verr != nil {
			return rotateReport{}, fmt.Errorf("rotate-key: old key fails the canary: %w", verr)
		}
	}

	rekeyed, err := reencryptRows(ctx, db, oldKR, newKR)
	if err != nil {
		return rotateReport{}, err
	}
	if dryRun {
		return rotateReport{rows: len(rekeyed)}, nil
	}
	if err := applyRotation(ctx, db, newKR, rekeyed); err != nil {
		return rotateReport{}, err
	}
	return rotateReport{rows: len(rekeyed)}, nil
}

// rekeyedRow is a secret row's id and its ciphertext re-sealed under the new key.
type rekeyedRow struct {
	id   int64
	blob string
}

// reencryptRows decrypts every secret row under the old key and re-encrypts it
// under the new key in memory, failing loud (before any write) on a wrong old key.
// An empty secret (empty ciphertext) stays empty — only its key_id is rotated.
func reencryptRows(ctx context.Context, db *database.DB, oldKR, newKR *secrets.Keyring) ([]rekeyedRow, error) {
	rows, err := (database.Rotation{}).AllSecrets(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("rotate-key: %w", err)
	}
	out := make([]rekeyedRow, 0, len(rows))
	for _, r := range rows {
		if r.ValueEncrypted == "" {
			out = append(out, rekeyedRow{id: r.ID, blob: ""})
			continue
		}
		pt, derr := oldKR.Decrypt(r.InstanceID, r.Name, r.ValueEncrypted)
		if derr != nil {
			return nil, fmt.Errorf("rotate-key: cannot decrypt setting %d under the old key (wrong key or tampered data)", r.ID)
		}
		blob, eerr := newKR.Encrypt(r.InstanceID, r.Name, pt)
		if eerr != nil {
			return nil, fmt.Errorf("rotate-key: re-encrypt setting %d: %w", r.ID, eerr)
		}
		out = append(out, rekeyedRow{id: r.ID, blob: blob})
	}
	return out, nil
}

// applyRotation writes the re-encrypted rows + the new canary + the new key_id in a
// single transaction, so the store is never left half-rotated.
func applyRotation(ctx context.Context, db *database.DB, newKR *secrets.Keyring, rekeyed []rekeyedRow) error {
	newCanary, err := newKR.EncryptCanary()
	if err != nil {
		return fmt.Errorf("rotate-key: seal canary: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rotate-key: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	rot := database.Rotation{}
	for _, e := range rekeyed {
		if uerr := rot.UpdateSecret(ctx, tx, e.id, e.blob, newKR.KeyID()); uerr != nil {
			return fmt.Errorf("rotate-key: %w", uerr)
		}
	}
	meta := database.AppMeta{}
	if serr := meta.Set(ctx, tx, canaryBlobKey, newCanary); serr != nil {
		return fmt.Errorf("rotate-key: write canary: %w", serr)
	}
	if serr := meta.Set(ctx, tx, canaryIDKey, newKR.KeyID()); serr != nil {
		return fmt.Errorf("rotate-key: write key id: %w", serr)
	}
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("rotate-key: commit: %w", cerr)
	}
	committed = true
	return nil
}
