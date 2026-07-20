package database_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/autobrr/harbrr/internal/database" // registers the "sqlite" driver
)

// TestMigration0022GuardAndDrop proves migration 0022's guard: it refuses to apply
// while any proxies row still has url_encrypted != ” (an un-split, pre-#71 row —
// see 0015/resourcemigrate.SplitProxyURLs), and passes once every row is split
// (host set, url_encrypted cleared back to ”), then confirms the column is
// actually gone and the rest of the row survives.
func TestMigration0022GuardAndDrop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("fresh db passes and drops the column", func(t *testing.T) {
		t.Parallel()
		db := open0022DB(t)
		if err := exec0022InTx(ctx, t, db); err != nil {
			t.Fatalf("guard rejected an empty db: %v", err)
		}
		if hasColumn(ctx, t, db, "proxies", "url_encrypted") {
			t.Error("proxies.url_encrypted still exists after 0022")
		}
	})

	t.Run("split db passes and drops the column", func(t *testing.T) {
		t.Parallel()
		db := open0022DB(t)
		exec(ctx, t, db, `INSERT INTO proxies (name, type, host, port, username, password_encrypted, key_id, url_encrypted, created_at, updated_at)
			VALUES ('p', 'socks5', '10.0.0.9', 1080, '', 'enc', 'k1', '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)

		if err := exec0022InTx(ctx, t, db); err != nil {
			t.Fatalf("guard rejected a fully-split db: %v", err)
		}
		if hasColumn(ctx, t, db, "proxies", "url_encrypted") {
			t.Error("proxies.url_encrypted still exists after 0022")
		}

		var host string
		if err := db.QueryRowContext(ctx, `SELECT host FROM proxies`).Scan(&host); err != nil {
			t.Fatalf("select surviving row: %v", err)
		}
		if host != "10.0.0.9" {
			t.Errorf("host = %q, want 10.0.0.9 (surviving row corrupted by the drop)", host)
		}
	})

	t.Run("unsplit row fires the guard and the whole script rolls back", func(t *testing.T) {
		t.Parallel()
		db := open0022DB(t)
		exec(ctx, t, db, `INSERT INTO proxies (name, type, host, port, username, password_encrypted, key_id, url_encrypted, created_at, updated_at)
			VALUES ('legacy', 'socks5', '', 0, '', '', 'k1', 'sealed-legacy-url', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)

		if err := exec0022InTx(ctx, t, db); err == nil {
			t.Fatal("guard did not fire on an un-split row")
		}

		// Prove the failed script's own DDL (the scratch table/trigger, and the
		// DROP COLUMN after it) did not survive the rollback — the all-or-nothing
		// contract applyMigrations relies on (its deferred tx.Rollback backs out
		// the whole batch).
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_temp_master WHERE name = '_0022_guard'`).Scan(&n); err != nil {
			t.Fatalf("query sqlite_temp_master: %v", err)
		}
		if n != 0 {
			t.Errorf("scratch table _0022_guard survived a failed script (n=%d), want 0 (fully rolled back)", n)
		}
		if !hasColumn(ctx, t, db, "proxies", "url_encrypted") {
			t.Error("proxies.url_encrypted gone after a rolled-back migration — drop partially applied")
		}
	})
}

// open0022DB opens a fresh scratch DB with every migration through 0022 (exclusive)
// applied.
func open0022DB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "m.db") + "?_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	applyMigrationsBefore(context.Background(), t, db, "0022")
	return db
}

// exec0022InTx runs the 0022 migration file inside an explicit transaction — the
// same tx.ExecContext(ctx, wholeFileString) call shape applyOne (migrate.go) uses —
// committing on success and rolling back on error (mirroring applyMigrations'
// deferred Rollback).
func exec0022InTx(ctx context.Context, t *testing.T, db *sql.DB) error {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit succeeds, same as applyMigrations.

	if _, err := tx.ExecContext(ctx, readMigration(t, "0022_drop_proxy_url_encrypted.sql")); err != nil {
		return err
	}
	return tx.Commit()
}
