package dbinterface

import (
	"context"
	"database/sql"
)

// Querier is the read/write seam every repository depends on, so the concrete
// store can be swapped without touching call sites. *database.DB satisfies it
// over SQLite today; a Postgres implementation can satisfy it later without
// reworking callers (AGENTS.md — interface clean, Postgres deferred). It stays
// small (4 methods): the four operations a repository actually needs.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	BeginTx(ctx context.Context, opts *sql.TxOptions) (TxQuerier, error)
}

// TxQuerier is a Querier scoped to a transaction. *sql.Tx satisfies it directly
// (it already has the three query methods plus Commit/Rollback), so the SQLite
// store wraps a transaction with zero glue.
type TxQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	Commit() error
	Rollback() error
}

// Dialect identifies the active SQL backend. Only SQLite is implemented;
// Postgres is deliberately deferred (AGENTS.md), but the type stays so dialect
// branching has a home the day a second backend lands.
type Dialect string

// DialectSQLite is the only dialect harbrr implements for now.
const DialectSQLite Dialect = "sqlite"

// Rebind adapts a query's bind-placeholder style to the dialect. SQLite uses the
// `?` placeholders repositories already write, so this is a passthrough; the
// `?`→`$N` rewrite for Postgres lands with the Postgres backend. Keeping the call
// site (repositories route SQL through Rebind) means adding Postgres later is a
// one-function change, not a sweep.
func Rebind(_ Dialect, query string) string {
	return query
}
