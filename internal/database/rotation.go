package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// SecretRow is one encrypted setting row for key rotation: its id, its AAD inputs
// (instance id + setting name), and its current ciphertext. The plaintext is never
// held here — the rotation command decrypts and re-encrypts in memory.
type SecretRow struct {
	ID             int64
	InstanceID     int64
	Name           string
	ValueEncrypted string
}

// Rotation is the SQLite repository for key-rotation queries over secret settings.
// Stateless; every method takes an Execer and routes placeholders through q.Rebind.
type Rotation struct{}

// AllSecrets returns every secret setting row (is_secret=1) across all instances,
// ordered by id for determinism.
func (Rotation) AllSecrets(ctx context.Context, q dbinterface.Execer) ([]SecretRow, error) {
	rows, err := q.QueryContext(ctx,
		q.Rebind(`SELECT id, instance_id, name, value_encrypted
		 FROM indexer_settings WHERE is_secret = 1 ORDER BY id`))
	if err != nil {
		return nil, fmt.Errorf("database: query secret settings: %w", err)
	}
	defer rows.Close()

	var out []SecretRow
	for rows.Next() {
		var (
			r   SecretRow
			enc sql.NullString
		)
		if err := rows.Scan(&r.ID, &r.InstanceID, &r.Name, &enc); err != nil {
			return nil, fmt.Errorf("database: scan secret setting: %w", err)
		}
		r.ValueEncrypted = enc.String
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate secret settings: %w", err)
	}
	return out, nil
}

// UpdateSecret rewrites a secret row's ciphertext + key_id (key rotation). An empty
// valueEncrypted is stored as NULL (an empty secret), mirroring InsertSetting.
func (Rotation) UpdateSecret(ctx context.Context, q dbinterface.Execer, id int64, valueEncrypted, keyID string) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE indexer_settings SET value_encrypted = ?, key_id = ? WHERE id = ?`),
		nullIfEmpty(valueEncrypted), keyID, id)
	if err != nil {
		return fmt.Errorf("database: update secret %d: %w", id, err)
	}
	return nil
}
