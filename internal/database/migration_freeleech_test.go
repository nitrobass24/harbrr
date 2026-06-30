package database_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	_ "github.com/autobrr/harbrr/internal/database" // registers the "sqlite" driver
)

// TestMigration0008PreservesLedgerAndFixesKindCheck applies migrations 0001–0007, seeds a
// connection + a per-indexer ledger row, then applies 0008 directly. It proves the table
// rebuild (a) does NOT cascade-wipe app_connection_indexers despite foreign_keys=ON, (b)
// backfills freeleech_mode='honor', and (c) drops the kind CHECK so a lidarr row (which
// 0003's CHECK rejected — #85) now inserts. A bare DROP of app_connections would fail (a).
func TestMigration0008PreservesLedgerAndFixesKindCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dsn := "file:" + filepath.Join(t.TempDir(), "m.db") + "?_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	applyMigrationsBefore(ctx, t, db, "0008")

	// Seed a connection + an instance + a ledger row linking them.
	const ts = "2026-01-01T00:00:00Z"
	exec(ctx, t, db, `INSERT INTO indexer_instances (id, slug, definition_id, name, created_at, updated_at)
		VALUES (1, 'tt', 'testtracker', 'TT', ?, ?)`, ts, ts)
	exec(ctx, t, db, `INSERT INTO app_connections
		(id, name, kind, base_url, api_key_encrypted, harbrr_url, harbrr_api_key_encrypted, key_id, created_at, updated_at)
		VALUES (1, 'Sonarr', 'sonarr', 'http://s:8989', 'enc-app', 'http://h:8787', 'enc-harbrr', 'k1', ?, ?)`, ts, ts)
	// A pre-existing qui connection must backfill to 'bypass', not 'honor' (it drives cross-seed).
	exec(ctx, t, db, `INSERT INTO app_connections
		(id, name, kind, base_url, api_key_encrypted, harbrr_url, harbrr_api_key_encrypted, key_id, created_at, updated_at)
		VALUES (2, 'qui', 'qui', 'http://q:7476', 'enc-app', 'http://h:8787', 'enc-harbrr', 'k1', ?, ?)`, ts, ts)
	exec(ctx, t, db, `INSERT INTO app_connection_indexers (id, connection_id, instance_id, remote_id)
		VALUES (1, 1, 1, 'remote-7')`)

	// Apply the migration under test.
	exec(ctx, t, db, readMigration(t, "0008_app_connections_freeleech.sql"))

	// (a) ledger row survived the parent rebuild.
	var connID, instID int64
	var remoteID string
	if err := db.QueryRowContext(ctx,
		`SELECT connection_id, instance_id, remote_id FROM app_connection_indexers WHERE id = 1`).
		Scan(&connID, &instID, &remoteID); err != nil {
		t.Fatalf("ledger row missing after 0008 (cascade wiped it?): %v", err)
	}
	if connID != 1 || instID != 1 || remoteID != "remote-7" {
		t.Errorf("ledger row = (conn %d, inst %d, remote %q), want (1, 1, remote-7)", connID, instID, remoteID)
	}

	// (b) existing rows backfilled by kind: the *arr gets honor, the qui row gets bypass.
	for _, tc := range []struct {
		id   int
		want string
	}{{1, "honor"}, {2, "bypass"}} {
		var mode string
		if err := db.QueryRowContext(ctx, `SELECT freeleech_mode FROM app_connections WHERE id = ?`, tc.id).Scan(&mode); err != nil {
			t.Fatalf("read freeleech_mode id=%d: %v", tc.id, err)
		}
		if mode != tc.want {
			t.Errorf("freeleech_mode id=%d = %q, want %q", tc.id, mode, tc.want)
		}
	}

	// (c) #85: a lidarr connection (rejected by 0003's kind CHECK) now inserts.
	if _, err := db.ExecContext(ctx, `INSERT INTO app_connections
		(name, kind, base_url, api_key_encrypted, harbrr_url, harbrr_api_key_encrypted, key_id, created_at, updated_at)
		VALUES ('Lidarr', 'lidarr', 'http://l:8686', 'enc', 'http://h:8787', 'enc', 'k1', ?, ?)`, ts, ts); err != nil {
		t.Fatalf("lidarr insert still fails (#85 not fixed): %v", err)
	}

	// the new column still enforces its own enum.
	if _, err := db.ExecContext(ctx, `INSERT INTO app_connections
		(name, kind, base_url, api_key_encrypted, harbrr_url, harbrr_api_key_encrypted, key_id, freeleech_mode, created_at, updated_at)
		VALUES ('Bad', 'sonarr', 'http://b', 'e', 'http://h', 'e', 'k', 'nonsense', ?, ?)`, ts, ts); err == nil {
		t.Error("freeleech_mode CHECK did not reject an invalid value")
	}
}

// applyMigrationsBefore runs every migrations/*.sql whose name sorts before stopAt.
func applyMigrationsBefore(ctx context.Context, t *testing.T, db *sql.DB, stopAt string) {
	t.Helper()
	files, err := filepath.Glob("migrations/0*.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		if filepath.Base(f) >= stopAt {
			continue
		}
		exec(ctx, t, db, readFile(t, f))
	}
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	return readFile(t, filepath.Join("migrations", name))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test-controlled path under the package dir
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func exec(ctx context.Context, t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("exec %.60s: %v", strings.TrimSpace(query), err)
	}
}
