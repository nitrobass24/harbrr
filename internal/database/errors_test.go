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

// TestIsForeignKeyViolation pins IsForeignKeyViolation to SQLITE_CONSTRAINT_FOREIGNKEY
// (787), the code modernc.org/sqlite reports when a write under foreign_keys=ON
// references a non-existent parent row (e.g. a dangling proxy_id/solver_id). A
// UNIQUE collision raises a different code (2067) and must NOT match, so the two
// classifiers stay distinct.
func TestIsForeignKeyViolation(t *testing.T) {
	t.Parallel()

	db := openMigrated(t, filepath.Join(t.TempDir(), "harbrr.db"))
	ctx := context.Background()

	// A dangling instance_id on indexer_settings → FK violation (code 787).
	_, fkErr := db.ExecContext(ctx,
		"INSERT INTO indexer_settings (instance_id, name, value, is_secret) VALUES (?, ?, ?, 0)",
		9999, "k", "v")
	if fkErr == nil {
		t.Fatal("insert with dangling instance_id succeeded, want a FOREIGN KEY violation")
	}

	// A UNIQUE(slug) collision → code 2067, a different code that must NOT match.
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

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"FK violation (code 787)", fkErr, true},
		{"UNIQUE collision (code 2067)", uniqueErr, false},
		{"plain non-driver error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := database.IsForeignKeyViolation(tt.err); got != tt.want {
				t.Errorf("IsForeignKeyViolation(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
