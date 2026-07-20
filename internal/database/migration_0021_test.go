package database_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/autobrr/harbrr/internal/database" // registers the "sqlite" driver
)

// TestMigration0021GuardDeRisk is the mandatory de-risk spike (plan §3's risk note): it
// proves modernc.org/sqlite executes a CREATE TRIGGER ... BEGIN ... END block (an internal
// semicolon) followed by more top-level statements — the guard, then all three table
// rebuilds — inside one tx.ExecContext(ctx, wholeFileString) call, the same call shape
// applyOne (migrate.go) already uses for plain multi-statement scripts (proven by 0008's
// test; this is the first one with a trigger body). It also proves the guard itself: fires
// on an un-folded row (whole transaction rolls back, nothing partially applied), and passes
// on folded/empty/host-less-only DBs.
func TestMigration0021GuardDeRisk(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("folded db passes", func(t *testing.T) {
		t.Parallel()
		db := open0021DB(t)
		seedAppsForGuard(ctx, t, db, true)
		if err := exec0021InTx(ctx, t, db); err != nil {
			t.Fatalf("guard rejected a fully-folded db: %v", err)
		}
	})

	t.Run("empty db passes", func(t *testing.T) {
		t.Parallel()
		db := open0021DB(t)
		if err := exec0021InTx(ctx, t, db); err != nil {
			t.Fatalf("guard rejected an empty db: %v", err)
		}
	})

	t.Run("host-less download client passes", func(t *testing.T) {
		t.Parallel()
		db := open0021DB(t)
		exec(ctx, t, db, `INSERT INTO download_clients (name, kind, host, username, secret_encrypted, key_id, created_at, updated_at)
			VALUES ('bh', 'blackhole', '', '', '', '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
		if err := exec0021InTx(ctx, t, db); err != nil {
			t.Fatalf("guard rejected a host-less (blackhole) row: %v", err)
		}
	})

	t.Run("unfolded row fires the guard and the whole script rolls back", func(t *testing.T) {
		t.Parallel()
		db := open0021DB(t)
		seedAppsForGuard(ctx, t, db, false)

		if err := exec0021InTx(ctx, t, db); err == nil {
			t.Fatal("guard did not fire on an unfolded row")
		}

		// Prove the failed script's own DDL (the scratch table/trigger, and the table
		// rebuilds after it) did not survive the rollback — the all-or-nothing contract
		// applyMigrations relies on (its deferred tx.Rollback backs out the whole batch).
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_temp_master WHERE name = '_0021_guard'`).Scan(&n); err != nil {
			t.Fatalf("query sqlite_temp_master: %v", err)
		}
		if n != 0 {
			t.Errorf("scratch table _0021_guard survived a failed script (n=%d), want 0 (fully rolled back)", n)
		}
		if !hasColumn(ctx, t, db, "app_connections", "base_url") {
			t.Error("app_connections.base_url gone after a rolled-back migration — rebuild partially applied")
		}
	})
}

// TestMigration0021DropsLegacyColumnsAndMovesUniqueness applies 0021 against a
// fully-folded, populated DB and asserts the target shape: the legacy columns are gone,
// the two push tables enforce a partial UNIQUE(app_id), and download_clients gains no
// app_id uniqueness (two clients may share one App).
func TestMigration0021DropsLegacyColumnsAndMovesUniqueness(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := open0021DB(t)

	seedAppsForGuard(ctx, t, db, true)
	// A second download client sharing the same app_id as the seeded one — must be
	// allowed (no app_id uniqueness on download_clients).
	exec(ctx, t, db, `INSERT INTO download_clients (name, kind, app_id, host, username, secret_encrypted, key_id, created_at, updated_at)
		VALUES ('qbit2', 'qbittorrent', 1, 'http://qbit:8080', '', 'enc-secret', 'k1', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)

	if err := exec0021InTx(ctx, t, db); err != nil {
		t.Fatalf("apply 0021: %v", err)
	}

	for _, tc := range []struct {
		table, column string
	}{
		{"app_connections", "base_url"},
		{"app_connections", "api_key_encrypted"},
		{"app_connections", "harbrr_url"},
		{"announce_connections", "base_url"},
		{"announce_connections", "api_key_encrypted"},
		{"announce_connections", "harbrr_url"},
		{"download_clients", "host"},
		{"download_clients", "username"},
		{"download_clients", "secret_encrypted"},
	} {
		if hasColumn(ctx, t, db, tc.table, tc.column) {
			t.Errorf("%s.%s still exists after 0021", tc.table, tc.column)
		}
	}
	// Surviving columns/rows are intact — the rebuild didn't just drop the table.
	if !hasColumn(ctx, t, db, "app_connections", "app_id") || !hasColumn(ctx, t, db, "app_connections", "sync_profile_id") {
		t.Error("app_connections lost a surviving column it should have kept")
	}

	// The two push tables reject a second row at the same app_id...
	if _, err := db.ExecContext(ctx, `INSERT INTO app_connections
		(name, kind, app_id, harbrr_api_key_encrypted, key_id, created_at, updated_at)
		VALUES ('dup', 'radarr', 1, 'enc', 'k1', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err == nil {
		t.Error("app_connections allowed two rows with the same app_id (partial unique index missing)")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO announce_connections
		(name, kind, app_id, harbrr_api_key_encrypted, key_id, created_at, updated_at)
		VALUES ('dup', 'crossseed-v6', 1, 'enc', 'k1', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err == nil {
		t.Error("announce_connections allowed two rows with the same app_id (partial unique index missing)")
	}
	// ...but a NULL app_id is exempt from the partial index (multiple allowed).
	if _, err := db.ExecContext(ctx, `INSERT INTO app_connections
		(name, kind, harbrr_api_key_encrypted, key_id, created_at, updated_at)
		VALUES ('null1', 'radarr', 'enc', 'k1', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Errorf("app_connections rejected a NULL app_id row: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO app_connections
		(name, kind, harbrr_api_key_encrypted, key_id, created_at, updated_at)
		VALUES ('null2', 'radarr', 'enc', 'k1', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Errorf("app_connections rejected a second NULL app_id row: %v", err)
	}

	// download_clients: the second same-app_id row seeded above survived the rebuild —
	// two clients sharing one App is allowed by design.
	var dlCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM download_clients WHERE app_id = 1`).Scan(&dlCount); err != nil {
		t.Fatalf("count download_clients: %v", err)
	}
	if dlCount != 2 {
		t.Errorf("download_clients sharing app_id=1 = %d, want 2 (no app_id uniqueness)", dlCount)
	}

	// The app_connection_indexers ledger row survived the app_connections rebuild.
	var remoteID string
	if err := db.QueryRowContext(ctx, `SELECT remote_id FROM app_connection_indexers WHERE connection_id = 1`).Scan(&remoteID); err != nil {
		t.Fatalf("ledger row missing after 0021 (cascade wiped it?): %v", err)
	}
}

// open0021DB opens a fresh scratch DB with every migration through 0021 (exclusive)
// applied, plus one indexer instance + ledger row so app_connection_indexers has
// something to preserve across the rebuild.
func open0021DB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "m.db") + "?_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	applyMigrationsBefore(context.Background(), t, db, "0021")
	exec(context.Background(), t, db, `INSERT INTO indexer_instances (id, slug, definition_id, name, created_at, updated_at)
		VALUES (1, 'tt', 'testtracker', 'TT', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	return db
}

// exec0021InTx runs the 0021 migration file inside an explicit transaction — the same
// tx.ExecContext(ctx, wholeFileString) call shape applyOne uses in migrate.go — committing
// on success and rolling back on error (mirroring applyMigrations' deferred Rollback).
func exec0021InTx(ctx context.Context, t *testing.T, db *sql.DB) error {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit succeeds, same as applyMigrations.

	if _, err := tx.ExecContext(ctx, readMigration(t, "0021_drop_legacy_app_columns.sql")); err != nil {
		return err
	}
	return tx.Commit()
}

// seedAppsForGuard inserts one row in each of the three surface tables (plus one
// app_connection_indexers ledger row referencing the app_connections row, id 1, so the
// rebuild's ledger-preservation path is also exercised). When folded is true every row
// carries a non-NULL app_id (the guard must pass); when false every row's app_id is left
// NULL (the guard must fire).
func seedAppsForGuard(ctx context.Context, t *testing.T, db *sql.DB, folded bool) {
	t.Helper()
	const ts = "2026-01-01T00:00:00Z"

	appID := "NULL"
	if folded {
		exec(ctx, t, db, `INSERT INTO apps (id, kind, name, base_url, key_id, created_at, updated_at)
			VALUES (1, 'sonarr', 'Sonarr', 'http://s:8989', 'k1', ?, ?)`, ts, ts)
		appID = "1"
	}

	exec(ctx, t, db, `INSERT INTO app_connections
		(id, name, kind, app_id, base_url, api_key_encrypted, harbrr_url, harbrr_api_key_encrypted, key_id, created_at, updated_at)
		VALUES (1, 'Sonarr', 'sonarr', `+appID+`, 'http://s:8989', 'enc-app', 'http://h:8787', 'enc-harbrr', 'k1', ?, ?)`, ts, ts)
	exec(ctx, t, db, `INSERT INTO app_connection_indexers (connection_id, instance_id, remote_id) VALUES (1, 1, 'remote-7')`)
	exec(ctx, t, db, `INSERT INTO announce_connections
		(name, kind, app_id, base_url, api_key_encrypted, harbrr_url, harbrr_api_key_encrypted, key_id, created_at, updated_at)
		VALUES ('qui', 'qui', `+appID+`, 'http://q:7476', 'enc-app', 'http://h:8787', 'enc-harbrr', 'k1', ?, ?)`, ts, ts)
	exec(ctx, t, db, `INSERT INTO download_clients (name, kind, app_id, host, username, secret_encrypted, key_id, created_at, updated_at)
		VALUES ('qbit', 'qbittorrent', `+appID+`, 'http://qbit:8080', '', 'enc-secret', 'k1', ?, ?)`, ts, ts)
}

// hasColumn reports whether table currently has the named column (PRAGMA table_info).
func hasColumn(ctx context.Context, t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate pragma_table_info(%s): %v", table, err)
	}
	return false
}
