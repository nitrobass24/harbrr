package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// AppMeta is the SQLite repository for the app_meta key/value table (the secrets
// canary + key id live here). Stateless; every method takes an Execer.
type AppMeta struct{}

// Get returns the value for a key and whether it exists.
func (AppMeta) Get(ctx context.Context, q dbinterface.Execer, key string) (string, bool, error) {
	var value string
	err := q.QueryRowContext(ctx, q.Rebind(`SELECT value FROM app_meta WHERE key = ?`), key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("database: get app_meta %q: %w", key, err)
	}
	return value, true, nil
}

// Set upserts a key/value pair.
func (AppMeta) Set(ctx context.Context, q dbinterface.Execer, key, value string) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO app_meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`), key, value)
	if err != nil {
		return fmt.Errorf("database: set app_meta %q: %w", key, err)
	}
	return nil
}
