package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// AppSettings is the SQLite repository for the app_settings key/value table —
// runtime-tunable operational config (the first consumer is the search-cache
// config). Stateless: every method takes an Execer (so it runs standalone or in a
// tx) and routes every placeholder through q.Rebind. Mirrors AppMeta/Health.
//
// A row here OVERRIDES the config-file default at runtime; absence means "use the
// file/default". No secret is ever stored here.
type AppSettings struct{}

// Get returns the value for key and whether it is present. A missing key is not an
// error (found=false), so callers fall back to their config-file/default value.
func (AppSettings) Get(ctx context.Context, q dbinterface.Execer, key string) (value string, found bool, err error) {
	row := q.QueryRowContext(ctx, q.Rebind(`SELECT value FROM app_settings WHERE key = ?`), key)
	switch err := row.Scan(&value); {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("database: get app setting %q: %w", key, err)
	default:
		return value, true, nil
	}
}

// GetAll returns every setting as a key→value map (empty when none are set).
func (AppSettings) GetAll(ctx context.Context, q dbinterface.Execer) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, q.Rebind(`SELECT key, value FROM app_settings`))
	if err != nil {
		return nil, fmt.Errorf("database: list app settings: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("database: scan app setting: %w", err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate app settings: %w", err)
	}
	return out, nil
}

// Set upserts key→value, stamping updated_at. The caller supplies the time (stores
// stay clock-free for testability).
func (AppSettings) Set(ctx context.Context, q dbinterface.Execer, key, value string, updatedAt time.Time) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`),
		key, value, updatedAt.UTC().Format(timeLayout))
	if err != nil {
		return fmt.Errorf("database: set app setting %q: %w", key, err)
	}
	return nil
}
