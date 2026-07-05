package database_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// openMigrated opens a database at path and applies all migrations, failing the
// test on any error. The returned DB is closed via t.Cleanup.
func openMigrated(t *testing.T, path string) *database.DB {
	t.Helper()
	db, err := database.Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

func TestMigrateCreatesSchema(t *testing.T) {
	t.Parallel()

	for _, path := range []string{":memory:", ""} {
		name := path
		if name == "" {
			name = "tempfile"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := path
			if p == "" {
				p = filepath.Join(t.TempDir(), "harbrr.db")
			}
			db := openMigrated(t, p)

			wantTables := []string{
				"users", "api_keys", "indexer_instances",
				"indexer_settings", "app_meta", "sessions", "search_cache", "app_settings",
				"cache_counters", "indexer_stat_counters", "notifications",
				"proxies", "solvers", "sync_profiles", "schema_migrations",
			}
			for _, tbl := range wantTables {
				var n int
				err := db.QueryRowContext(
					context.Background(),
					"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", tbl,
				).Scan(&n)
				if err != nil {
					t.Fatalf("query table %q: %v", tbl, err)
				}
				if n != 1 {
					t.Errorf("table %q: present=%d, want 1", tbl, n)
				}
			}
		})
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "harbrr.db")
	db := openMigrated(t, p) // first apply

	// Second apply must be a no-op and must not error.
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	// Each migration recorded once (0001_init.sql, 0002_indexer_health.sql,
	// 0003_appsync.sql, 0004_search_cache.sql, 0005_indexer_protocol.sql,
	// 0006_app_settings.sql, 0007_cache_counters.sql,
	// 0008_app_connections_freeleech.sql, 0009_announce_connections.sql,
	// 0010_indexer_stat_counters.sql, 0011_notifications.sql,
	// 0012_proxies_solvers.sql, 0013_sync_profiles.sql), not duplicated by the
	// second apply.
	const wantMigrations = 13
	var applied int
	if err := db.QueryRowContext(context.Background(),
		"SELECT count(*) FROM schema_migrations").Scan(&applied); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if applied != wantMigrations {
		t.Errorf("schema_migrations rows = %d, want %d", applied, wantMigrations)
	}
}

// TestPragmasApplied proves the modernc _pragma DSN parameters actually engage
// on the live connection — the foundation of the WAL/concurrency/FK design.
func TestPragmasApplied(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()

	var journal string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if !strings.EqualFold(journal, "wal") {
		t.Errorf("journal_mode = %q, want wal", journal)
	}

	var fk int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}

	var busy int
	if err := db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busy != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busy)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()

	// A setting referencing a non-existent instance must be rejected (FK on).
	_, err := db.ExecContext(ctx,
		"INSERT INTO indexer_settings (instance_id, name, value, is_secret) VALUES (?, ?, ?, 0)",
		9999, "k", "v")
	if err == nil {
		t.Fatal("insert with dangling instance_id succeeded, want foreign-key violation")
	}
}

func TestCascadeDeleteSettings(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()

	res, err := db.ExecContext(ctx,
		"INSERT INTO indexer_instances (slug, definition_id, name, created_at, updated_at) VALUES (?,?,?,?,?)",
		"tl", "torrentleech", "TL", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	id, _ := res.LastInsertId()
	if _, err := db.ExecContext(ctx,
		"INSERT INTO indexer_settings (instance_id, name, value, is_secret) VALUES (?,?,?,0)",
		id, "sort", "seeders"); err != nil {
		t.Fatalf("insert setting: %v", err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM indexer_instances WHERE id=?", id); err != nil {
		t.Fatalf("delete instance: %v", err)
	}

	var remaining int
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM indexer_settings WHERE instance_id=?", id).Scan(&remaining); err != nil {
		t.Fatalf("count settings: %v", err)
	}
	if remaining != 0 {
		t.Errorf("settings after cascade delete = %d, want 0", remaining)
	}
}

// TestUpsertSetting proves UpsertSetting inserts a setting then updates the same
// (instance_id, name) row in place — no duplicate — while leaving sibling settings
// untouched. This is the seam a native driver uses to persist a rotated credential.
func TestUpsertSetting(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()
	insts := database.Instances{}

	res, err := db.ExecContext(ctx,
		"INSERT INTO indexer_instances (slug, definition_id, name, created_at, updated_at) VALUES (?,?,?,?,?)",
		"mam", "myanonamouse", "MAM", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	id, _ := res.LastInsertId()

	// A sibling setting that must survive the upsert.
	if err := insts.InsertSetting(ctx, db, id, domain.IndexerSetting{Name: "searchType", Value: "all"}); err != nil {
		t.Fatalf("insert sibling: %v", err)
	}
	// First upsert inserts the secret; second upsert updates it in place.
	if err := insts.UpsertSetting(ctx, db, id, domain.IndexerSetting{Name: "mam_id", ValueEncrypted: "enc-v1", KeyID: "k1", IsSecret: true}); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	if err := insts.UpsertSetting(ctx, db, id, domain.IndexerSetting{Name: "mam_id", ValueEncrypted: "enc-v2", KeyID: "k1", IsSecret: true}); err != nil {
		t.Fatalf("upsert update: %v", err)
	}

	got, err := insts.Settings(ctx, db, id)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	byName := make(map[string]domain.IndexerSetting, len(got))
	for _, s := range got {
		byName[s.Name] = s
	}
	if len(got) != 2 {
		t.Fatalf("settings = %d, want 2 (sibling + a single updated mam_id, no duplicate)", len(got))
	}
	if byName["mam_id"].ValueEncrypted != "enc-v2" {
		t.Errorf("mam_id ciphertext = %q, want enc-v2 (updated in place)", byName["mam_id"].ValueEncrypted)
	}
	if !byName["mam_id"].IsSecret {
		t.Error("mam_id lost its secret flag after upsert")
	}
	if byName["searchType"].Value != "all" {
		t.Errorf("sibling searchType = %q, want all (preserved)", byName["searchType"].Value)
	}
}

func TestFilePermissions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Tighten the temp dir afterwards is the daemon's job; here we point at a
	// nested data dir that Open must create 0700.
	dataDir := filepath.Join(dir, "data")
	p := filepath.Join(dataDir, "harbrr.db")

	db := openMigrated(t, p)
	ctx := context.Background()
	// Force a write so the WAL side files exist before we assert their mode.
	if _, err := db.ExecContext(ctx,
		"INSERT INTO app_meta (key, value) VALUES ('probe','1')"); err != nil {
		t.Fatalf("probe write: %v", err)
	}
	if err := db.Checkpoint(ctx); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	if got := statMode(t, dataDir); got != 0o700 {
		t.Errorf("data dir mode = %o, want 700", got)
	}
	if got := statMode(t, p); got != 0o600 {
		t.Errorf("db file mode = %o, want 600", got)
	}
	// Every side file that exists must be 0600.
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		sp := p + suffix
		if _, err := os.Stat(sp); err != nil {
			continue
		}
		if got := statMode(t, sp); got != 0o600 {
			t.Errorf("side file %s mode = %o, want 600", suffix, got)
		}
	}
}

// TestDataPersistsAcrossReopen guards against the isMemory misclassification
// regression: a real file path must persist data, never be opened in-memory.
func TestDataPersistsAcrossReopen(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "harbrr.db")
	ctx := context.Background()

	first := openMigrated(t, p)
	if _, err := first.ExecContext(ctx,
		"INSERT INTO app_meta (key, value) VALUES ('persisted','yes')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second := openMigrated(t, p) // reopen the same file
	var val string
	if err := second.QueryRowContext(ctx,
		"SELECT value FROM app_meta WHERE key='persisted'").Scan(&val); err != nil {
		t.Fatalf("read after reopen: %v", err)
	}
	if val != "yes" {
		t.Errorf("persisted value = %q, want \"yes\" (file opened in-memory?)", val)
	}
}

// statMode returns the permission bits of path.
func statMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	return fi.Mode().Perm()
}

// TestQuerierInterface is a compile-time + runtime check that *DB is usable
// through the storage seam (the type other packages depend on).
func TestQuerierInterface(t *testing.T) {
	t.Parallel()

	var q dbinterface.Querier = openMigrated(t, ":memory:")
	tx, err := q.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
}

// TestProtocolColumnDefaultsToTorrent proves migration 0005 added the protocol
// column and that an insert omitting it (as the older code paths and the
// test-only inserts do) backfills 'torrent' via the NOT NULL DEFAULT.
func TestProtocolColumnDefaultsToTorrent(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		"INSERT INTO indexer_instances (slug, definition_id, name, created_at, updated_at) VALUES (?,?,?,?,?)",
		"tl", "torrentleech", "TL", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("insert instance without protocol: %v", err)
	}

	var protocol string
	if err := db.QueryRowContext(ctx,
		"SELECT protocol FROM indexer_instances WHERE slug=?", "tl").Scan(&protocol); err != nil {
		t.Fatalf("read protocol: %v", err)
	}
	if protocol != "torrent" {
		t.Errorf("protocol = %q, want torrent", protocol)
	}

	// scanInstance must surface the column through the typed repository too.
	inst, err := database.Instances{}.GetBySlug(ctx, db, "tl")
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if inst.Protocol != "torrent" {
		t.Errorf("GetBySlug protocol = %q, want torrent", inst.Protocol)
	}
}
