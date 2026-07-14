package connresource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// testKey is a synthetic 32-byte encryption key that exists only to exercise the
// keyring in tests (never a real secret).
const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

const secretDiscriminator = "secret"

// resource is a minimal stand-in for the three real connection-resource entities
// (appsync/announce connections, notifications), just enough to exercise every
// step of the Lifecycle: an identity, one encrypted secret, an optional minted-key
// reference, a mutable flag (for the no-lost-update invariant), and a timestamp.
type resource struct {
	ID              int64
	Name            string
	Flag            bool
	SecretEncrypted string
	KeyID           string
	MintedKeyID     int64
	UpdatedAt       time.Time
}

// newTestDB opens an in-memory DB with the ad-hoc resources table this test suite
// exercises Lifecycle against — deliberately not one of harbrr's real tables, so
// this package's tests do not depend on any product schema.
func newTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.ExecContext(context.Background(), `CREATE TABLE resources (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		flag INTEGER NOT NULL DEFAULT 0,
		secret_encrypted TEXT NOT NULL DEFAULT '',
		key_id TEXT NOT NULL DEFAULT '',
		minted_key_id INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		t.Fatalf("create resources table: %v", err)
	}
	return db
}

func newTestKeyring(t *testing.T) *secrets.Keyring {
	t.Helper()
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open keyring: %v", err)
	}
	return kr
}

func insertResource(ctx context.Context, q dbinterface.Execer, r resource) (int64, error) {
	res, err := q.ExecContext(ctx, q.Rebind(`INSERT INTO resources
		(name, flag, secret_encrypted, key_id, minted_key_id, updated_at) VALUES (?, ?, ?, ?, ?, ?)`),
		r.Name, boolToInt(r.Flag), r.SecretEncrypted, r.KeyID, r.MintedKeyID, r.UpdatedAt.Format(time.RFC3339))
	if err != nil {
		return 0, err //nolint:wrapcheck // test helper; Lifecycle wraps.
	}
	return res.LastInsertId() //nolint:wrapcheck // test helper.
}

func getResource(ctx context.Context, q dbinterface.Execer, id int64) (resource, error) {
	row := q.QueryRowContext(ctx, q.Rebind(`SELECT id, name, flag, secret_encrypted, key_id, minted_key_id, updated_at
		FROM resources WHERE id = ?`), id)
	var (
		r         resource
		flag      int
		updatedAt string
	)
	if err := row.Scan(&r.ID, &r.Name, &flag, &r.SecretEncrypted, &r.KeyID, &r.MintedKeyID, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resource{}, fmt.Errorf("resource %d: %w", id, database.ErrNotFound)
		}
		return resource{}, err //nolint:wrapcheck // test helper.
	}
	r.Flag = flag != 0
	r.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return r, nil
}

func updateResource(ctx context.Context, q dbinterface.Execer, r resource) error {
	res, err := q.ExecContext(ctx, q.Rebind(`UPDATE resources SET
		flag = ?, secret_encrypted = ?, key_id = ?, updated_at = ? WHERE id = ?`),
		boolToInt(r.Flag), r.SecretEncrypted, r.KeyID, r.UpdatedAt.Format(time.RFC3339), r.ID)
	if err != nil {
		return err //nolint:wrapcheck // test helper.
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err //nolint:wrapcheck // test helper.
	}
	if n == 0 {
		return fmt.Errorf("resource %d: %w", r.ID, database.ErrNotFound)
	}
	return nil
}

func deleteResource(ctx context.Context, q dbinterface.Execer, id int64) error {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM resources WHERE id = ?`), id)
	if err != nil {
		return err //nolint:wrapcheck // test helper.
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err //nolint:wrapcheck // test helper.
	}
	if n == 0 {
		return fmt.Errorf("resource %d: %w", id, database.ErrNotFound)
	}
	return nil
}

func setResourceSecret(ctx context.Context, q dbinterface.Execer, id int64, encrypted []string, keyID string) error {
	_, err := q.ExecContext(ctx, q.Rebind(`UPDATE resources SET secret_encrypted = ?, key_id = ? WHERE id = ?`),
		encrypted[0], keyID, id)
	return err //nolint:wrapcheck // test helper.
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// fakeMinter is a KeyMinter whose Mint/Revoke can be forced to fail, and which
// tracks which minted ids are still outstanding (not yet revoked) so a test can
// assert a create/delete failure actually triggered (or skipped) a revoke.
type fakeMinter struct {
	mu         sync.Mutex
	next       int64
	live       map[int64]bool
	failMint   bool
	failRevoke bool
}

func newFakeMinter() *fakeMinter { return &fakeMinter{live: make(map[int64]bool)} }

func (m *fakeMinter) MintAPIKey(_ context.Context, name string) (string, domain.APIKey, error) {
	if m.failMint {
		return "", domain.APIKey{}, errors.New("fakeMinter: mint boom")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	id := m.next
	m.live[id] = true
	return fmt.Sprintf("plain-key-%d", id), domain.APIKey{ID: id, Name: name}, nil
}

func (m *fakeMinter) RevokeAPIKey(_ context.Context, id int64) error {
	if m.failRevoke {
		return errors.New("fakeMinter: revoke boom")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.live, id)
	return nil
}

func (m *fakeMinter) isLive(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.live[id]
}

// baseCreateSpec builds a CreateSpec for one resource named name, with no minter
// and a single secret sealed under the discriminator plaintext.
func baseCreateSpec(name, plaintext string) CreateSpec[resource] {
	return CreateSpec[resource]{
		Build: func(now time.Time, mintedKeyID int64) resource {
			return resource{Name: name, MintedKeyID: mintedKeyID, UpdatedAt: now}
		},
		Insert: insertResource,
		Secrets: func(_ resource, _ string) []Secret {
			return []Secret{{Discriminator: secretDiscriminator, Plaintext: plaintext}}
		},
		SetSecrets: setResourceSecret,
		Finalize: func(r resource, id int64, encrypted []string, keyID string) resource {
			r.ID, r.SecretEncrypted, r.KeyID = id, encrypted[0], keyID
			return r
		},
		Conflict: func(r resource) error {
			return fmt.Errorf("%w: resource %q", domain.ErrConflict, r.Name)
		},
	}
}

// (a) AAD binds to the inserted id: a secret sealed for one resource cannot be
// decrypted under a different id, and decrypts correctly under its own.
func TestCreateSealsSecretBoundToInsertedID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	kr := newTestKeyring(t)
	life := New[resource](db, kr, time.Now)

	got, err := life.Create(ctx, baseCreateSpec("alpha", "s3cr3t-plaintext"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID == 0 {
		t.Fatal("Create did not assign an id")
	}

	dec, err := kr.Decrypt(got.ID, secretDiscriminator, got.SecretEncrypted)
	if err != nil {
		t.Fatalf("decrypt under the resource's own id: %v", err)
	}
	if dec != "s3cr3t-plaintext" {
		t.Fatalf("decrypted = %q, want the original plaintext", dec)
	}

	if _, err := kr.Decrypt(got.ID+1, secretDiscriminator, got.SecretEncrypted); err == nil {
		t.Fatal("decrypt under a different id should fail (AAD not bound to that id), got nil error")
	}
}

// (b) a failed create (here: a unique-name conflict on the second insert) revokes
// the just-minted orphan key, and the returned error still wraps domain.ErrConflict.
func TestCreateFailureRevokesOrphanKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	kr := newTestKeyring(t)
	life := New[resource](db, kr, time.Now)
	minter := newFakeMinter()

	spec := baseCreateSpec("dup", "first")
	spec.Minter = minter
	spec.MintName = "test key"
	if _, err := life.Create(ctx, spec); err != nil {
		t.Fatalf("first create: %v", err)
	}

	dupSpec := baseCreateSpec("dup", "second") // same name -> unique violation
	dupSpec.Minter = minter
	dupSpec.MintName = "test key 2"
	_, err := life.Create(ctx, dupSpec)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second create err = %v, want domain.ErrConflict", err)
	}

	// The second mint (id 2) must have been revoked; the first (id 1) must not.
	if minter.isLive(2) {
		t.Error("orphan key from the failed create was not revoked")
	}
	if !minter.isLive(1) {
		t.Error("the first, successful create's key was revoked by mistake")
	}
}

// TestCreateRevokeFailureSurfaces: when the orphan revoke itself fails, that
// failure is returned alongside the original error rather than swallowed.
func TestCreateRevokeFailureSurfaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	kr := newTestKeyring(t)
	life := New[resource](db, kr, time.Now)
	minter := newFakeMinter()

	spec := baseCreateSpec("dup2", "first")
	spec.Minter = minter
	if _, err := life.Create(ctx, spec); err != nil {
		t.Fatalf("first create: %v", err)
	}

	minter.failRevoke = true
	dupSpec := baseCreateSpec("dup2", "second")
	dupSpec.Minter = minter
	_, err := life.Create(ctx, dupSpec)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("err = %v, want it to still wrap domain.ErrConflict", err)
	}
	if !strings.Contains(err.Error(), "could not be revoked") {
		t.Errorf("err = %v, want the revoke failure surfaced", err)
	}
}

// (c) a delete whose key revoke fails is fail-closed: the row is gone (delete
// succeeded) but the caller still gets an error, since a live orphan key remains.
func TestDeleteRevokeFailureFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	kr := newTestKeyring(t)
	life := New[resource](db, kr, time.Now)
	minter := newFakeMinter()

	spec := baseCreateSpec("to-delete", "s")
	spec.Minter = minter
	created, err := life.Create(ctx, spec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	minter.failRevoke = true
	delSpec := DeleteSpec[resource]{
		Get:         getResource,
		Delete:      deleteResource,
		Minter:      minter,
		MintedKeyID: func(r resource) int64 { return r.MintedKeyID },
		RevokeFailMsg: func(r resource, keyID int64, revokeErr error) error {
			return fmt.Errorf("resource %q deleted but its key (%d) could not be revoked: %w", r.Name, keyID, revokeErr)
		},
	}
	err = life.Delete(ctx, created.ID, delSpec)
	if err == nil || !strings.Contains(err.Error(), "could not be revoked") {
		t.Fatalf("Delete with failing revoke = %v, want a surfaced revoke failure", err)
	}

	// The row must still be gone despite the revoke failure (delete already committed).
	if _, err := getResource(ctx, db, created.ID); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("row should be deleted even though revoke failed: err = %v", err)
	}
}

// (d) two overlapping Updates (one flipping Flag, one rotating the secret) inside
// one transaction each cannot lose each other's write: the single-connection DB
// serializes them, so the second always reads the first's committed row.
func TestUpdateHasNoLostUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	kr := newTestKeyring(t)
	life := New[resource](db, kr, time.Now)

	created, err := life.Create(ctx, baseCreateSpec("concurrent", "orig"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	flagUpdate := func(want bool) UpdateSpec[resource] {
		return UpdateSpec[resource]{
			Get: getResource,
			Patch: func(r *resource) error {
				r.Flag = want
				return nil
			},
			Write: updateResource,
		}
	}
	secretUpdate := func(plaintext string) UpdateSpec[resource] {
		return UpdateSpec[resource]{
			Get: getResource,
			Rotate: func(_ *resource) (Secret, bool, error) {
				return Secret{Discriminator: secretDiscriminator, Plaintext: plaintext}, true, nil
			},
			Apply: func(r *resource, encrypted, keyID string) { r.SecretEncrypted, r.KeyID = encrypted, keyID },
			Write: updateResource,
		}
	}

	for i := range 20 {
		wantFlag := i%2 == 0
		wantSecret := fmt.Sprintf("rotated-%d", i)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := life.Update(ctx, created.ID, flagUpdate(wantFlag)); err != nil {
				t.Errorf("iter %d: flag update: %v", i, err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := life.Update(ctx, created.ID, secretUpdate(wantSecret)); err != nil {
				t.Errorf("iter %d: secret update: %v", i, err)
			}
		}()
		wg.Wait()

		got, err := getResource(ctx, db, created.ID)
		if err != nil {
			t.Fatalf("iter %d: get: %v", i, err)
		}
		if got.Flag != wantFlag {
			t.Fatalf("iter %d: flag = %v, want %v (write lost)", i, got.Flag, wantFlag)
		}
		dec, err := kr.Decrypt(got.ID, secretDiscriminator, got.SecretEncrypted)
		if err != nil {
			t.Fatalf("iter %d: decrypt: %v", i, err)
		}
		if dec != wantSecret {
			t.Fatalf("iter %d: secret = %q, want %q (rotation lost)", i, dec, wantSecret)
		}
	}
}

// (e) a unique-constraint violation on create maps to domain.ErrConflict via the
// spec's Conflict formatter, not a generic wrapped error.
func TestCreateUniqueViolationMapsToConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newTestDB(t)
	kr := newTestKeyring(t)
	life := New[resource](db, kr, time.Now)

	if _, err := life.Create(ctx, baseCreateSpec("taken", "a")); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := life.Create(ctx, baseCreateSpec("taken", "b"))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate create err = %v, want domain.ErrConflict", err)
	}
}
