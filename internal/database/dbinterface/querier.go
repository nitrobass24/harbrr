package dbinterface

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// Execer is the read/write operations shared by the database handle and a
// transaction. Repository methods take an Execer, so the same method runs
// standalone (passed the *DB) or inside a transaction (passed the TxQuerier) —
// which is how the registry inserts an instance and its settings atomically.
//
// Rebind is part of this seam (not a free function) so every call site has it on
// the handle it already holds: repositories route placeholder-bearing SQL through
// q.Rebind(...), which keeps adding a second backend a one-function change in
// Rebind rather than a sweep of every query.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	// Rebind adapts a query's `?` bind placeholders to the active dialect's style.
	// It is a no-op for SQLite (which uses `?`) and rewrites to `$1,$2,…` for
	// Postgres. Always pass SQL that contains placeholders through it.
	Rebind(query string) string
}

// Querier is the read/write seam every repository depends on, so the concrete
// store can be swapped without touching call sites. *database.DB satisfies it
// over SQLite today; a Postgres implementation can satisfy it later without
// reworking callers (AGENTS.md — interface clean, Postgres demand-gated).
type Querier interface {
	Execer
	BeginTx(ctx context.Context, opts *sql.TxOptions) (TxQuerier, error)
}

// TxQuerier is an Execer scoped to a transaction. The SQLite store wraps *sql.Tx
// in a thin type that carries the dialect (so Rebind is available on the tx
// handle too) and otherwise promotes *sql.Tx's methods unchanged.
type TxQuerier interface {
	Execer
	Commit() error
	Rollback() error
}

// Dialect identifies the active SQL backend.
type Dialect string

const (
	// DialectSQLite is the only dialect harbrr wires a backend for today.
	DialectSQLite Dialect = "sqlite"
	// DialectPostgres is recognized by Rebind so the placeholder seam is provably
	// ready, but NO Postgres backend exists — it is demand-gated (docs/plan.md
	// "Beyond the alpha"). Adding one means wiring a driver + dialect detection in
	// Open, not rewriting repository SQL.
	DialectPostgres Dialect = "postgres"
)

// Rebind adapts a query's `?` bind placeholders to the dialect. SQLite (and any
// unknown dialect) uses the `?` placeholders repositories already write, so it is
// a passthrough; Postgres uses positional `$1,$2,…`. Placeholders inside single-
// or double-quoted string literals are left untouched. (Escaped quotes within SQL
// string literals are not modeled; harbrr's queries contain none, and the day one
// is needed the tokenizer grows here, still a one-place change.)
func Rebind(dialect Dialect, query string) string {
	if dialect != DialectPostgres {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	var inSingle, inDouble bool
	for i := 0; i < len(query); i++ {
		switch c := query[i]; {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
			b.WriteByte(c)
		case c == '"' && !inSingle:
			inDouble = !inDouble
			b.WriteByte(c)
		case c == '?' && !inSingle && !inDouble:
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
