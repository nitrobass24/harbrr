package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/autobrr/harbrr/internal/app"
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
			db, err := app.OpenDatabase(ctx, cfg)
			if err != nil {
				return fmt.Errorf("rotate-key: %w", err)
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
	storedID, haveID, err := meta.Get(ctx, db, app.CanaryIDKey)
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
	if blob, ok, cerr := meta.Get(ctx, db, app.CanaryBlobKey); cerr != nil {
		return rotateReport{}, fmt.Errorf("rotate-key: read canary: %w", cerr)
	} else if ok {
		if verr := oldKR.VerifyCanary(storedID, blob); verr != nil {
			return rotateReport{}, fmt.Errorf("rotate-key: old key fails the canary: %w", verr)
		}
	}

	plan, err := reencryptAll(ctx, db, oldKR, newKR)
	if err != nil {
		return rotateReport{}, err
	}
	if dryRun {
		return rotateReport{rows: plan.count()}, nil
	}
	if err := applyRotation(ctx, db, newKR, plan); err != nil {
		return rotateReport{}, err
	}
	return rotateReport{rows: plan.count()}, nil
}

// rekeyedRow is a secret row's id and its ciphertext re-sealed under the new key.
type rekeyedRow struct {
	id   int64
	blob string
}

// rekeyedSurface holds a fixed-AAD surface's re-sealed rows (each row's ciphertext
// columns are parallel to the surface's Columns).
type rekeyedSurface struct {
	surface database.FixedAADSurface
	rows    []rekeyedSurfaceRow
}

// rekeyedSurfaceRow is one surface row's id and its ciphertext columns re-sealed
// under the new key, in the surface's Columns order.
type rekeyedSurfaceRow struct {
	id    int64
	blobs []string
}

// rekeyPlan is every secret in the store re-sealed under the new key, held in memory
// before any write so a wrong old key fails loud with the store untouched. It spans
// indexer_settings plus every fixed-AAD surface (SecretSurfaces).
type rekeyPlan struct {
	settings []rekeyedRow
	surfaces []rekeyedSurface
}

// count is the number of secret rows re-sealed across every table (for the report).
func (p rekeyPlan) count() int {
	n := len(p.settings)
	for _, s := range p.surfaces {
		n += len(s.rows)
	}
	return n
}

// reencryptAll decrypts every stored secret under the old key and re-encrypts it
// under the new key in memory — indexer_settings and every fixed-AAD surface — using
// each surface's exact sealing AAD. It fails loud (before any write) on a wrong old
// key. Nothing here is persisted; applyRotation writes the plan atomically.
func reencryptAll(ctx context.Context, db *database.DB, oldKR, newKR *secrets.Keyring) (rekeyPlan, error) {
	settings, err := reencryptRows(ctx, db, oldKR, newKR)
	if err != nil {
		return rekeyPlan{}, err
	}
	rot := database.Rotation{}
	surfaces := make([]rekeyedSurface, 0, len(database.SecretSurfaces()))
	for _, s := range database.SecretSurfaces() {
		rs, serr := reencryptSurface(ctx, db, rot, s, oldKR, newKR)
		if serr != nil {
			return rekeyPlan{}, serr
		}
		surfaces = append(surfaces, rs)
	}
	return rekeyPlan{settings: settings, surfaces: surfaces}, nil
}

// reencryptRows decrypts every indexer_settings secret row under the old key and
// re-encrypts it under the new key in memory, failing loud (before any write) on a
// wrong old key. An empty secret (empty ciphertext) stays empty — only key_id rotates.
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

// reencryptSurface decrypts every ciphertext column of one fixed-AAD surface under
// the old key (AAD = row id + the column's constant discriminator) and re-encrypts
// it under the new key, failing loud on a wrong old key. An empty ciphertext column
// stays empty — only key_id rotates.
func reencryptSurface(ctx context.Context, db *database.DB, rot database.Rotation, s database.FixedAADSurface, oldKR, newKR *secrets.Keyring) (rekeyedSurface, error) {
	rows, err := rot.SurfaceRows(ctx, db, s)
	if err != nil {
		return rekeyedSurface{}, fmt.Errorf("rotate-key: %w", err)
	}
	out := make([]rekeyedSurfaceRow, 0, len(rows))
	for _, r := range rows {
		blobs := make([]string, len(s.Columns))
		for i, col := range s.Columns {
			if r.Ciphers[i] == "" {
				continue
			}
			pt, derr := oldKR.Decrypt(r.ID, col.Setting, r.Ciphers[i])
			if derr != nil {
				return rekeyedSurface{}, fmt.Errorf("rotate-key: cannot decrypt %s.%s %d under the old key (wrong key or tampered data)", s.Table, col.Cipher, r.ID)
			}
			blob, eerr := newKR.Encrypt(r.ID, col.Setting, pt)
			if eerr != nil {
				return rekeyedSurface{}, fmt.Errorf("rotate-key: re-encrypt %s.%s %d: %w", s.Table, col.Cipher, r.ID, eerr)
			}
			blobs[i] = blob
		}
		out = append(out, rekeyedSurfaceRow{id: r.ID, blobs: blobs})
	}
	return rekeyedSurface{surface: s, rows: out}, nil
}

// applyRotation writes the whole re-encrypted plan + the new canary + the new key_id
// in a single transaction, so the store is never left half-rotated across tables.
func applyRotation(ctx context.Context, db *database.DB, newKR *secrets.Keyring, plan rekeyPlan) error {
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
	for _, e := range plan.settings {
		if uerr := rot.UpdateSecret(ctx, tx, e.id, e.blob, newKR.KeyID()); uerr != nil {
			return fmt.Errorf("rotate-key: %w", uerr)
		}
	}
	for _, rs := range plan.surfaces {
		for _, e := range rs.rows {
			if uerr := rot.UpdateSurface(ctx, tx, rs.surface, e.id, e.blobs, newKR.KeyID()); uerr != nil {
				return fmt.Errorf("rotate-key: %w", uerr)
			}
		}
	}
	meta := database.AppMeta{}
	if serr := meta.Set(ctx, tx, app.CanaryBlobKey, newCanary); serr != nil {
		return fmt.Errorf("rotate-key: write canary: %w", serr)
	}
	if serr := meta.Set(ctx, tx, app.CanaryIDKey, newKR.KeyID()); serr != nil {
		return fmt.Errorf("rotate-key: write key id: %w", serr)
	}
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("rotate-key: commit: %w", cerr)
	}
	committed = true
	return nil
}
