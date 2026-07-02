// Package dbinterface defines the storage interface harbrr depends on, so a
// backend can be swapped without touching call sites. SQLite is the only
// implementation for now; Postgres is intentionally deferred (see AGENTS.md and
// docs/architecture.md, invariant 5). Do not implement Postgres yet.
package dbinterface
