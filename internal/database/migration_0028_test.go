package database_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	_ "github.com/autobrr/harbrr/internal/database" // registers the "sqlite" driver
)

// open0028DB opens a fresh scratch DB with every migration through 0028 (exclusive)
// applied.
func open0028DB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "m.db") + "?_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	applyMigrationsBefore(context.Background(), t, db, "0028")
	return db
}

// exec0028InTx runs the 0028 migration file inside an explicit transaction — the same
// tx.ExecContext(ctx, wholeFileString) call shape applyOne (migrate.go) uses — committing
// on success and rolling back on error (mirroring applyMigrations' deferred Rollback).
func exec0028InTx(ctx context.Context, t *testing.T, db *sql.DB) error {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit succeeds, same as applyMigrations.

	if _, err := tx.ExecContext(ctx, readMigration(t, "0028_drop_dead_sync_columns.sql")); err != nil {
		return err
	}
	return tx.Commit()
}

const ts0028 = "2026-01-01T00:00:00Z"

// seed0028 inserts a populated pre-0028-shape sync model: two Apps, two indexer
// instances, one sync profile (still carrying its dead behavioral columns), two
// app_connections (one profile-routed, one not) and three ledger rows across them — so
// the app_connections rebuild's cascade trap has a ledger to lose.
func seed0028(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()

	exec(ctx, t, db, `INSERT INTO apps (id, kind, name, base_url, key_id, created_at, updated_at)
		VALUES (1, 'sonarr', 'Sonarr', 'http://s:8989', 'k1', ?, ?), (2, 'radarr', 'Radarr', 'http://r:7878', 'k1', ?, ?)`,
		ts0028, ts0028, ts0028, ts0028)
	exec(ctx, t, db, `INSERT INTO indexer_instances (id, slug, definition_id, name, created_at, updated_at)
		VALUES (1, 'tt', 'testtracker', 'TT', ?, ?), (2, 'ot', 'othertracker', 'OT', ?, ?)`,
		ts0028, ts0028, ts0028, ts0028)
	exec(ctx, t, db, `INSERT INTO sync_profiles
		(id, name, categories, min_seeders, enable_rss, enable_automatic_search, enable_interactive_search, created_at, updated_at)
		VALUES (7, 'tv indexers', '5000,5030', 4, 1, 0, 1, ?, ?)`, ts0028, ts0028)

	exec(ctx, t, db, `INSERT INTO app_connections
		(id, name, kind, app_id, harbrr_api_key_encrypted, key_id, enabled, sync_level, index_scope,
		 freeleech_mode, sync_profile_id, last_sync_at, last_sync_status, last_sync_error, created_at, updated_at)
		VALUES (1, 'Sonarr', 'sonarr', 1, 'enc-harbrr-1', 'key-1', 1, 'add_update', 'all',
		        'bypass', 7, ?, 'ok', NULL, ?, ?)`, ts0028, ts0028, ts0028)
	exec(ctx, t, db, `INSERT INTO app_connections
		(id, name, kind, app_id, harbrr_api_key_encrypted, key_id, enabled, sync_level, index_scope,
		 freeleech_mode, sync_profile_id, created_at, updated_at)
		VALUES (2, 'Radarr', 'radarr', 2, 'enc-harbrr-2', 'key-2', 0, 'full', 'all',
		        'honor', NULL, ?, ?)`, ts0028, ts0028)

	exec(ctx, t, db, `INSERT INTO app_connection_indexers
		(id, connection_id, instance_id, remote_id, selected, payload_hash, last_pushed_at, last_push_status, last_push_error)
		VALUES (10, 1, 1, 'remote-7', 1, 'hash-a', ?, 'ok', NULL),
		       (11, 1, 2, 'remote-8', 0, 'hash-b', ?, 'ok', NULL),
		       (12, 2, 1, 'remote-9', 1, 'hash-c', NULL, 'error', 'boom')`, ts0028, ts0028)
}

// indexNames returns the named (non-implicit) indexes sqlite_master holds for table.
func indexNames(ctx context.Context, t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND sql IS NOT NULL ORDER BY name`, table)
	if err != nil {
		t.Fatalf("query sqlite_master indexes for %s: %v", table, err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan index name: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite_master indexes for %s: %v", table, err)
	}
	return names
}

// TestMigration0028DropsDeadColumns proves every column 0024 left behind is gone from the
// table 0024 left it on — and, per table, that the SAME-NAMED live columns on
// indexer_instances (min_seeders / enable_rss / enable_automatic_search /
// enable_interactive_search, which 0023/0024 made the live behavior source) are untouched.
func TestMigration0028DropsDeadColumns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := open0028DB(t)
	seed0028(ctx, t, db)

	if err := exec0028InTx(ctx, t, db); err != nil {
		t.Fatalf("apply 0028: %v", err)
	}

	for _, tc := range []struct {
		table, column string
		want          bool
	}{
		{"sync_profiles", "categories", false},
		{"sync_profiles", "min_seeders", false},
		{"sync_profiles", "enable_rss", false},
		{"sync_profiles", "enable_automatic_search", false},
		{"sync_profiles", "enable_interactive_search", false},
		{"app_connection_indexers", "selected", false},
		{"app_connections", "index_scope", false},
		// The live namesakes on indexer_instances must survive.
		{"indexer_instances", "min_seeders", true},
		{"indexer_instances", "enable_rss", true},
		{"indexer_instances", "enable_automatic_search", true},
		{"indexer_instances", "enable_interactive_search", true},
		{"indexer_instances", "sync_categories", true},
		// Surviving columns on the rebuilt/altered tables.
		{"sync_profiles", "name", true},
		{"app_connection_indexers", "payload_hash", true},
		{"app_connections", "app_id", true},
		{"app_connections", "sync_level", true},
		{"app_connections", "freeleech_mode", true},
		{"app_connections", "sync_profile_id", true},
	} {
		if got := hasColumn(ctx, t, db, tc.table, tc.column); got != tc.want {
			t.Errorf("hasColumn(%s.%s) = %v, want %v", tc.table, tc.column, got, tc.want)
		}
	}

	// The surviving CHECK constraints still bite (the rebuild reproduced them).
	if _, err := db.ExecContext(ctx, `INSERT INTO app_connections
		(name, kind, harbrr_api_key_encrypted, key_id, sync_level, created_at, updated_at)
		VALUES ('bad', 'sonarr', 'enc', 'k', 'nonsense', ?, ?)`, ts0028, ts0028); err == nil {
		t.Error("sync_level CHECK did not survive the rebuild")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO app_connections
		(name, kind, harbrr_api_key_encrypted, key_id, freeleech_mode, created_at, updated_at)
		VALUES ('bad', 'sonarr', 'enc', 'k', 'nonsense', ?, ?)`, ts0028, ts0028); err == nil {
		t.Error("freeleech_mode CHECK did not survive the rebuild")
	}
}

// TestMigration0028PreservesLedger is the cascade trap: app_connection_indexers.
// connection_id is ON DELETE CASCADE and foreign_keys is ON, so the rebuild's
// `DROP TABLE app_connections` wipes the whole sync ledger unless the migration stages the
// children and restores them. A column-only assertion would pass with the ledger gone.
func TestMigration0028PreservesLedger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := open0028DB(t)
	seed0028(ctx, t, db)

	if err := exec0028InTx(ctx, t, db); err != nil {
		t.Fatalf("apply 0028: %v", err)
	}

	type ledger struct {
		id, connID, instID int64
		remoteID, hash     string
		pushedAt           sql.NullString
		status             string
		pushErr            sql.NullString
	}
	want := []ledger{
		{10, 1, 1, "remote-7", "hash-a", sql.NullString{String: ts0028, Valid: true}, "ok", sql.NullString{}},
		{11, 1, 2, "remote-8", "hash-b", sql.NullString{String: ts0028, Valid: true}, "ok", sql.NullString{}},
		{12, 2, 1, "remote-9", "hash-c", sql.NullString{}, "error", sql.NullString{String: "boom", Valid: true}},
	}

	rows, err := db.QueryContext(ctx, `SELECT id, connection_id, instance_id, remote_id, payload_hash,
		last_pushed_at, last_push_status, last_push_error FROM app_connection_indexers ORDER BY id`)
	if err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	defer rows.Close()

	var got []ledger
	for rows.Next() {
		var l ledger
		if err := rows.Scan(&l.id, &l.connID, &l.instID, &l.remoteID, &l.hash,
			&l.pushedAt, &l.status, &l.pushErr); err != nil {
			t.Fatalf("scan ledger: %v", err)
		}
		got = append(got, l)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ledger: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("ledger has %d rows after 0028, want %d — the cascade wiped it (stage/restore missing?): %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ledger row %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// The restored FK references still resolve against the rebuilt parent: deleting a
	// connection cascades exactly its own ledger rows.
	exec(ctx, t, db, `DELETE FROM app_connections WHERE id = 1`)
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_connection_indexers`).Scan(&n); err != nil {
		t.Fatalf("count ledger after delete: %v", err)
	}
	if n != 1 {
		t.Errorf("ledger rows after deleting connection 1 = %d, want 1 (only connection 2's row left)", n)
	}
}

// TestMigration0028PreservesConnectionData proves the rebuild copied every surviving
// app_connections column verbatim — including the encrypted key columns, which a
// mis-ordered INSERT ... SELECT would silently shift.
func TestMigration0028PreservesConnectionData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := open0028DB(t)
	seed0028(ctx, t, db)

	if err := exec0028InTx(ctx, t, db); err != nil {
		t.Fatalf("apply 0028: %v", err)
	}

	type conn struct {
		id                         int64
		name, kind                 string
		appID                      sql.NullInt64
		harbrrKeyID                sql.NullInt64
		harbrrKeyEnc, keyID        string
		enabled                    int
		syncLevel, freeleechMode   string
		profileID                  sql.NullInt64
		lastSyncAt, lastSyncStatus sql.NullString
		lastSyncErr                sql.NullString
		createdAt, updatedAt       string
	}
	want := []conn{
		{
			id: 1, name: "Sonarr", kind: "sonarr",
			appID: sql.NullInt64{Int64: 1, Valid: true}, harbrrKeyEnc: "enc-harbrr-1", keyID: "key-1",
			enabled: 1, syncLevel: "add_update", freeleechMode: "bypass",
			profileID:      sql.NullInt64{Int64: 7, Valid: true},
			lastSyncAt:     sql.NullString{String: ts0028, Valid: true},
			lastSyncStatus: sql.NullString{String: "ok", Valid: true},
			createdAt:      ts0028, updatedAt: ts0028,
		},
		{
			id: 2, name: "Radarr", kind: "radarr",
			appID: sql.NullInt64{Int64: 2, Valid: true}, harbrrKeyEnc: "enc-harbrr-2", keyID: "key-2",
			enabled: 0, syncLevel: "full", freeleechMode: "honor",
			createdAt: ts0028, updatedAt: ts0028,
		},
	}

	rows, err := db.QueryContext(ctx, `SELECT id, name, kind, app_id, harbrr_api_key_id,
		harbrr_api_key_encrypted, key_id, enabled, sync_level, freeleech_mode, sync_profile_id,
		last_sync_at, last_sync_status, last_sync_error, created_at, updated_at
		FROM app_connections ORDER BY id`)
	if err != nil {
		t.Fatalf("query connections: %v", err)
	}
	defer rows.Close()

	var got []conn
	for rows.Next() {
		var c conn
		if err := rows.Scan(&c.id, &c.name, &c.kind, &c.appID, &c.harbrrKeyID,
			&c.harbrrKeyEnc, &c.keyID, &c.enabled, &c.syncLevel, &c.freeleechMode, &c.profileID,
			&c.lastSyncAt, &c.lastSyncStatus, &c.lastSyncErr, &c.createdAt, &c.updatedAt); err != nil {
			t.Fatalf("scan connection: %v", err)
		}
		got = append(got, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate connections: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("app_connections has %d rows after 0028, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("connection %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// The surviving profile row kept its identity through its own DROP COLUMNs.
	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM sync_profiles WHERE id = 7`).Scan(&name); err != nil {
		t.Fatalf("read sync profile: %v", err)
	}
	if name != "tv indexers" {
		t.Errorf("sync_profiles.name = %q, want %q", name, "tv indexers")
	}
}

// TestMigration0028PreservesIndexesAndProfileFK guards the worst outcome of a rebuild: a
// silently lost index. The index set is read out of the real sqlite_master before and
// after, not asserted from the migration text, and the partial UNIQUE(app_id) is then
// exercised for real. It also proves sync_profile_id's ON DELETE SET NULL survived.
func TestMigration0028PreservesIndexesAndProfileFK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := open0028DB(t)
	seed0028(ctx, t, db)

	before := indexNames(ctx, t, db, "app_connections")
	if !slices.Equal(before, []string{"app_connections_app_id_uq"}) {
		t.Fatalf("pre-0028 app_connections indexes = %v; the migration's recreate list is written against "+
			"[app_connections_app_id_uq] and must be updated", before)
	}

	if err := exec0028InTx(ctx, t, db); err != nil {
		t.Fatalf("apply 0028: %v", err)
	}

	if after := indexNames(ctx, t, db, "app_connections"); !slices.Equal(after, before) {
		t.Errorf("app_connections indexes = %v after 0028, want %v", after, before)
	}
	if after := indexNames(ctx, t, db, "app_connection_indexers"); !slices.Equal(after, []string{"app_connection_indexers_conn_idx"}) {
		t.Errorf("app_connection_indexers indexes = %v after 0028, want [app_connection_indexers_conn_idx]", after)
	}

	// The partial unique index still bites on a duplicate non-NULL app_id...
	if _, err := db.ExecContext(ctx, `INSERT INTO app_connections
		(name, kind, app_id, harbrr_api_key_encrypted, key_id, created_at, updated_at)
		VALUES ('dup', 'sonarr', 1, 'enc', 'k', ?, ?)`, ts0028, ts0028); err == nil {
		t.Error("app_connections allowed a second row at app_id=1 (partial unique index lost in the rebuild)")
	}
	// ...and still exempts NULL app_id (more than one hostless row allowed).
	for _, name := range []string{"null1", "null2"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO app_connections
			(name, kind, harbrr_api_key_encrypted, key_id, created_at, updated_at)
			VALUES (?, 'sonarr', 'enc', 'k', ?, ?)`, name, ts0028, ts0028); err != nil {
			t.Errorf("app_connections rejected NULL-app_id row %q: %v", name, err)
		}
	}

	// The UNIQUE(connection_id, instance_id) upsert key survived the child's DROP COLUMN.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO app_connection_indexers (connection_id, instance_id) VALUES (1, 1)`); err == nil {
		t.Error("app_connection_indexers allowed a duplicate (connection_id, instance_id)")
	}

	// sync_profile_id's ON DELETE SET NULL still fires.
	exec(ctx, t, db, `DELETE FROM sync_profiles WHERE id = 7`)
	var profileID sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT sync_profile_id FROM app_connections WHERE id = 1`).Scan(&profileID); err != nil {
		t.Fatalf("read sync_profile_id: %v", err)
	}
	if profileID.Valid {
		t.Errorf("sync_profile_id = %v after deleting the profile, want NULL (ON DELETE SET NULL lost)", profileID)
	}
}
