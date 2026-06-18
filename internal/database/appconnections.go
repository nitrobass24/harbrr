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

// AppConnections is the SQLite repository for app-sync connections and their
// per-indexer reconciliation ledger. Stateless: every method takes an Execer, so
// the service can call it standalone (passing *DB) or inside a transaction. The
// repository stores opaque (already-encrypted) secret values; encryption is the
// service's concern, exactly as Instances treats indexer settings.
type AppConnections struct{}

// connectionColumns is the full select list, in scan order.
const connectionColumns = `id, name, kind, base_url, api_key_encrypted, harbrr_url,
	harbrr_api_key_id, harbrr_api_key_encrypted, key_id, enabled, sync_level,
	index_scope, priority, last_sync_at, last_sync_status, last_sync_error,
	created_at, updated_at`

// InsertConnection writes a connection row and returns its new id.
func (AppConnections) InsertConnection(ctx context.Context, q dbinterface.Execer, c domain.AppConnection) (int64, error) {
	res, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO app_connections
			(name, kind, base_url, api_key_encrypted, harbrr_url, harbrr_api_key_id,
			 harbrr_api_key_encrypted, key_id, enabled, sync_level, index_scope,
			 priority, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		c.Name, c.Kind, c.BaseURL, c.APIKeyEncrypted, c.HarbrrURL, nullIfZero(c.HarbrrAPIKeyID),
		c.HarbrrAPIKeyEncrypted, c.KeyID, boolToInt(c.Enabled), c.SyncLevel, c.IndexScope,
		c.Priority, c.CreatedAt.UTC().Format(timeLayout), c.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("database: insert app connection: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: app connection last insert id: %w", err)
	}
	return id, nil
}

// GetConnection returns the connection with the given id, or ErrNotFound.
func (AppConnections) GetConnection(ctx context.Context, q dbinterface.Execer, id int64) (domain.AppConnection, error) {
	row := q.QueryRowContext(ctx,
		q.Rebind(`SELECT `+connectionColumns+` FROM app_connections WHERE id = ?`), id)
	c, err := scanConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AppConnection{}, fmt.Errorf("app connection %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return domain.AppConnection{}, err
	}
	return c, nil
}

// ListConnections returns all connections ordered by id.
func (AppConnections) ListConnections(ctx context.Context, q dbinterface.Execer) ([]domain.AppConnection, error) {
	rows, err := q.QueryContext(ctx, q.Rebind(`SELECT `+connectionColumns+` FROM app_connections ORDER BY id`))
	if err != nil {
		return nil, fmt.Errorf("database: list app connections: %w", err)
	}
	defer rows.Close()

	var out []domain.AppConnection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate app connections: %w", err)
	}
	return out, nil
}

// UpdateConnection writes a connection's mutable fields (everything a PATCH can
// change plus the re-encrypted app key) by id, returning ErrNotFound when no row
// matches. The minted harbrr key and timestamps-of-record are not touched here.
func (AppConnections) UpdateConnection(ctx context.Context, q dbinterface.Execer, c domain.AppConnection) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE app_connections SET
			name = ?, base_url = ?, api_key_encrypted = ?, harbrr_url = ?, key_id = ?,
			sync_level = ?, index_scope = ?, priority = ?, updated_at = ?
			WHERE id = ?`),
		c.Name, c.BaseURL, c.APIKeyEncrypted, c.HarbrrURL, c.KeyID,
		c.SyncLevel, c.IndexScope, c.Priority, c.UpdatedAt.UTC().Format(timeLayout), c.ID)
	if err != nil {
		return fmt.Errorf("database: update app connection: %w", err)
	}
	return affectedOrNotFoundID(res, c.ID)
}

// SetConnectionSecrets writes the encrypted secret columns by id. Connections are
// inserted in two phases inside one transaction — the row first (to mint the id the
// encryption AAD binds to), then its secrets — so a credential is never bound to the
// wrong row.
func (AppConnections) SetConnectionSecrets(ctx context.Context, q dbinterface.Execer, id int64, apiKeyEncrypted, harbrrKeyEncrypted, keyID string) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE app_connections SET api_key_encrypted = ?, harbrr_api_key_encrypted = ?, key_id = ?
			WHERE id = ?`),
		apiKeyEncrypted, harbrrKeyEncrypted, keyID, id)
	if err != nil {
		return fmt.Errorf("database: set connection secrets: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// SetConnectionEnabled toggles a connection's enabled flag by id.
func (AppConnections) SetConnectionEnabled(ctx context.Context, q dbinterface.Execer, id int64, enabled bool, updatedAt time.Time) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE app_connections SET enabled = ?, updated_at = ? WHERE id = ?`),
		boolToInt(enabled), updatedAt.UTC().Format(timeLayout), id)
	if err != nil {
		return fmt.Errorf("database: set app connection enabled: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// RecordSyncResult stores the outcome of a sync run on the connection.
func (AppConnections) RecordSyncResult(ctx context.Context, q dbinterface.Execer, id int64, status, detail string, at time.Time) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE app_connections SET last_sync_at = ?, last_sync_status = ?, last_sync_error = ?
			WHERE id = ?`),
		at.UTC().Format(timeLayout), status, nullIfEmpty(detail), id)
	if err != nil {
		return fmt.Errorf("database: record sync result: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// DeleteConnection removes a connection (its ledger rows cascade) by id.
func (AppConnections) DeleteConnection(ctx context.Context, q dbinterface.Execer, id int64) error {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM app_connections WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("database: delete app connection: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// UpsertConnectionIndexer inserts or updates one ledger row, keyed on
// (connection_id, instance_id) — the reconcile path calls it after each push. The
// DO UPDATE deliberately does NOT touch `selected`: that column is user intent owned
// by SetIndexerSelection, so a re-sync never re-selects a deselected indexer. (On a
// fresh INSERT the provided selected value applies; it is ignored under scope "all".)
func (AppConnections) UpsertConnectionIndexer(ctx context.Context, q dbinterface.Execer, l domain.AppConnectionIndexer) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO app_connection_indexers
			(connection_id, instance_id, remote_id, selected, payload_hash,
			 last_pushed_at, last_push_status, last_push_error)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(connection_id, instance_id) DO UPDATE SET
			  remote_id = excluded.remote_id,
			  payload_hash = excluded.payload_hash, last_pushed_at = excluded.last_pushed_at,
			  last_push_status = excluded.last_push_status, last_push_error = excluded.last_push_error`),
		l.ConnectionID, l.InstanceID, nullIfEmpty(l.RemoteID), boolToInt(l.Selected),
		nullIfEmpty(l.PayloadHash), nullTime(l.LastPushedAt), nullIfEmpty(l.LastPushStatus),
		nullIfEmpty(l.LastPushError))
	if err != nil {
		return fmt.Errorf("database: upsert connection indexer: %w", err)
	}
	return nil
}

// SetIndexerSelection sets a ledger row's selected flag, creating a placeholder row
// (no remote id) when none exists yet. This is the only writer of `selected` — the
// reconcile upsert leaves it alone — so it owns the scope="selected" set.
func (AppConnections) SetIndexerSelection(ctx context.Context, q dbinterface.Execer, connectionID, instanceID int64, selected bool) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO app_connection_indexers (connection_id, instance_id, selected)
			VALUES (?, ?, ?)
			ON CONFLICT(connection_id, instance_id) DO UPDATE SET selected = excluded.selected`),
		connectionID, instanceID, boolToInt(selected))
	if err != nil {
		return fmt.Errorf("database: set indexer selection: %w", err)
	}
	return nil
}

// ListConnectionIndexers returns a connection's ledger rows ordered by instance id.
func (AppConnections) ListConnectionIndexers(ctx context.Context, q dbinterface.Execer, connectionID int64) ([]domain.AppConnectionIndexer, error) {
	rows, err := q.QueryContext(ctx,
		q.Rebind(`SELECT id, connection_id, instance_id, remote_id, selected, payload_hash,
			last_pushed_at, last_push_status, last_push_error
			FROM app_connection_indexers WHERE connection_id = ? ORDER BY instance_id`), connectionID)
	if err != nil {
		return nil, fmt.Errorf("database: list connection indexers: %w", err)
	}
	defer rows.Close()

	var out []domain.AppConnectionIndexer
	for rows.Next() {
		l, err := scanConnectionIndexer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate connection indexers: %w", err)
	}
	return out, nil
}

// DeleteConnectionIndexer removes one ledger row (used after an orphan removal).
func (AppConnections) DeleteConnectionIndexer(ctx context.Context, q dbinterface.Execer, connectionID, instanceID int64) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`DELETE FROM app_connection_indexers WHERE connection_id = ? AND instance_id = ?`),
		connectionID, instanceID)
	if err != nil {
		return fmt.Errorf("database: delete connection indexer: %w", err)
	}
	return nil
}

// scanConnection reads one app_connections row from a *sql.Row or *sql.Rows.
func scanConnection(s interface{ Scan(...any) error }) (domain.AppConnection, error) {
	var (
		c                                      domain.AppConnection
		harbrrKeyID                            sql.NullInt64
		enabled                                int
		lastSyncAt, lastSyncStatus, lastSyncEr sql.NullString
		createdAt, updatedAt                   string
	)
	if err := s.Scan(&c.ID, &c.Name, &c.Kind, &c.BaseURL, &c.APIKeyEncrypted, &c.HarbrrURL,
		&harbrrKeyID, &c.HarbrrAPIKeyEncrypted, &c.KeyID, &enabled, &c.SyncLevel,
		&c.IndexScope, &c.Priority, &lastSyncAt, &lastSyncStatus, &lastSyncEr,
		&createdAt, &updatedAt); err != nil {
		return domain.AppConnection{}, err //nolint:wrapcheck // sql.ErrNoRows matched by caller; others wrapped there.
	}
	c.HarbrrAPIKeyID = harbrrKeyID.Int64
	c.Enabled = enabled != 0
	c.LastSyncAt = timePtr(lastSyncAt)
	c.LastSyncStatus, c.LastSyncError = lastSyncStatus.String, lastSyncEr.String
	c.CreatedAt, c.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return c, nil
}

// scanConnectionIndexer reads one app_connection_indexers row.
func scanConnectionIndexer(s interface{ Scan(...any) error }) (domain.AppConnectionIndexer, error) {
	var (
		l                          domain.AppConnectionIndexer
		remoteID, hash, status, er sql.NullString
		lastPushedAt               sql.NullString
		selected                   int
	)
	if err := s.Scan(&l.ID, &l.ConnectionID, &l.InstanceID, &remoteID, &selected, &hash,
		&lastPushedAt, &status, &er); err != nil {
		return domain.AppConnectionIndexer{}, err //nolint:wrapcheck // wrapped by the caller.
	}
	l.RemoteID, l.PayloadHash = remoteID.String, hash.String
	l.Selected = selected != 0
	l.LastPushedAt = timePtr(lastPushedAt)
	l.LastPushStatus, l.LastPushError = status.String, er.String
	return l, nil
}

// affectedOrNotFoundID maps a zero-rows-affected result to ErrNotFound, keyed by id.
func affectedOrNotFoundID(res sql.Result, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("database: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("app connection %d: %w", id, ErrNotFound)
	}
	return nil
}

// nullIfZero maps a zero id to a NULL column value (the harbrr key was revoked).
func nullIfZero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// nullTime maps a nil time to NULL, else the RFC3339 UTC string.
func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(timeLayout)
}

// timePtr maps a nullable stored timestamp to a *time.Time.
func timePtr(ns sql.NullString) *time.Time {
	if !ns.Valid {
		return nil
	}
	t := parseTime(ns.String)
	return &t
}
