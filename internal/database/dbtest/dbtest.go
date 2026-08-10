// Package dbtest hands tests a fully-migrated database without paying for the
// migration chain every time. The chain is run exactly once per test binary into
// a template file; each OpenMigrated call copies that file and opens the copy.
//
// Test-only: nothing outside *_test.go should import it.
package dbtest

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
)

var (
	templateOnce sync.Once
	templatePath string
	errTemplate  error
)

// buildTemplate opens a scratch database, runs the migration chain and closes it
// so the WAL is checkpointed back into the main file — the file is then a
// complete, self-contained migrated database that can simply be copied.
func buildTemplate() {
	dir, err := os.MkdirTemp("", "harbrr-dbtest-*")
	if err != nil {
		errTemplate = err
		return
	}
	path := filepath.Join(dir, "template.db")

	db, err := database.Open(path)
	if err != nil {
		errTemplate = err
		return
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		errTemplate = err
		return
	}
	if err := db.Checkpoint(ctx); err != nil {
		errTemplate = err
		return
	}
	templatePath = path
}

// OpenMigrated returns a fully-migrated *database.DB backed by a private file in
// t.TempDir(), cloned from a template migrated once per test binary. It is opened
// through database.Open, so the single-connection semantics every caller depends
// on are identical to production. The handle is closed via t.Cleanup.
func OpenMigrated(t *testing.T) *database.DB {
	t.Helper()

	templateOnce.Do(buildTemplate)
	if errTemplate != nil {
		t.Fatalf("dbtest: build template: %v", errTemplate)
	}

	// The template is read-only after the sync.Once, so parallel clones are safe.
	blob, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("dbtest: read template: %v", err)
	}
	clonePath := filepath.Join(t.TempDir(), "harbrr.db")
	if err := os.WriteFile(clonePath, blob, 0o600); err != nil { //nolint:gosec // G703: clonePath is t.TempDir() plus a literal, never user input.
		t.Fatalf("dbtest: write clone: %v", err)
	}

	db, err := database.Open(clonePath)
	if err != nil {
		t.Fatalf("dbtest: open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
