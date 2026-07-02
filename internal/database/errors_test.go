package database_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
)

// TestIsUniqueViolation pins what IsUniqueViolation does — and, deliberately, what
// it does not. It matches SQLITE_CONSTRAINT_UNIQUE (2067), which is what every
// caller's natural-key UNIQUE(...) constraint raises on a lost insert race. A
// PRIMARY KEY conflict raises a *different* code (SQLITE_CONSTRAINT_PRIMARYKEY,
// 1555) and is intentionally NOT matched (see errors.go); this test makes that
// 1555-vs-2067 gap explicit so a future change to the helper is a conscious one.
func TestIsUniqueViolation(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()

	// A UNIQUE(slug) collision on indexer_instances → code 2067 → matched.
	insertInstance := func() error {
		_, err := db.ExecContext(ctx,
			"INSERT INTO indexer_instances (slug, definition_id, name, created_at, updated_at) VALUES (?,?,?,?,?)",
			"dup", "torrentleech", "TL", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
		return err
	}
	if err := insertInstance(); err != nil {
		t.Fatalf("first instance insert: %v", err)
	}
	uniqueErr := insertInstance()
	if uniqueErr == nil {
		t.Fatal("second insert with the same slug succeeded, want a UNIQUE violation")
	}
	if !database.IsUniqueViolation(uniqueErr) {
		t.Errorf("IsUniqueViolation(UNIQUE collision) = false, want true: %v", uniqueErr)
	}

	// A PRIMARY KEY(app_meta.key) collision → code 1555 → NOT matched (the gap).
	insertMeta := func() error {
		_, err := db.ExecContext(ctx,
			"INSERT INTO app_meta (key, value) VALUES ('pk', '1')")
		return err
	}
	if err := insertMeta(); err != nil {
		t.Fatalf("first app_meta insert: %v", err)
	}
	pkErr := insertMeta()
	if pkErr == nil {
		t.Fatal("second insert with the same key succeeded, want a PRIMARY KEY violation")
	}
	if database.IsUniqueViolation(pkErr) {
		t.Errorf("IsUniqueViolation(PRIMARY KEY collision) = true; the 1555 gap is documented as unmatched: %v", pkErr)
	}

	// Non-driver errors never match.
	if database.IsUniqueViolation(errors.New("boom")) {
		t.Error("IsUniqueViolation(plain error) = true, want false")
	}
	if database.IsUniqueViolation(nil) {
		t.Error("IsUniqueViolation(nil) = true, want false")
	}
}
