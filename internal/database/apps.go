package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// Apps is the SQLite repository for first-class App identities (ADR 0004). Like the
// other resource repos it is stateless (every method takes an Execer) and stores the
// opaque (already-encrypted) credential; encryption is the apps service's concern.
type Apps struct{}

// appColumns is the full select list, in scan order.
const appColumns = `id, kind, name, base_url, username, api_key_encrypted, key_id, harbrr_url, enabled, created_at, updated_at`

// InsertApp writes a row with an empty api_key_encrypted/key_id (so its id can bind
// the credential's AAD) and returns the new id; the service seals the credential back
// via SetAppSecret in the same tx.
func (Apps) InsertApp(ctx context.Context, q dbinterface.Execer, a domain.App) (int64, error) {
	res, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO apps
			(kind, name, base_url, username, api_key_encrypted, key_id, harbrr_url, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, '', '', ?, ?, ?, ?)`),
		a.Kind, a.Name, a.BaseURL, a.Username, a.HarbrrURL, boolToInt(a.Enabled),
		a.CreatedAt.UTC().Format(timeLayout), a.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("database: insert app: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: app last insert id: %w", err)
	}
	return id, nil
}

// SetAppSecret writes the sealed credential column + key_id by id (phase two of the
// insert-then-seal write, so the credential binds to the freshly-minted row id).
func (Apps) SetAppSecret(ctx context.Context, q dbinterface.Execer, id int64, apiKeyEncrypted, keyID string) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE apps SET api_key_encrypted = ?, key_id = ? WHERE id = ?`),
		apiKeyEncrypted, keyID, id)
	if err != nil {
		return fmt.Errorf("database: set app secret: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// GetApp returns the app with the given id, or ErrNotFound.
func (Apps) GetApp(ctx context.Context, q dbinterface.Execer, id int64) (domain.App, error) {
	row := q.QueryRowContext(ctx, q.Rebind(`SELECT `+appColumns+` FROM apps WHERE id = ?`), id)
	a, err := scanApp(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.App{}, fmt.Errorf("app %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return domain.App{}, fmt.Errorf("database: scan app %d: %w", id, err)
	}
	return a, nil
}

// GetAppByIdentity returns the app matching (kind, base_url), or ErrNotFound — the
// get-or-create lookup the apps service resolves against.
func (Apps) GetAppByIdentity(ctx context.Context, q dbinterface.Execer, kind, baseURL string) (domain.App, error) {
	row := q.QueryRowContext(ctx, q.Rebind(`SELECT `+appColumns+` FROM apps WHERE kind = ? AND base_url = ?`), kind, baseURL)
	a, err := scanApp(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.App{}, fmt.Errorf("app %s at %s: %w", kind, baseURL, ErrNotFound)
	}
	if err != nil {
		return domain.App{}, fmt.Errorf("database: scan app by identity: %w", err)
	}
	return a, nil
}

// ListApps returns all apps ordered by id.
func (Apps) ListApps(ctx context.Context, q dbinterface.Execer) ([]domain.App, error) {
	rows, err := q.QueryContext(ctx, q.Rebind(`SELECT `+appColumns+` FROM apps ORDER BY id`))
	if err != nil {
		return nil, fmt.Errorf("database: list apps: %w", err)
	}
	defer rows.Close()

	var out []domain.App
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, fmt.Errorf("database: scan app row: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate apps: %w", err)
	}
	return out, nil
}

// UpdateApp writes an app's mutable fields (name, base_url, username, harbrr_url,
// enabled, the re-encrypted credential, key_id) by id. Kind is immutable and excluded
// from the SET list. Returns ErrNotFound when no row matches.
func (Apps) UpdateApp(ctx context.Context, q dbinterface.Execer, a domain.App) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE apps SET name = ?, base_url = ?, username = ?, harbrr_url = ?,
			enabled = ?, api_key_encrypted = ?, key_id = ?, updated_at = ?
			WHERE id = ?`),
		a.Name, a.BaseURL, a.Username, a.HarbrrURL, boolToInt(a.Enabled),
		a.APIKeyEncrypted, a.KeyID, a.UpdatedAt.UTC().Format(timeLayout), a.ID)
	if err != nil {
		return fmt.Errorf("database: update app: %w", err)
	}
	return affectedOrNotFoundID(res, a.ID)
}

// PropagateAppBaseURL rewrites the interim base_url/host copies every referencing
// surface row carries for its UNIQUE(kind, base_url) index, so an app base_url change
// keeps those indexes truthful until the cleanup migration (#269) drops the copies.
// Must run in the same tx as the app's UpdateApp.
func (Apps) PropagateAppBaseURL(ctx context.Context, q dbinterface.Execer, appID int64, baseURL string) error {
	for _, stmt := range []string{
		`UPDATE app_connections SET base_url = ? WHERE app_id = ?`,
		`UPDATE announce_connections SET base_url = ? WHERE app_id = ?`,
		`UPDATE download_clients SET host = ? WHERE app_id = ?`,
	} {
		if _, err := q.ExecContext(ctx, q.Rebind(stmt), baseURL, appID); err != nil {
			return fmt.Errorf("database: propagate app base_url: %w", err)
		}
	}
	return nil
}

// DeleteApp removes an app by id, returning ErrNotFound when absent. The caller
// (apps.Service) checks references first and returns a 409 rather than relying on an
// FK error, so the referencing surfaces can be named.
func (Apps) DeleteApp(ctx context.Context, q dbinterface.Execer, id int64) error {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM apps WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("database: delete app: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// AppReferences counts how many rows of each surface table reference an app — the
// input to the delete-blocked 409 that names them.
type AppReferences struct {
	AppConnections int
	Announce       int
	Download       int
}

// Any reports whether the app is referenced by any surface.
func (r AppReferences) Any() bool {
	return r.AppConnections+r.Announce+r.Download > 0
}

// CountAppReferences counts the surface rows pointing at an app.
func (Apps) CountAppReferences(ctx context.Context, q dbinterface.Execer, id int64) (AppReferences, error) {
	var r AppReferences
	for _, c := range []struct {
		table string
		dst   *int
	}{
		{"app_connections", &r.AppConnections},
		{"announce_connections", &r.Announce},
		{"download_clients", &r.Download},
	} {
		//nolint:gosec // table is a fixed literal from the loop above, never user input.
		if err := q.QueryRowContext(ctx, q.Rebind(`SELECT count(*) FROM `+c.table+` WHERE app_id = ?`), id).Scan(c.dst); err != nil {
			return AppReferences{}, fmt.Errorf("database: count %s refs: %w", c.table, err)
		}
	}
	return r, nil
}

// scanApp reads one apps row from a *sql.Row or *sql.Rows.
func scanApp(s interface{ Scan(...any) error }) (domain.App, error) {
	var (
		a                    domain.App
		enabled              int
		createdAt, updatedAt string
	)
	if err := s.Scan(&a.ID, &a.Kind, &a.Name, &a.BaseURL, &a.Username,
		&a.APIKeyEncrypted, &a.KeyID, &a.HarbrrURL, &enabled, &createdAt, &updatedAt); err != nil {
		return domain.App{}, err //nolint:wrapcheck // sql.ErrNoRows matched by caller; others wrapped there.
	}
	a.Enabled = enabled != 0
	a.CreatedAt, a.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return a, nil
}
