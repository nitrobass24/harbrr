package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no cgo — required for the cross-build gate)

	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// dirPerm and filePerm are the §9 on-disk permissions: the data dir is owner-only
// (0700) and the database plus every SQLite side file is owner-rw (0600).
const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

// sideSuffixes are the SQLite side files created alongside the database. In WAL
// mode SQLite writes -wal and -shm; -journal appears in the rollback-journal
// mode. secureDBFiles chmods whichever exist so none is left world-readable
// regardless of the process umask (the 0700 dir is defense-in-depth on top).
var sideSuffixes = []string{"-wal", "-shm", "-journal"}

// DB is harbrr's SQLite datastore. It wraps *sql.DB and satisfies
// dbinterface.Querier, so repositories depend on the interface, never the
// concrete driver. A single underlying connection (SetMaxOpenConns(1)) fully
// serializes access, which removes SQLITE_BUSY and nested-transaction hazards for
// a single-user daemon; the writer/reader pool split is a later optimization.
//
// CALLER CONTRACT: because there is exactly one connection, code that holds a
// transaction (from BeginTx) MUST issue every query through that TxQuerier — never
// through the *DB — until it commits/rolls back. A *DB query issued while a
// transaction is open would wait forever for the connection the transaction holds.
type DB struct {
	sql     *sql.DB
	dialect dbinterface.Dialect
	path    string
}

// Open opens (creating if absent) the SQLite database at path and applies the
// WAL / busy-timeout / foreign-keys pragmas via the DSN. Pass ":memory:" for an
// ephemeral test database. The data directory is created 0700; file permissions
// are tightened by Migrate via secureDBFiles once the side files exist.
func Open(path string) (*DB, error) {
	if err := ensureDataDir(path); err != nil {
		return nil, err
	}

	sqlDB, err := sql.Open("sqlite", dsnFor(path))
	if err != nil {
		return nil, fmt.Errorf("database: open %q: %w", path, err)
	}
	// One connection serializes all access (see DB doc); never expire it so an
	// in-memory database is not silently dropped between queries.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.PingContext(context.Background()); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("database: ping %q: %w", path, err)
	}

	return &DB{sql: sqlDB, dialect: dbinterface.DialectSQLite, path: path}, nil
}

// ensureDataDir creates the database's parent directory (0700) and tightens its
// mode even when it already existed — MkdirAll leaves an existing dir's mode
// untouched, so a Docker volume or a prior looser run is still corrected to the
// owner-only policy. It is a no-op for an in-memory database.
func ensureDataDir(path string) error {
	if isMemory(path) {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("database: create data dir %q: %w", dir, err)
	}
	if err := os.Chmod(dir, dirPerm); err != nil {
		return fmt.Errorf("database: secure data dir %q: %w", dir, err)
	}
	return nil
}

// dsnFor builds the modernc DSN. modernc applies each _pragma query parameter as
// a "PRAGMA ..." on every new connection (verified against the pinned version);
// WAL and synchronous are omitted for in-memory databases where they do not
// apply, but foreign_keys and busy_timeout always hold.
func dsnFor(path string) string {
	pragmas := make([]string, 0, 4)
	pragmas = append(pragmas, "_pragma=busy_timeout(5000)", "_pragma=foreign_keys(ON)")
	if isMemory(path) {
		return "file::memory:?" + strings.Join(pragmas, "&")
	}
	pragmas = append(pragmas, "_pragma=journal_mode(WAL)", "_pragma=synchronous(NORMAL)")
	// filepath.ToSlash keeps the file: URI well-formed on Windows (a cross-build
	// target), where native paths use backslashes.
	return "file:" + filepath.ToSlash(path) + "?" + strings.Join(pragmas, "&")
}

// isMemory reports whether path requests an in-memory database. Only the exact
// ":memory:" sentinel qualifies — a substring match would misclassify a real file
// path that happened to contain ":memory:" and silently discard its data.
func isMemory(path string) bool {
	return path == ":memory:"
}

// secureDBFiles tightens the database file and any existing SQLite side files to
// 0600 (no-op for an in-memory database). It is idempotent and safe to call
// repeatedly: Migrate calls it once the WAL side files exist, and Checkpoint
// re-calls it so any side file created since is re-secured. Side files that do not
// (yet) exist are skipped.
func (db *DB) secureDBFiles() error {
	if isMemory(db.path) {
		return nil
	}
	paths := append([]string{db.path}, sideFiles(db.path)...)
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // a side file that does not exist needs no chmod
			}
			return fmt.Errorf("database: stat %q: %w", p, err) // surface permission/IO errors
		}
		if err := os.Chmod(p, filePerm); err != nil {
			return fmt.Errorf("database: chmod %q: %w", p, err)
		}
	}
	return nil
}

// sideFiles returns the candidate SQLite side-file paths for the database.
func sideFiles(path string) []string {
	out := make([]string, 0, len(sideSuffixes))
	for _, s := range sideSuffixes {
		out = append(out, path+s)
	}
	return out
}

// ExecContext runs a statement against the database.
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := db.sql.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("database: exec: %w", err)
	}
	return res, nil
}

// QueryContext runs a query returning rows.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("database: query: %w", err)
	}
	return rows, nil
}

// QueryRowContext runs a query returning a single row.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.sql.QueryRowContext(ctx, query, args...)
}

// Rebind adapts a query's `?` placeholders to the active dialect (a no-op for
// SQLite). Repositories route placeholder-bearing SQL through it so a second
// backend stays a one-function change — see dbinterface.Rebind.
func (db *DB) Rebind(query string) string { return dbinterface.Rebind(db.dialect, query) }

// txQuerier scopes the Execer seam to a transaction. It embeds *sql.Tx — so
// ExecContext/QueryContext/QueryRowContext/Commit/Rollback are promoted unchanged,
// preserving the bare-*sql.Tx behavior from before this wrapper (driver errors are
// returned without *DB's "database: …" prefix; repositories add their own context
// on both paths) — and adds Rebind by carrying the dialect, so transaction-scoped
// SQL reaches the same placeholder adapter as *DB.
type txQuerier struct {
	*sql.Tx
	dialect dbinterface.Dialect
}

// Rebind adapts a query's `?` placeholders to the dialect carried by the tx.
func (t txQuerier) Rebind(query string) string { return dbinterface.Rebind(t.dialect, query) }

// Compile-time proof the tx wrapper still satisfies the transaction seam.
var _ dbinterface.TxQuerier = txQuerier{}

// BeginTx starts a transaction, returning it wrapped so Rebind is available on the
// tx handle (the wrapper embeds *sql.Tx, so every other method is unchanged). The
// caller MUST route every query through the returned TxQuerier (not the *DB) until
// commit/rollback — see the DB caller contract; a *DB query while the transaction
// is open would deadlock on the single connection.
func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (dbinterface.TxQuerier, error) {
	tx, err := db.sql.BeginTx(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("database: begin tx: %w", err)
	}
	return txQuerier{Tx: tx, dialect: db.dialect}, nil
}

// Dialect reports the active backend (always SQLite for now).
func (db *DB) Dialect() dbinterface.Dialect { return db.dialect }

// Checkpoint flushes the WAL into the main database (TRUNCATE) and re-applies file
// permissions so any side file created since Open is also owner-only. It is a
// no-op (beyond re-securing) for an in-memory database.
func (db *DB) Checkpoint(ctx context.Context) error {
	if !isMemory(db.path) {
		if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return fmt.Errorf("database: wal checkpoint: %w", err)
		}
	}
	return db.secureDBFiles()
}

// Close closes the underlying database handle.
func (db *DB) Close() error {
	if err := db.sql.Close(); err != nil {
		return fmt.Errorf("database: close: %w", err)
	}
	return nil
}
