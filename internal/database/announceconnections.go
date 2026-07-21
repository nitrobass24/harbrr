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

// AnnounceConnections is the SQLite repository for cross-seed announce targets. Like
// AppConnections it is stateless (every method takes an Execer) and stores opaque
// already-encrypted secret values; encryption is the service's concern.
type AnnounceConnections struct{}

// announceColumns is the full select list, in scan order. app_id is the App
// reference (ADR 0004) — the sole identity source; base_url/api_key_encrypted/
// harbrr_url were dropped by #269 once every row was guaranteed a non-NULL app_id.
const announceColumns = `id, name, kind, app_id,
	harbrr_api_key_id, harbrr_api_key_encrypted, key_id, enabled, created_at, updated_at`

// InsertAnnounceConnection writes a row and returns its new id.
func (AnnounceConnections) InsertAnnounceConnection(ctx context.Context, q dbinterface.Execer, c domain.AnnounceConnection) (int64, error) {
	res, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO announce_connections
			(name, kind, app_id, harbrr_api_key_id,
			 harbrr_api_key_encrypted, key_id, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		c.Name, c.Kind, nullInt64(c.AppID), nullIfZero(c.HarbrrAPIKeyID),
		c.HarbrrAPIKeyEncrypted, c.KeyID, boolToInt(c.Enabled),
		c.CreatedAt.UTC().Format(timeLayout), c.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("database: insert announce connection: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: announce connection last insert id: %w", err)
	}
	return id, nil
}

// SetAnnounceConnectionSecrets writes the minted harbrr key column + key_id by id
// (mirrors AppConnections.SetConnectionSecrets; the tool's own key lives on the App).
// Used by restore, which must insert the row first to mint the id its secret's AAD
// binds to, then seal it under that id.
func (AnnounceConnections) SetAnnounceConnectionSecrets(ctx context.Context, q dbinterface.Execer, id int64, harbrrKeyEncrypted, keyID string) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE announce_connections SET harbrr_api_key_encrypted = ?, key_id = ?
			WHERE id = ?`),
		harbrrKeyEncrypted, keyID, id)
	if err != nil {
		return fmt.Errorf("database: set announce connection secrets: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// GetAnnounceConnection returns the connection with the given id, or ErrNotFound.
func (AnnounceConnections) GetAnnounceConnection(ctx context.Context, q dbinterface.Execer, id int64) (domain.AnnounceConnection, error) {
	row := q.QueryRowContext(ctx,
		q.Rebind(`SELECT `+announceColumns+` FROM announce_connections WHERE id = ?`), id)
	c, err := scanAnnounceConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AnnounceConnection{}, fmt.Errorf("announce connection %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return domain.AnnounceConnection{}, err
	}
	return c, nil
}

// ListAnnounceConnections returns all connections ordered by id.
func (AnnounceConnections) ListAnnounceConnections(ctx context.Context, q dbinterface.Execer) ([]domain.AnnounceConnection, error) {
	rows, err := q.QueryContext(ctx, q.Rebind(`SELECT `+announceColumns+` FROM announce_connections ORDER BY id`))
	if err != nil {
		return nil, fmt.Errorf("database: list announce connections: %w", err)
	}
	defer rows.Close()

	var out []domain.AnnounceConnection
	for rows.Next() {
		c, err := scanAnnounceConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate announce connections: %w", err)
	}
	return out, nil
}

// UpdateAnnounceConnection writes the mutable fields of an existing connection back by id
// (kind is immutable — not included in the SET list). Mirrors AppConnections.UpdateConnection.
func (AnnounceConnections) UpdateAnnounceConnection(ctx context.Context, q dbinterface.Execer, c domain.AnnounceConnection) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE announce_connections SET
			name = ?, key_id = ?, updated_at = ?
			WHERE id = ?`),
		c.Name, c.KeyID, c.UpdatedAt.UTC().Format(timeLayout), c.ID)
	if err != nil {
		return fmt.Errorf("database: update announce connection: %w", err)
	}
	return affectedOrNotFoundID(res, c.ID)
}

// SetAnnounceConnectionEnabled toggles the enabled flag.
func (AnnounceConnections) SetAnnounceConnectionEnabled(ctx context.Context, q dbinterface.Execer, id int64, enabled bool, updatedAt time.Time) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE announce_connections SET enabled = ?, updated_at = ? WHERE id = ?`),
		boolToInt(enabled), updatedAt.UTC().Format(timeLayout), id)
	if err != nil {
		return fmt.Errorf("database: set announce connection enabled: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// DeleteAnnounceConnection removes a connection by id, returning ErrNotFound when absent.
func (AnnounceConnections) DeleteAnnounceConnection(ctx context.Context, q dbinterface.Execer, id int64) error {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM announce_connections WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("database: delete announce connection: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// scanAnnounceConnection reads one announce_connections row.
func scanAnnounceConnection(s interface{ Scan(...any) error }) (domain.AnnounceConnection, error) {
	var (
		c                    domain.AnnounceConnection
		appID, harbrrKeyID   sql.NullInt64
		enabled              int
		createdAt, updatedAt string
	)
	if err := s.Scan(&c.ID, &c.Name, &c.Kind, &appID,
		&harbrrKeyID, &c.HarbrrAPIKeyEncrypted, &c.KeyID, &enabled, &createdAt, &updatedAt); err != nil {
		return domain.AnnounceConnection{}, err //nolint:wrapcheck // sql.ErrNoRows matched by caller; others wrapped there.
	}
	c.AppID = nullableToPtr(appID)
	c.HarbrrAPIKeyID = harbrrKeyID.Int64
	c.Enabled = enabled != 0
	c.CreatedAt, c.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return c, nil
}
