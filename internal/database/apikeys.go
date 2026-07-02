package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// APIKeys is the SQLite repository for management/Torznab API keys. Stateless;
// every method takes an Execer. Only key hashes are stored.
type APIKeys struct{}

// Create inserts an API key (hash only) and returns its id.
func (APIKeys) Create(ctx context.Context, q dbinterface.Execer, k domain.APIKey) (int64, error) {
	res, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO api_keys (name, key_hash, created_at) VALUES (?, ?, ?)`),
		k.Name, k.KeyHash, k.CreatedAt.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("database: insert api key: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: api key last insert id: %w", err)
	}
	return id, nil
}

// GetByHash returns the API key matching a stored hash, or ErrNotFound. Used to
// validate a presented key (the caller hashes the plaintext first).
func (APIKeys) GetByHash(ctx context.Context, q dbinterface.Execer, hash string) (domain.APIKey, error) {
	k, err := scanAPIKey(q.QueryRowContext(ctx,
		q.Rebind(`SELECT id, name, key_hash, created_at, last_used_at FROM api_keys WHERE key_hash = ?`), hash))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.APIKey{}, fmt.Errorf("api key: %w", ErrNotFound)
	}
	if err != nil {
		return domain.APIKey{}, err
	}
	return k, nil
}

// List returns all API keys (hashes, never plaintext), newest first.
func (APIKeys) List(ctx context.Context, q dbinterface.Execer) ([]domain.APIKey, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, name, key_hash, created_at, last_used_at FROM api_keys ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("database: list api keys: %w", err)
	}
	defer rows.Close()

	var out []domain.APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate api keys: %w", err)
	}
	return out, nil
}

// Delete removes an API key by id, returning ErrNotFound when no row matches.
func (APIKeys) Delete(ctx context.Context, q dbinterface.Execer, id int64) error {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM api_keys WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("database: delete api key: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("database: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("api key %d: %w", id, ErrNotFound)
	}
	return nil
}

// last_used_at is populated by a later phase (a debounced touch), not on every
// validation — validating an API key must not write on the request path.

// scanAPIKey reads one api_keys row.
func scanAPIKey(s interface{ Scan(...any) error }) (domain.APIKey, error) {
	var (
		k          domain.APIKey
		createdAt  string
		lastUsedAt sql.NullString
	)
	if err := s.Scan(&k.ID, &k.Name, &k.KeyHash, &createdAt, &lastUsedAt); err != nil {
		return domain.APIKey{}, err //nolint:wrapcheck // sql.ErrNoRows matched by caller; others wrapped there.
	}
	k.CreatedAt = parseTime(createdAt)
	if lastUsedAt.Valid {
		t := parseTime(lastUsedAt.String)
		k.LastUsedAt = &t
	}
	return k, nil
}
