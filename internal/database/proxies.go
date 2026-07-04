package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// Proxies is the SQLite repository for global proxy resources. Like the other
// resource repos it is stateless (every method takes an Execer, so it runs
// standalone or inside a transaction) and stores the opaque (already-encrypted)
// URL; encryption is the service's concern.
type Proxies struct{}

// proxyColumns is the full select list, in scan order.
const proxyColumns = `id, name, type, url_encrypted, key_id, created_at, updated_at`

// InsertProxy writes a proxy row and returns its new id. The row is inserted with
// an empty url_encrypted so its id can bind the encryption AAD; the service writes
// the sealed URL back via SetProxySecret in the same tx.
func (Proxies) InsertProxy(ctx context.Context, q dbinterface.Execer, p domain.Proxy) (int64, error) {
	res, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO proxies (name, type, url_encrypted, key_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`),
		p.Name, p.Type, p.URLEncrypted, p.KeyID,
		p.CreatedAt.UTC().Format(timeLayout), p.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("database: insert proxy: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: proxy last insert id: %w", err)
	}
	return id, nil
}

// GetProxy returns the proxy with the given id, or ErrNotFound.
func (Proxies) GetProxy(ctx context.Context, q dbinterface.Execer, id int64) (domain.Proxy, error) {
	row := q.QueryRowContext(ctx, q.Rebind(`SELECT `+proxyColumns+` FROM proxies WHERE id = ?`), id)
	p, err := scanProxy(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Proxy{}, fmt.Errorf("proxy %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return domain.Proxy{}, err
	}
	return p, nil
}

// ListProxies returns all proxies ordered by id.
func (Proxies) ListProxies(ctx context.Context, q dbinterface.Execer) ([]domain.Proxy, error) {
	rows, err := q.QueryContext(ctx, q.Rebind(`SELECT `+proxyColumns+` FROM proxies ORDER BY id`))
	if err != nil {
		return nil, fmt.Errorf("database: list proxies: %w", err)
	}
	defer rows.Close()

	var out []domain.Proxy
	for rows.Next() {
		p, err := scanProxy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate proxies: %w", err)
	}
	return out, nil
}

// UpdateProxy writes a proxy's mutable fields (name, type, the re-encrypted URL,
// key_id) by id, returning ErrNotFound when no row matches.
func (Proxies) UpdateProxy(ctx context.Context, q dbinterface.Execer, p domain.Proxy) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE proxies SET name = ?, type = ?, url_encrypted = ?, key_id = ?, updated_at = ?
			WHERE id = ?`),
		p.Name, p.Type, p.URLEncrypted, p.KeyID, p.UpdatedAt.UTC().Format(timeLayout), p.ID)
	if err != nil {
		return fmt.Errorf("database: update proxy: %w", err)
	}
	return affectedOrNotFoundID(res, p.ID)
}

// SetProxySecret writes the encrypted URL column + key_id by id (phase two of the
// insert-then-seal write, so the credential binds to the freshly-minted row id).
func (Proxies) SetProxySecret(ctx context.Context, q dbinterface.Execer, id int64, urlEncrypted, keyID string) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE proxies SET url_encrypted = ?, key_id = ? WHERE id = ?`),
		urlEncrypted, keyID, id)
	if err != nil {
		return fmt.Errorf("database: set proxy secret: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// DeleteProxy removes a proxy by id, returning ErrNotFound when absent. Referencing
// instances' proxy_id is nulled by the ON DELETE SET NULL foreign key.
func (Proxies) DeleteProxy(ctx context.Context, q dbinterface.Execer, id int64) error {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM proxies WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("database: delete proxy: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// scanProxy reads one proxies row from a *sql.Row or *sql.Rows.
func scanProxy(s interface{ Scan(...any) error }) (domain.Proxy, error) {
	var (
		p                    domain.Proxy
		createdAt, updatedAt string
	)
	if err := s.Scan(&p.ID, &p.Name, &p.Type, &p.URLEncrypted, &p.KeyID, &createdAt, &updatedAt); err != nil {
		return domain.Proxy{}, err //nolint:wrapcheck // sql.ErrNoRows matched by caller; others wrapped there.
	}
	p.CreatedAt, p.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return p, nil
}
