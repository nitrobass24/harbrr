package database_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/autobrr/harbrr/internal/database" // registers the "sqlite" driver
)

// open0024DB opens a fresh scratch DB with every migration through 0024 (exclusive)
// applied.
func open0024DB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "m.db") + "?_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	applyMigrationsBefore(context.Background(), t, db, "0024")
	return db
}

// readMemberIDs returns a minted profile's sync_profile_indexers instance ids, ordered.
func readMemberIDs(ctx context.Context, t *testing.T, db *sql.DB, profileID int64) []int64 {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT instance_id FROM sync_profile_indexers WHERE profile_id = ? ORDER BY instance_id`, profileID)
	if err != nil {
		t.Fatalf("query profile members: %v", err)
	}
	defer rows.Close()

	var members []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan member: %v", err)
		}
		members = append(members, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate profile members: %v", err)
	}
	return members
}

// exec0024InTx runs the 0024 migration file inside an explicit transaction — the same
// tx.ExecContext(ctx, wholeFileString) call shape applyOne (migrate.go) uses —
// committing on success and rolling back on error (mirroring applyMigrations' deferred
// Rollback).
func exec0024InTx(ctx context.Context, t *testing.T, db *sql.DB) error {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit succeeds, same as applyMigrations.

	if _, err := tx.ExecContext(ctx, readMigration(t, "0024_sync_profile_routing.sql")); err != nil {
		return err
	}
	return tx.Commit()
}

// seed0024Instance inserts a pre-0024-shape indexer_instances row (no enable_*/
// sync_categories columns yet — those are what the migration under test adds).
func seed0024Instance(ctx context.Context, t *testing.T, db *sql.DB, id int64, slug string, minSeeders int) {
	t.Helper()
	const ts = "2026-01-01T00:00:00Z"
	exec(ctx, t, db, `INSERT INTO indexer_instances (id, slug, definition_id, name, priority, min_seeders, created_at, updated_at)
		VALUES (?, ?, ?, ?, 25, ?, ?, ?)`, id, slug, slug, slug, minSeeders, ts, ts)
}

// seed0024Profile inserts a pre-0024-shape sync_profiles row (behavioral columns —
// categories/min_seeders/enable_* — still exist pre-migration; they are what the
// transform's step (a) reads and step (c)'s later cleanup migration will drop).
func seed0024Profile(ctx context.Context, t *testing.T, db *sql.DB, id int64, name, categories string, minSeeders, rss, auto, interactive int) {
	t.Helper()
	const ts = "2026-01-01T00:00:00Z"
	exec(ctx, t, db, `INSERT INTO sync_profiles
		(id, name, categories, min_seeders, enable_rss, enable_automatic_search, enable_interactive_search, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, name, categories, minSeeders, rss, auto, interactive, ts, ts)
}

// seed0024Connection inserts a pre-0024-shape app_connections row.
func seed0024Connection(ctx context.Context, t *testing.T, db *sql.DB, id int64, name, indexScope string, profileID *int64) {
	t.Helper()
	const ts = "2026-01-01T00:00:00Z"
	exec(ctx, t, db, `INSERT INTO app_connections
		(id, name, kind, harbrr_api_key_encrypted, key_id, sync_level, index_scope, freeleech_mode, sync_profile_id, created_at, updated_at)
		VALUES (?, ?, 'sonarr', 'enc', 'k1', 'full', ?, 'honor', ?, ?, ?)`, id, name, indexScope, profileID, ts, ts)
}

// seed0024Ledger inserts a pre-0024-shape app_connection_indexers row.
func seed0024Ledger(ctx context.Context, t *testing.T, db *sql.DB, connID, instID int64, selected bool) {
	t.Helper()
	sel := 0
	if selected {
		sel = 1
	}
	exec(ctx, t, db, `INSERT INTO app_connection_indexers (connection_id, instance_id, selected) VALUES (?, ?, ?)`, connID, instID, sel)
}

// TestMigration0024MintsRoutingProfileAndBackfillsBehavior is the combined scenario the
// transform's step ordering exists for: a single index_scope='selected' connection that
// ALSO references a behavioral profile. Because exactly one profile is referenced across
// all connections, step (a) backfills that profile's behavior onto every instance BEFORE
// step (b) mints a fresh routing profile from the ledger selection and overwrites the
// connection's sync_profile_id to point at it (not the original behavioral profile).
func TestMigration0024MintsRoutingProfileAndBackfillsBehavior(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := open0024DB(t)

	// Three instances; the connection selects two of them (1 and 3, not 2).
	seed0024Instance(ctx, t, db, 1, "alpha", 0)
	seed0024Instance(ctx, t, db, 2, "beta", 0)
	seed0024Instance(ctx, t, db, 3, "gamma", 9) // pre-existing per-indexer floor — must NOT be overwritten

	profileID := int64(100)
	seed0024Profile(ctx, t, db, profileID, "tv-profile", "5000,5030", 4, 1, 0, 1)

	connID := int64(1)
	seed0024Connection(ctx, t, db, connID, "Sonarr", "selected", &profileID)
	seed0024Ledger(ctx, t, db, connID, 1, true)
	seed0024Ledger(ctx, t, db, connID, 2, false)
	seed0024Ledger(ctx, t, db, connID, 3, true)

	if err := exec0024InTx(ctx, t, db); err != nil {
		t.Fatalf("apply 0024: %v", err)
	}

	// Step (a): every instance inherits the sole referenced profile's behavior. Instance
	// 3's own min_seeders (9) wins over the profile's (4) — the CASE only fills unset (0).
	type behavior struct {
		rss, auto, interactive int
		cats                   string
		minSeeders             int
	}
	want := map[int64]behavior{
		1: {1, 0, 1, "5000,5030", 4},
		2: {1, 0, 1, "5000,5030", 4},
		3: {1, 0, 1, "5000,5030", 9},
	}
	for id, w := range want {
		var got behavior
		if err := db.QueryRowContext(ctx,
			`SELECT enable_rss, enable_automatic_search, enable_interactive_search, sync_categories, min_seeders
			 FROM indexer_instances WHERE id = ?`, id).
			Scan(&got.rss, &got.auto, &got.interactive, &got.cats, &got.minSeeders); err != nil {
			t.Fatalf("read instance %d: %v", id, err)
		}
		if got != w {
			t.Errorf("instance %d behavior = %+v, want %+v", id, got, w)
		}
	}

	// Step (b): a NEW routing profile was minted (id-suffixed name), selecting exactly the
	// two ledger-selected instances (1, 3) — not the untouched-behavior source profile.
	var mintedID int64
	var mintedName string
	if err := db.QueryRowContext(ctx,
		`SELECT id, name FROM sync_profiles WHERE name = ?`, "Sonarr indexers (1)").
		Scan(&mintedID, &mintedName); err != nil {
		t.Fatalf("minted profile not found: %v", err)
	}
	if mintedID == profileID {
		t.Fatal("minted profile reused the original behavioral profile's id")
	}
	members := readMemberIDs(ctx, t, db, mintedID)
	if len(members) != 2 || members[0] != 1 || members[1] != 3 {
		t.Fatalf("minted profile members = %v, want [1 3]", members)
	}

	// The connection's ref was overwritten to the MINTED profile, and index_scope
	// neutralized to 'all' (step c) — code stops reading it.
	var gotProfileID sql.NullInt64
	var gotScope string
	if err := db.QueryRowContext(ctx, `SELECT sync_profile_id, index_scope FROM app_connections WHERE id = ?`, connID).
		Scan(&gotProfileID, &gotScope); err != nil {
		t.Fatalf("read connection: %v", err)
	}
	if !gotProfileID.Valid || gotProfileID.Int64 != mintedID {
		t.Errorf("connection sync_profile_id = %v, want the minted profile %d", gotProfileID, mintedID)
	}
	if gotScope != "all" {
		t.Errorf("connection index_scope = %q, want neutralized to 'all'", gotScope)
	}
}

// TestMigration0024AmbiguousMultiProfileLeavesDefaults proves the ambiguous case (more
// than one distinct profile referenced across all connections) is left at the safe
// defaults — toggles on, no category narrowing — rather than guessing a mapping.
func TestMigration0024AmbiguousMultiProfileLeavesDefaults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := open0024DB(t)

	seed0024Instance(ctx, t, db, 1, "one", 0)
	seed0024Instance(ctx, t, db, 2, "two", 0)

	profA, profB := int64(10), int64(20)
	seed0024Profile(ctx, t, db, profA, "movies", "2000", 3, 0, 0, 0)
	seed0024Profile(ctx, t, db, profB, "tv", "5000", 5, 1, 1, 1)
	seed0024Connection(ctx, t, db, 1, "Radarr", "all", &profA)
	seed0024Connection(ctx, t, db, 2, "Sonarr", "all", &profB)

	if err := exec0024InTx(ctx, t, db); err != nil {
		t.Fatalf("apply 0024: %v", err)
	}

	for _, id := range []int64{1, 2} {
		var rss, auto, interactive, minSeeders int
		var cats string
		if err := db.QueryRowContext(ctx,
			`SELECT enable_rss, enable_automatic_search, enable_interactive_search, sync_categories, min_seeders
			 FROM indexer_instances WHERE id = ?`, id).
			Scan(&rss, &auto, &interactive, &cats, &minSeeders); err != nil {
			t.Fatalf("read instance %d: %v", id, err)
		}
		if rss != 1 || auto != 1 || interactive != 1 || cats != "" || minSeeders != 0 {
			t.Errorf("instance %d = rss=%d auto=%d interactive=%d cats=%q minSeeders=%d, want defaults (1,1,1,'',0)",
				id, rss, auto, interactive, cats, minSeeders)
		}
	}
}

// TestMigration0024NoReferencedProfileLeavesDefaults proves the zero-profile case (no
// connection references any profile) also leaves the safe defaults, and that index_scope
// is neutralized to 'all' even for an 'all'-scope connection that never used it.
func TestMigration0024NoReferencedProfileLeavesDefaults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := open0024DB(t)

	seed0024Instance(ctx, t, db, 1, "solo", 0)
	seed0024Connection(ctx, t, db, 1, "Sonarr", "all", nil)

	if err := exec0024InTx(ctx, t, db); err != nil {
		t.Fatalf("apply 0024: %v", err)
	}

	var rss, auto, interactive int
	var cats string
	if err := db.QueryRowContext(ctx,
		`SELECT enable_rss, enable_automatic_search, enable_interactive_search, sync_categories FROM indexer_instances WHERE id = 1`).
		Scan(&rss, &auto, &interactive, &cats); err != nil {
		t.Fatalf("read instance: %v", err)
	}
	if rss != 1 || auto != 1 || interactive != 1 || cats != "" {
		t.Errorf("instance = rss=%d auto=%d interactive=%d cats=%q, want defaults (1,1,1,'')", rss, auto, interactive, cats)
	}

	var gotScope string
	if err := db.QueryRowContext(ctx, `SELECT index_scope FROM app_connections WHERE id = 1`).Scan(&gotScope); err != nil {
		t.Fatalf("read connection: %v", err)
	}
	if gotScope != "all" {
		t.Errorf("index_scope = %q, want 'all'", gotScope)
	}
}

// TestMigration0024AddsSchema proves the additive schema lands on a fresh (empty) DB: the
// new indexer_instances columns and the sync_profile_indexers join table both exist, and
// the join table's FKs cascade on either parent's delete.
func TestMigration0024AddsSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := open0024DB(t)

	if err := exec0024InTx(ctx, t, db); err != nil {
		t.Fatalf("apply 0024: %v", err)
	}
	for _, col := range []string{"enable_rss", "enable_automatic_search", "enable_interactive_search", "sync_categories"} {
		if !hasColumn(ctx, t, db, "indexer_instances", col) {
			t.Errorf("indexer_instances.%s missing after 0024", col)
		}
	}

	profileID := int64(1)
	seed0024Profile(ctx, t, db, profileID, "p", "", 0, 1, 1, 1)
	seed0024Instance(ctx, t, db, 1, "cascade-test", 0)
	exec(ctx, t, db, `INSERT INTO sync_profile_indexers (profile_id, instance_id) VALUES (?, ?)`, profileID, 1)

	// Deleting the instance cascades the join row without touching the profile.
	exec(ctx, t, db, `DELETE FROM indexer_instances WHERE id = 1`)
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_profile_indexers`).Scan(&n); err != nil {
		t.Fatalf("count sync_profile_indexers: %v", err)
	}
	if n != 0 {
		t.Errorf("sync_profile_indexers row survived instance delete (n=%d), want cascaded to 0", n)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_profiles WHERE id = ?`, profileID).Scan(&n); err != nil {
		t.Fatalf("count sync_profiles: %v", err)
	}
	if n != 1 {
		t.Errorf("profile row wrongly removed by the instance-side cascade (n=%d), want 1", n)
	}
}
