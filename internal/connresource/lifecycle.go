package connresource

import (
	"context"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/secrets"
)

// Lifecycle sequences the create/update/delete lifecycle of one connection-
// resource type T, encrypting its secrets at rest and revoking any minted key it
// owns. It has no state of its own beyond its dependencies (db, keyring, clock)
// and no fields that vary per call — every per-call decision comes from the Spec
// passed to Create/Update/Delete.
type Lifecycle[T any] struct {
	db      dbinterface.Querier
	keyring *secrets.Keyring
	clock   func() time.Time
}

// New builds a Lifecycle for resource type T. Dependencies are accepted, not
// created: db and keyring are shared with the rest of the service, and clock is
// injectable for deterministic tests (callers whose own clock field is itself
// mutable in tests should pass a thunk that reads it, e.g.
// func() time.Time { return s.clock() }, so a later reassignment of s.clock is
// still observed).
func New[T any](db dbinterface.Querier, keyring *secrets.Keyring, clock func() time.Time) *Lifecycle[T] {
	return &Lifecycle[T]{db: db, keyring: keyring, clock: clock}
}

// CreateSpec is the input to Lifecycle.Create. Build, Insert and Finalize are
// required; Minter/MintName, Secret/SetSecret and Conflict are optional.
type CreateSpec[T any] struct {
	// Minter mints the resource's dedicated harbrr key before Build runs, and is
	// revoked (fail-closed: a revoke failure is returned alongside the create
	// failure) if anything after the mint fails. Nil when the resource mints
	// nothing (notify).
	Minter KeyMinter
	// MintName is the minted key's display name. Only read when Minter is non-nil.
	MintName string

	// Build constructs the pre-insert entity from the caller's already-validated
	// params (closed over), now (the lifecycle's clock reading), and the minted
	// key id (0 when Minter is nil).
	Build func(now time.Time, mintedKeyID int64) T

	// Insert writes the row and returns its new id. A unique-constraint violation
	// is detected by Lifecycle itself (via database.IsUniqueViolation) and does
	// not need to be special-cased here.
	Insert func(ctx context.Context, q dbinterface.Execer, entity T) (int64, error)

	// Secret returns the plaintext secret to seal once the entity's id is known,
	// given the entity and the minted key's plaintext (empty when Minter is nil).
	// Nil when the row itself seals nothing (download — its credential lives on
	// the App), in which case SetSecret is not called either.
	Secret func(entity T, mintedPlain string) Secret

	// SetSecret writes the sealed secret plus the active key id back onto the row.
	// Only read when Secret is non-nil.
	SetSecret func(ctx context.Context, q dbinterface.Execer, id int64, encrypted, keyID string) error

	// Finalize returns the entity to hand back to the caller: entity with its id
	// and sealed secret applied (both blank when Secret is nil).
	Finalize func(entity T, id int64, encrypted, keyID string) T

	// Conflict formats the domain.ErrConflict-wrapped error for a unique-
	// constraint violation on Insert. Nil means a unique violation is not
	// translated — it is wrapped plainly instead (notify's create has no unique
	// constraint to map today).
	Conflict func(entity T) error
}

// Create mints a key (if Minter is set), builds and inserts the entity, then
// seals its secret (if Secret is set) — the row is written first so its id can
// bind the secret's encryption AAD. A failure after a successful mint revokes the
// orphaned key, fail-closed: if the revoke itself fails, that failure is surfaced alongside the
// original error rather than swallowed, since an unrevoked key remains a live
// credential.
func (l *Lifecycle[T]) Create(ctx context.Context, spec CreateSpec[T]) (T, error) {
	var zero T
	var mintedKeyID int64
	var mintedPlain string
	if spec.Minter != nil {
		plain, key, err := spec.Minter.MintAPIKey(ctx, spec.MintName)
		if err != nil {
			return zero, fmt.Errorf("connresource: mint key: %w", err)
		}
		mintedKeyID, mintedPlain = key.ID, plain
	}

	entity := spec.Build(l.clock(), mintedKeyID)
	result, err := l.insertSealed(ctx, spec, entity, mintedPlain)
	if err != nil && spec.Minter != nil {
		return zero, revokeOrphan(ctx, spec.Minter, mintedKeyID, err)
	}
	if err != nil {
		return zero, err
	}
	return result, nil
}

// insertSealed runs the insert-then-seal body in one transaction.
func (l *Lifecycle[T]) insertSealed(ctx context.Context, spec CreateSpec[T], entity T, mintedPlain string) (T, error) {
	var zero T
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, fmt.Errorf("connresource: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	id, err := spec.Insert(ctx, tx, entity)
	if err != nil {
		if spec.Conflict != nil && database.IsUniqueViolation(err) {
			return zero, spec.Conflict(entity)
		}
		return zero, fmt.Errorf("connresource: insert: %w", err)
	}

	var encrypted, keyID string
	if spec.Secret != nil {
		if encrypted, keyID, err = Seal(l.keyring, id, spec.Secret(entity, mintedPlain)); err != nil {
			return zero, err
		}
		if err := spec.SetSecret(ctx, tx, id, encrypted, keyID); err != nil {
			return zero, fmt.Errorf("connresource: set secret: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return zero, fmt.Errorf("connresource: commit: %w", err)
	}
	return spec.Finalize(entity, id, encrypted, keyID), nil
}

// revokeOrphan revokes a just-minted key after a failed create, fail-closed: a
// revoke failure is surfaced alongside createErr (never swallowed into a log
// line), since an unrevoked orphan key remains a valid credential.
func revokeOrphan(ctx context.Context, minter KeyMinter, keyID int64, createErr error) error {
	if revErr := minter.RevokeAPIKey(ctx, keyID); revErr != nil {
		return fmt.Errorf("%w (and its orphan key %d could not be revoked — revoke it manually: %w)",
			createErr, keyID, revErr)
	}
	return createErr
}

// UpdateSpec is the input to Lifecycle.Update. Get and Write are required;
// Patch, Rotate/Apply and Touch are optional.
type UpdateSpec[T any] struct {
	// Get reads the current row inside the update transaction. A caller whose
	// update needs to validate something else against that same transaction does
	// it here (appsync's sync-profile reference check), so the check cannot be
	// raced by a concurrent write between the read and the write.
	Get func(ctx context.Context, q dbinterface.Execer, id int64) (T, error)

	// Patch mutates entity's non-secret fields from the caller's patch params
	// (closed over), validating as it goes.
	Patch func(entity *T) error

	// Rotate reports the secret to (re)seal, or ok=false when this update leaves
	// the secret untouched. Runs after Patch.
	Rotate func(entity *T) (secret Secret, ok bool, err error)
	// Apply stores a rotated secret's sealed value and key id onto entity. Only
	// read when Rotate is non-nil.
	Apply func(entity *T, encrypted, keyID string)

	// Touch stamps entity's updated-at field with now. Optional (present in every
	// adopter today, but not required by the shape).
	Touch func(entity *T, now time.Time)

	// Write persists the full patched row.
	Write func(ctx context.Context, q dbinterface.Execer, entity T) error

	// Conflict formats the domain.ErrConflict-wrapped error for a unique-
	// constraint violation on Write. Nil means a unique violation is wrapped
	// plainly instead.
	Conflict func(entity T) error
}

// Update reads, patches, optionally rotates a secret, and writes back the full
// row — all inside one transaction, so two overlapping PATCHes cannot lose each
// other's write (the second reads the first's committed row rather than stale
// state captured before it).
func (l *Lifecycle[T]) Update(ctx context.Context, id int64, spec UpdateSpec[T]) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("connresource: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	entity, err := spec.Get(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("connresource: get: %w", err)
	}
	if err := l.applyUpdate(id, &entity, spec); err != nil {
		return err
	}
	if err := spec.Write(ctx, tx, entity); err != nil {
		if spec.Conflict != nil && database.IsUniqueViolation(err) {
			return spec.Conflict(entity)
		}
		return fmt.Errorf("connresource: update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("connresource: commit: %w", err)
	}
	return nil
}

// applyUpdate runs the patch/rotate/touch steps between Get and Write.
func (l *Lifecycle[T]) applyUpdate(id int64, entity *T, spec UpdateSpec[T]) error {
	if spec.Patch != nil {
		if err := spec.Patch(entity); err != nil {
			return err
		}
	}
	if spec.Rotate != nil {
		sec, ok, err := spec.Rotate(entity)
		if err != nil {
			return err
		}
		if ok {
			enc, keyID, err := Seal(l.keyring, id, sec)
			if err != nil {
				return err
			}
			spec.Apply(entity, enc, keyID)
		}
	}
	if spec.Touch != nil {
		spec.Touch(entity, l.clock())
	}
	return nil
}

// DeleteSpec is the input to Lifecycle.Delete. Get and Delete are required;
// Minter/MintedKeyID are optional (nil Minter means the resource mints nothing
// and Delete never attempts a revoke — notify, download).
type DeleteSpec[T any] struct {
	// Get reads the row before it is deleted, so MintedKeyID can find its minted
	// key reference (if any).
	Get func(ctx context.Context, q dbinterface.Execer, id int64) (T, error)
	// Delete removes the row.
	Delete func(ctx context.Context, q dbinterface.Execer, id int64) error

	// Minter revokes the resource's minted key after a successful delete. Nil
	// when the resource mints nothing.
	Minter KeyMinter
	// MintedKeyID extracts the minted key id from the fetched entity. A zero
	// result skips the revoke (a resource whose key was already revoked out of
	// band). Only read when Minter is non-nil.
	MintedKeyID func(entity T) int64
}

// Delete removes the row, then revokes its minted key if it has one — fail
// closed: the row is already gone, but a still-valid minted key would remain a
// live credential, so a revoke failure is returned rather than swallowed.
func (l *Lifecycle[T]) Delete(ctx context.Context, id int64, spec DeleteSpec[T]) error {
	entity, err := spec.Get(ctx, l.db, id)
	if err != nil {
		return fmt.Errorf("connresource: get: %w", err)
	}
	if err := spec.Delete(ctx, l.db, id); err != nil {
		return fmt.Errorf("connresource: delete: %w", err)
	}
	if spec.Minter == nil || spec.MintedKeyID == nil {
		return nil
	}
	keyID := spec.MintedKeyID(entity)
	if keyID == 0 {
		return nil
	}
	if err := spec.Minter.RevokeAPIKey(ctx, keyID); err != nil {
		return fmt.Errorf("connresource: resource deleted but its harbrr key (%d) could not be revoked — revoke it manually: %w",
			keyID, err)
	}
	return nil
}
