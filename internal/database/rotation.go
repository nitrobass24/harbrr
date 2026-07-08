package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
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

// SecretColumn is one AEAD-ciphertext column on a FixedAADSurface: the ciphertext
// column plus the CONSTANT AAD discriminator its owning service passes when it seals.
// Setting must byte-match that service verbatim — the rotation re-seals each blob
// under aad(row.id, Setting), and a mismatch produces ciphertext the service can no
// longer open (the U8-F1 bug). Cited to the verified sealing site in SecretSurfaces.
type SecretColumn struct {
	Cipher  string // the *_encrypted column
	Setting string // AAD discriminator string the service seals with
}

// FixedAADSurface is a secret-bearing table whose ciphertext binds to the row's OWN
// id and a per-column constant discriminator (unlike indexer_settings, whose AAD is
// driven by its instance_id + name columns and is rotated via AllSecrets/UpdateSecret).
// One row may hold several ciphertext columns that all share a single key_id column.
type FixedAADSurface struct {
	Table    string
	KeyIDCol string
	Columns  []SecretColumn
}

// SecretSurfaces enumerates EVERY fixed-AAD table holding at-rest ciphertext that
// rotate-key must re-encrypt. This is the single source of truth for rotation
// coverage: adding a new secret column WITHOUT listing it here silently drops it
// from key rotation, stranding those credentials under the old key on the next
// rotate (the U8-F1 bug). indexer_settings is the one exception — its AAD is column
// -driven (instance_id + name), so it is rotated separately via AllSecrets. Each
// Setting below is verified against the service's actual sealing call site; keep
// them in sync with those constants.
func SecretSurfaces() []FixedAADSurface {
	return []FixedAADSurface{
		{
			// internal/appsync/service.go + internal/announce/service.go seal both
			// columns with the connection id as AAD id; secretApp="app", secretHarbrr="harbrr".
			Table: "app_connections", KeyIDCol: "key_id",
			Columns: []SecretColumn{
				{Cipher: "api_key_encrypted", Setting: "app"},
				{Cipher: "harbrr_api_key_encrypted", Setting: "harbrr"},
			},
		},
		{
			Table: "announce_connections", KeyIDCol: "key_id",
			Columns: []SecretColumn{
				{Cipher: "api_key_encrypted", Setting: "app"},
				{Cipher: "harbrr_api_key_encrypted", Setting: "harbrr"},
			},
		},
		{
			// internal/notify/service.go seals with the notification id; secretURL="url".
			Table: "notifications", KeyIDCol: "key_id",
			Columns: []SecretColumn{{Cipher: "url_encrypted", Setting: "url"}},
		},
		{
			// internal/proxy/service.go seals with the proxy id; domain.ProxySecretURL.
			Table: "proxies", KeyIDCol: "key_id",
			Columns: []SecretColumn{{Cipher: "url_encrypted", Setting: domain.ProxySecretURL}},
		},
		{
			// internal/solver/service.go seals with the solver id; domain.SolverSecretURL.
			Table: "solvers", KeyIDCol: "key_id",
			Columns: []SecretColumn{{Cipher: "url_encrypted", Setting: domain.SolverSecretURL}},
		},
	}
}

// SurfaceRow is one row of a FixedAADSurface: its id and the ciphertext of each of
// the surface's columns, in Columns order. An empty string is an absent/empty secret.
type SurfaceRow struct {
	ID      int64
	Ciphers []string
}

// SurfaceRows reads every row's id + ciphertext columns for a fixed-AAD surface,
// ordered by id. Column names come from the typed surface (code constants, never
// user input), so the built SQL is safe.
func (Rotation) SurfaceRows(ctx context.Context, q dbinterface.Execer, s FixedAADSurface) ([]SurfaceRow, error) {
	cols := make([]string, 0, len(s.Columns)+1)
	cols = append(cols, "id")
	for _, c := range s.Columns {
		cols = append(cols, c.Cipher)
	}
	query := fmt.Sprintf("SELECT %s FROM %s ORDER BY id", strings.Join(cols, ", "), s.Table)
	rows, err := q.QueryContext(ctx, q.Rebind(query))
	if err != nil {
		return nil, fmt.Errorf("database: query %s secrets: %w", s.Table, err)
	}
	defer rows.Close()

	var out []SurfaceRow
	for rows.Next() {
		var id int64
		encs := make([]sql.NullString, len(s.Columns))
		dest := make([]any, 0, len(s.Columns)+1)
		dest = append(dest, &id)
		for i := range encs {
			dest = append(dest, &encs[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("database: scan %s secret: %w", s.Table, err)
		}
		r := SurfaceRow{ID: id, Ciphers: make([]string, len(encs))}
		for i, e := range encs {
			r.Ciphers[i] = e.String
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate %s secrets: %w", s.Table, err)
	}
	return out, nil
}

// UpdateSurface rewrites one row's ciphertext columns + shared key_id (key rotation).
// blobs is parallel to s.Columns. These columns are NOT NULL, so a value is stored
// verbatim (an empty secret stays the empty string, never NULL).
func (Rotation) UpdateSurface(ctx context.Context, q dbinterface.Execer, s FixedAADSurface, id int64, blobs []string, keyID string) error {
	sets := make([]string, 0, len(s.Columns)+1)
	args := make([]any, 0, len(s.Columns)+2)
	for i, c := range s.Columns {
		sets = append(sets, c.Cipher+" = ?")
		args = append(args, blobs[i])
	}
	sets = append(sets, s.KeyIDCol+" = ?")
	args = append(args, keyID, id)
	query := fmt.Sprintf("UPDATE %s SET %s WHERE id = ?", s.Table, strings.Join(sets, ", "))
	if _, err := q.ExecContext(ctx, q.Rebind(query), args...); err != nil {
		return fmt.Errorf("database: update %s secret %d: %w", s.Table, id, err)
	}
	return nil
}
