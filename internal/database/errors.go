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

// sqliteConstraintForeignKey is SQLITE_CONSTRAINT_FOREIGNKEY, the extended result
// code SQLITE_CONSTRAINT(19) | (3<<8) = 768 + 19 = 787, returned under
// foreign_keys=ON when a write references a non-existent parent row. Verified as
// the value modernc.org/sqlite's *sqlite.Error.Code() reports for a real FK
// violation (the same driver whose Code() is 2067 for UNIQUE).
const sqliteConstraintForeignKey = 787

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

// IsForeignKeyViolation reports whether err is a SQLite FOREIGN KEY-constraint
// violation, so a caller can map a write that references a non-existent row (e.g.
// a dangling proxy_id/solver_id) to invalid input rather than an opaque 500. It
// unwraps the error chain to the driver error.
func IsForeignKeyViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code() == sqliteConstraintForeignKey
	}
	return false
}
