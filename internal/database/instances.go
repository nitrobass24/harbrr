package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// Instances is the SQLite repository for configured indexer instances and their
// settings. It is stateless: every method takes an Execer, so the registry can
// call it standalone (passing *DB) or inside a transaction (passing the
// TxQuerier) — the latter is how an instance and its settings are written
// atomically. The repository stores opaque setting values; encryption is the
// registry's concern.
type Instances struct{}

// timeLayout is the RFC3339 form all timestamps are stored in.
const timeLayout = time.RFC3339

// Insert writes an instance row and returns its new id. CreatedAt/UpdatedAt and
// Enabled come from the caller.
func (Instances) Insert(ctx context.Context, q dbinterface.Execer, inst domain.IndexerInstance) (int64, error) {
	res, err := q.ExecContext(
		ctx,
		q.Rebind(`INSERT INTO indexer_instances (slug, definition_id, name, base_url, enabled, protocol, proxy_id, solver_id, priority, min_seeders, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		inst.Slug, inst.DefinitionID, inst.Name, inst.BaseURL, boolToInt(inst.Enabled), inst.Protocol,
		nullInt64(inst.ProxyID), nullInt64(inst.SolverID), inst.Priority, inst.MinSeeders,
		inst.CreatedAt.UTC().Format(timeLayout), inst.UpdatedAt.UTC().Format(timeLayout),
	)
	if err != nil {
		return 0, fmt.Errorf("database: insert instance: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: instance last insert id: %w", err)
	}
	return id, nil
}

// InsertSetting writes one setting row for an instance.
func (Instances) InsertSetting(ctx context.Context, q dbinterface.Execer, instanceID int64, s domain.IndexerSetting) error {
	if err := validateSettingInvariant(s); err != nil {
		return err
	}
	_, err := q.ExecContext(
		ctx,
		q.Rebind(`INSERT INTO indexer_settings (instance_id, name, value, value_encrypted, key_id, is_secret)
		 VALUES (?, ?, ?, ?, ?, ?)`),
		instanceID, s.Name,
		nullIfEmpty(s.Value), nullIfEmpty(s.ValueEncrypted), nullIfEmpty(s.KeyID), boolToInt(s.IsSecret),
	)
	if err != nil {
		return fmt.Errorf("database: insert setting %q: %w", s.Name, err)
	}
	return nil
}

// UpsertSetting inserts a setting or, when one already exists for (instance_id, name),
// updates it in place — used to persist a rotated credential (e.g. MyAnonamouse's
// mam_id) without touching the instance's other settings. It relies on the
// UNIQUE(instance_id, name) constraint.
func (Instances) UpsertSetting(ctx context.Context, q dbinterface.Execer, instanceID int64, s domain.IndexerSetting) error {
	if err := validateSettingInvariant(s); err != nil {
		return err
	}
	_, err := q.ExecContext(
		ctx,
		q.Rebind(`INSERT INTO indexer_settings (instance_id, name, value, value_encrypted, key_id, is_secret)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(instance_id, name) DO UPDATE SET
		   value = excluded.value, value_encrypted = excluded.value_encrypted,
		   key_id = excluded.key_id, is_secret = excluded.is_secret`),
		instanceID, s.Name,
		nullIfEmpty(s.Value), nullIfEmpty(s.ValueEncrypted), nullIfEmpty(s.KeyID), boolToInt(s.IsSecret),
	)
	if err != nil {
		return fmt.Errorf("database: upsert setting %q: %w", s.Name, err)
	}
	return nil
}

// GetBySlug returns the instance with the given slug, or ErrNotFound.
func (Instances) GetBySlug(ctx context.Context, q dbinterface.Execer, slug string) (domain.IndexerInstance, error) {
	row := q.QueryRowContext(ctx,
		q.Rebind(`SELECT id, slug, definition_id, name, base_url, enabled, protocol, proxy_id, solver_id, priority, min_seeders, created_at, updated_at
		 FROM indexer_instances WHERE slug = ?`), slug)
	inst, err := scanInstance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.IndexerInstance{}, fmt.Errorf("instance %q: %w", slug, ErrNotFound)
	}
	if err != nil {
		return domain.IndexerInstance{}, err
	}
	return inst, nil
}

// GetByID returns the instance with the given id, or ErrNotFound. Used by the announce
// source to resolve a cache write-back's instance id back to its slug.
func (Instances) GetByID(ctx context.Context, q dbinterface.Execer, id int64) (domain.IndexerInstance, error) {
	row := q.QueryRowContext(ctx,
		q.Rebind(`SELECT id, slug, definition_id, name, base_url, enabled, protocol, proxy_id, solver_id, priority, min_seeders, created_at, updated_at
		 FROM indexer_instances WHERE id = ?`), id)
	inst, err := scanInstance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.IndexerInstance{}, fmt.Errorf("instance %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return domain.IndexerInstance{}, err
	}
	return inst, nil
}

// Settings returns all settings for an instance, ordered by name for determinism.
func (Instances) Settings(ctx context.Context, q dbinterface.Execer, instanceID int64) ([]domain.IndexerSetting, error) {
	rows, err := q.QueryContext(ctx,
		q.Rebind(`SELECT name, value, value_encrypted, key_id, is_secret
		 FROM indexer_settings WHERE instance_id = ? ORDER BY name`), instanceID)
	if err != nil {
		return nil, fmt.Errorf("database: query settings: %w", err)
	}
	defer rows.Close()

	var out []domain.IndexerSetting
	for rows.Next() {
		var (
			s                       domain.IndexerSetting
			value, encrypted, keyID sql.NullString
			isSecret                int
		)
		if err := rows.Scan(&s.Name, &value, &encrypted, &keyID, &isSecret); err != nil {
			return nil, fmt.Errorf("database: scan setting: %w", err)
		}
		s.Value, s.ValueEncrypted, s.KeyID = value.String, encrypted.String, keyID.String
		s.IsSecret = isSecret != 0
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate settings: %w", err)
	}
	return out, nil
}

// List returns all instances ordered by slug.
func (Instances) List(ctx context.Context, q dbinterface.Execer) ([]domain.IndexerInstance, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, slug, definition_id, name, base_url, enabled, protocol, proxy_id, solver_id, priority, min_seeders, created_at, updated_at
		 FROM indexer_instances ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("database: list instances: %w", err)
	}
	defer rows.Close()

	var out []domain.IndexerInstance
	for rows.Next() {
		inst, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate instances: %w", err)
	}
	return out, nil
}

// UpdateMeta updates an instance's name, base URL, priority, min seeders, and
// updated_at by id.
func (Instances) UpdateMeta(ctx context.Context, q dbinterface.Execer, id int64, name, baseURL string, priority, minSeeders int, updatedAt time.Time) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE indexer_instances SET name = ?, base_url = ?, priority = ?, min_seeders = ?, updated_at = ? WHERE id = ?`),
		name, baseURL, priority, minSeeders, updatedAt.UTC().Format(timeLayout), id)
	if err != nil {
		return fmt.Errorf("database: update instance meta: %w", err)
	}
	return nil
}

// DeleteSettings removes all settings for an instance (used by the replace-on-
// update path).
func (Instances) DeleteSettings(ctx context.Context, q dbinterface.Execer, instanceID int64) error {
	if _, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM indexer_settings WHERE instance_id = ?`), instanceID); err != nil {
		return fmt.Errorf("database: delete settings: %w", err)
	}
	return nil
}

// DeleteSetting removes one setting by (instance_id, name). Used by the one-time
// resource migration to strip inline proxy/solver settings after they are folded
// into a global resource.
func (Instances) DeleteSetting(ctx context.Context, q dbinterface.Execer, instanceID int64, name string) error {
	if _, err := q.ExecContext(ctx,
		q.Rebind(`DELETE FROM indexer_settings WHERE instance_id = ? AND name = ?`), instanceID, name); err != nil {
		return fmt.Errorf("database: delete setting %q: %w", name, err)
	}
	return nil
}

// SetEnabled toggles an instance's enabled flag by slug, returning ErrNotFound
// when no row matches.
func (Instances) SetEnabled(ctx context.Context, q dbinterface.Execer, slug string, enabled bool, updatedAt time.Time) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE indexer_instances SET enabled = ?, updated_at = ? WHERE slug = ?`),
		boolToInt(enabled), updatedAt.UTC().Format(timeLayout), slug)
	if err != nil {
		return fmt.Errorf("database: set enabled: %w", err)
	}
	return affectedOrNotFound(res, slug)
}

// Delete removes an instance (its settings cascade) by slug, returning
// ErrNotFound when no row matches.
func (Instances) Delete(ctx context.Context, q dbinterface.Execer, slug string) error {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM indexer_instances WHERE slug = ?`), slug)
	if err != nil {
		return fmt.Errorf("database: delete instance: %w", err)
	}
	return affectedOrNotFound(res, slug)
}

// scanInstance reads one instance row from a *sql.Row or *sql.Rows.
func scanInstance(s interface{ Scan(...any) error }) (domain.IndexerInstance, error) {
	var (
		inst                 domain.IndexerInstance
		baseURL              sql.NullString
		enabled              int
		proxyID, solverID    sql.NullInt64
		createdAt, updatedAt string
	)
	if err := s.Scan(&inst.ID, &inst.Slug, &inst.DefinitionID, &inst.Name, &baseURL, &enabled, &inst.Protocol,
		&proxyID, &solverID, &inst.Priority, &inst.MinSeeders, &createdAt, &updatedAt); err != nil {
		return domain.IndexerInstance{}, err //nolint:wrapcheck // sql.ErrNoRows is matched by the caller; other errors wrap there.
	}
	inst.BaseURL = baseURL.String
	inst.Enabled = enabled != 0
	inst.ProxyID = nullableToPtr(proxyID)
	inst.SolverID = nullableToPtr(solverID)
	inst.CreatedAt = parseTime(createdAt)
	inst.UpdatedAt = parseTime(updatedAt)
	return inst, nil
}

// SetRefs updates an instance's proxy_id / solver_id references (nil clears the
// reference) and updated_at by id. Kept separate from UpdateMeta so the resource
// wiring is an explicit, independently-testable write.
func (Instances) SetRefs(ctx context.Context, q dbinterface.Execer, id int64, proxyID, solverID *int64, updatedAt time.Time) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE indexer_instances SET proxy_id = ?, solver_id = ?, updated_at = ? WHERE id = ?`),
		nullInt64(proxyID), nullInt64(solverID), updatedAt.UTC().Format(timeLayout), id)
	if err != nil {
		return fmt.Errorf("database: set instance refs: %w", err)
	}
	return nil
}

// nullInt64 binds an optional int64 as SQL NULL when nil.
func nullInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// nullableToPtr converts a scanned sql.NullInt64 to *int64 (nil when absent).
func nullableToPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// affectedOrNotFound maps a zero-rows-affected result to ErrNotFound.
func affectedOrNotFound(res sql.Result, slug string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("database: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("instance %q: %w", slug, ErrNotFound)
	}
	return nil
}

// parseTime parses a stored RFC3339 timestamp, returning the zero time on a
// malformed value (timestamps are written by us, so this is defensive).
func parseTime(s string) time.Time {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// boolToInt maps a bool to SQLite's 0/1 integer.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// validateSettingInvariant enforces the storage contract at the DB boundary so a
// caller regression can never persist a credential in cleartext: a secret never
// stores a plaintext value column and always carries the key_id it was encrypted
// under; a non-secret never carries ciphertext or a key_id. (A secret's
// value_encrypted may be empty when the secret's plaintext value is empty.)
func validateSettingInvariant(s domain.IndexerSetting) error {
	if s.IsSecret {
		if s.Value != "" {
			return fmt.Errorf("database: secret setting %q must not store a plaintext value", s.Name)
		}
		if s.KeyID == "" {
			return fmt.Errorf("database: secret setting %q is missing its key_id", s.Name)
		}
		return nil
	}
	if s.ValueEncrypted != "" || s.KeyID != "" {
		return fmt.Errorf("database: non-secret setting %q must not carry ciphertext or a key_id", s.Name)
	}
	return nil
}

// nullIfEmpty maps "" to a NULL column value so empty stays distinct from set.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
