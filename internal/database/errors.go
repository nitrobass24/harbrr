package database

import (
	"errors"

	"modernc.org/sqlite"
)

// ErrNotFound is returned by repository lookups when no row matches. Callers
// distinguish it with errors.Is to map to a 404 (or "create" path) rather than a
// 500.
var ErrNotFound = errors.New("database: not found")

// sqliteConstraintUnique is SQLITE_CONSTRAINT_UNIQUE (a stable SQLite result
// code), returned when a UNIQUE constraint is violated. A PRIMARY KEY conflict
// reports a different code (SQLITE_CONSTRAINT_PRIMARYKEY, 1555), so this does not
// cover it.
const sqliteConstraintUnique = 2067

// IsUniqueViolation reports whether err is a SQLite UNIQUE-constraint violation,
// so a caller can map a lost insert race to a conflict even when a pre-check
// passed (TOCTOU). It unwraps the error chain to the driver error.
func IsUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code() == sqliteConstraintUnique
	}
	return false
}
