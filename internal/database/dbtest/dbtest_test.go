package dbtest_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbtest"
)

// insertApp writes one row into a table that only exists post-migration, which
// doubles as proof the clone carries the schema.
func insertApp(t *testing.T, db *database.DB, name string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO apps (kind, name, base_url, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		"sonarr", name, "http://"+name, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("insert app: %v", err)
	}
}

func countApps(t *testing.T, db *database.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM apps`).Scan(&n); err != nil {
		t.Fatalf("count apps: %v", err)
	}
	return n
}

// TestClonesAreIsolated proves each call gets its own file: a write in one clone
// is invisible to another.
func TestClonesAreIsolated(t *testing.T) {
	t.Parallel()

	first, second := dbtest.OpenMigrated(t), dbtest.OpenMigrated(t)
	insertApp(t, first, "only-in-first")

	if got := countApps(t, first); got != 1 {
		t.Fatalf("first clone: got %d apps, want 1", got)
	}
	if got := countApps(t, second); got != 0 {
		t.Fatalf("second clone leaked rows: got %d apps, want 0", got)
	}
}

// TestCloneIsMigrated compares the clone's applied-migration ledger against a
// database migrated from scratch, so a stale or partial template fails loudly.
func TestCloneIsMigrated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fresh, err := database.Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("open fresh: %v", err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	if err := fresh.Migrate(ctx); err != nil {
		t.Fatalf("migrate fresh: %v", err)
	}

	want := appliedMigrations(t, fresh)
	got := appliedMigrations(t, dbtest.OpenMigrated(t))
	if len(want) == 0 {
		t.Fatal("fresh database recorded no migrations")
	}
	if len(got) != len(want) {
		t.Fatalf("clone applied %d migrations, fresh applied %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("migration %d: clone has %q, fresh has %q", i, got[i], want[i])
		}
	}
}

func appliedMigrations(t *testing.T, db *database.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT filename FROM schema_migrations ORDER BY filename`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan filename: %v", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema_migrations: %v", err)
	}
	return out
}

// BenchmarkFullMigration is the per-fixture cost this package exists to avoid.
func BenchmarkFullMigration(b *testing.B) {
	dir := b.TempDir()
	for i := 0; b.Loop(); i++ {
		_ = migrateInto(b, filepath.Join(dir, "bench-"+strconv.Itoa(i)+".db")).Close()
	}
}

// BenchmarkCloneMigrated mirrors what OpenMigrated does per call (OpenMigrated
// itself takes *testing.T, so the mechanics are repeated rather than called).
func BenchmarkCloneMigrated(b *testing.B) {
	dir := b.TempDir()
	template := filepath.Join(dir, "template.db")
	_ = migrateInto(b, template).Close()
	blob, err := os.ReadFile(template)
	if err != nil {
		b.Fatalf("read template: %v", err)
	}

	for i := 0; b.Loop(); i++ {
		clone := filepath.Join(dir, "clone-"+strconv.Itoa(i)+".db")
		if err := os.WriteFile(clone, blob, 0o600); err != nil { //nolint:gosec // G703: clone is b.TempDir() plus a counter, never user input.
			b.Fatalf("write clone: %v", err)
		}
		db, err := database.Open(clone)
		if err != nil {
			b.Fatalf("open clone: %v", err)
		}
		_ = db.Close()
	}
}

// migrateInto opens path and runs the full migration chain against it.
func migrateInto(b *testing.B, path string) *database.DB {
	b.Helper()
	db, err := database.Open(path)
	if err != nil {
		b.Fatalf("open %s: %v", path, err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		b.Fatalf("migrate %s: %v", path, err)
	}
	return db
}
