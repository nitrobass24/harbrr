package database

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// migrationsFS holds the embedded, forward-only SQL migrations. They are applied
// in lexical filename order; new schema lands as a new NNNN_*.sql file, never by
// editing a shipped one (an applied migration's filename is recorded and skipped
// on the next run).
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsDir is the embed subdirectory holding the .sql files.
const migrationsDir = "migrations"

// Compile-time proof the SQLite store satisfies the storage seam.
var _ dbinterface.Querier = (*DB)(nil)

// Migrate applies every pending embedded migration inside a single transaction,
// then tightens file permissions. It is idempotent: already-applied filenames
// (tracked in schema_migrations) are skipped, so re-running is a no-op. A clock
// is injected for the applied_at timestamp so tests stay deterministic.
func (db *DB) Migrate(ctx context.Context) error {
	return db.migrate(ctx, time.Now)
}

// migrate is Migrate with an injectable clock.
func (db *DB) migrate(ctx context.Context, now func() time.Time) error {
	if err := db.ensureMigrationsTable(ctx); err != nil {
		return err
	}
	applied, err := db.appliedMigrations(ctx)
	if err != nil {
		return err
	}
	pending, err := pendingMigrations(applied)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		if err := db.applyMigrations(ctx, pending, now); err != nil {
			return err
		}
	}
	if err := db.secureDBFiles(); err != nil {
		return err
	}
	return nil
}

// ensureMigrationsTable creates the bookkeeping table if absent.
func (db *DB) ensureMigrationsTable(ctx context.Context) error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
	filename   TEXT PRIMARY KEY,
	applied_at TEXT NOT NULL
)`
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("database: ensure schema_migrations: %w", err)
	}
	return nil
}

// appliedMigrations returns the set of already-applied migration filenames.
func (db *DB) appliedMigrations(ctx context.Context) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, "SELECT filename FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("database: read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[string]struct{}{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("database: scan migration row: %w", err)
		}
		applied[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate migrations: %w", err)
	}
	return applied, nil
}

// pendingMigrations lists embedded migration filenames not yet applied, in
// lexical order.
func pendingMigrations(applied map[string]struct{}) ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("database: read embedded migrations: %w", err)
	}
	var pending []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, done := applied[e.Name()]; done {
			continue
		}
		pending = append(pending, e.Name())
	}
	sort.Strings(pending)
	return pending, nil
}

// applyMigrations runs all pending files plus their bookkeeping inserts in one
// transaction, so a failure leaves the schema untouched (all-or-nothing).
func (db *DB) applyMigrations(ctx context.Context, pending []string, now func() time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit succeeds

	for _, name := range pending {
		if err := applyOne(ctx, tx, name, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("database: commit migrations: %w", err)
	}
	return nil
}

// applyOne executes a single migration file and records it as applied.
func applyOne(ctx context.Context, tx dbinterface.TxQuerier, name string, now func() time.Time) error {
	body, err := migrationsFS.ReadFile(migrationsDir + "/" + name)
	if err != nil {
		return fmt.Errorf("database: read migration %q: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("database: apply migration %q: %w", name, err)
	}
	const ins = "INSERT INTO schema_migrations (filename, applied_at) VALUES (?, ?)"
	if _, err := tx.ExecContext(ctx, tx.Rebind(ins), name, now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("database: record migration %q: %w", name, err)
	}
	return nil
}
