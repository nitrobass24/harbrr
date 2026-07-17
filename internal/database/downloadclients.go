package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// DownloadClients is the SQLite repository for configured download clients. Like
// the other resource repos it is stateless (every method takes an Execer) and
// stores the opaque (already-encrypted) secret; encryption is the service's
// concern.
type DownloadClients struct{}

// downloadClientColumns is the full select list, in scan order.
const downloadClientColumns = `id, name, kind, enabled, host, username, secret_encrypted, key_id, settings_json, created_at, updated_at`

// InsertDownloadClient writes a row with an empty secret_encrypted/key_id (so its
// id can bind the encryption AAD) and returns the new id; the service writes the
// sealed secret back via SetDownloadClientSecret in the same tx.
func (DownloadClients) InsertDownloadClient(ctx context.Context, q dbinterface.Execer, c domain.DownloadClient) (int64, error) {
	settingsJSON, err := marshalDownloadClientSettings(c.Settings)
	if err != nil {
		return 0, err
	}
	res, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO download_clients
			(name, kind, enabled, host, username, secret_encrypted, key_id, settings_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, '', '', ?, ?, ?)`),
		c.Name, c.Kind, boolToInt(c.Enabled), c.Host, c.Username, settingsJSON,
		c.CreatedAt.UTC().Format(timeLayout), c.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("database: insert download client: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: download client last insert id: %w", err)
	}
	return id, nil
}

// SetDownloadClientSecret writes the encrypted secret column + key_id by id
// (phase two of the insert-then-seal write, so the credential binds to the
// freshly-minted row id).
func (DownloadClients) SetDownloadClientSecret(ctx context.Context, q dbinterface.Execer, id int64, secretEncrypted, keyID string) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE download_clients SET secret_encrypted = ?, key_id = ? WHERE id = ?`),
		secretEncrypted, keyID, id)
	if err != nil {
		return fmt.Errorf("database: set download client secret: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// GetDownloadClient returns the client with the given id, or ErrNotFound.
func (DownloadClients) GetDownloadClient(ctx context.Context, q dbinterface.Execer, id int64) (domain.DownloadClient, error) {
	row := q.QueryRowContext(ctx, q.Rebind(`SELECT `+downloadClientColumns+` FROM download_clients WHERE id = ?`), id)
	c, err := scanDownloadClient(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DownloadClient{}, fmt.Errorf("download client %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return domain.DownloadClient{}, fmt.Errorf("database: scan download client %d: %w", id, err)
	}
	return c, nil
}

// ListDownloadClients returns all clients ordered by id.
func (DownloadClients) ListDownloadClients(ctx context.Context, q dbinterface.Execer) ([]domain.DownloadClient, error) {
	rows, err := q.QueryContext(ctx, q.Rebind(`SELECT `+downloadClientColumns+` FROM download_clients ORDER BY id`))
	if err != nil {
		return nil, fmt.Errorf("database: list download clients: %w", err)
	}
	defer rows.Close()

	var out []domain.DownloadClient
	for rows.Next() {
		c, err := scanDownloadClient(rows)
		if err != nil {
			return nil, fmt.Errorf("database: scan download client row: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate download clients: %w", err)
	}
	return out, nil
}

// UpdateDownloadClient writes a client's mutable fields (name, enabled, host,
// username, settings, the re-encrypted secret, key_id) by id. Kind is immutable
// and deliberately excluded from the SET list. Returns ErrNotFound when no row
// matches.
func (DownloadClients) UpdateDownloadClient(ctx context.Context, q dbinterface.Execer, c domain.DownloadClient) error {
	settingsJSON, err := marshalDownloadClientSettings(c.Settings)
	if err != nil {
		return err
	}
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE download_clients SET name = ?, enabled = ?, host = ?, username = ?,
			secret_encrypted = ?, key_id = ?, settings_json = ?, updated_at = ?
			WHERE id = ?`),
		c.Name, boolToInt(c.Enabled), c.Host, c.Username, c.SecretEncrypted, c.KeyID, settingsJSON,
		c.UpdatedAt.UTC().Format(timeLayout), c.ID)
	if err != nil {
		return fmt.Errorf("database: update download client: %w", err)
	}
	return affectedOrNotFoundID(res, c.ID)
}

// SetDownloadClientEnabled toggles the enabled flag.
func (DownloadClients) SetDownloadClientEnabled(ctx context.Context, q dbinterface.Execer, id int64, enabled bool, updatedAt time.Time) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE download_clients SET enabled = ?, updated_at = ? WHERE id = ?`),
		boolToInt(enabled), updatedAt.UTC().Format(timeLayout), id)
	if err != nil {
		return fmt.Errorf("database: set download client enabled: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// DeleteDownloadClient removes a client by id, returning ErrNotFound when absent.
func (DownloadClients) DeleteDownloadClient(ctx context.Context, q dbinterface.Execer, id int64) error {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM download_clients WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("database: delete download client: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// marshalDownloadClientSettings serializes the typed settings wrapper for storage.
func marshalDownloadClientSettings(s domain.DownloadClientSettings) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("database: marshal download client settings: %w", err)
	}
	return string(b), nil
}

// scanDownloadClient reads one download_clients row from a *sql.Row or *sql.Rows.
func scanDownloadClient(s interface{ Scan(...any) error }) (domain.DownloadClient, error) {
	var (
		c                    domain.DownloadClient
		enabled              int
		settingsJSON         string
		createdAt, updatedAt string
	)
	if err := s.Scan(&c.ID, &c.Name, &c.Kind, &enabled, &c.Host, &c.Username,
		&c.SecretEncrypted, &c.KeyID, &settingsJSON, &createdAt, &updatedAt); err != nil {
		return domain.DownloadClient{}, err //nolint:wrapcheck // sql.ErrNoRows matched by caller; others wrapped there.
	}
	c.Enabled = enabled != 0
	if err := json.Unmarshal([]byte(settingsJSON), &c.Settings); err != nil {
		return domain.DownloadClient{}, fmt.Errorf("database: unmarshal download client %d settings: %w", c.ID, err)
	}
	c.CreatedAt, c.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return c, nil
}
