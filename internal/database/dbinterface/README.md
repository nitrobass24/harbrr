# dbinterface — the storage seam (and its dialect-portability contract)

Every repository depends on `Querier` / `Execer` / `TxQuerier`, never the concrete
driver, so the store can be swapped without touching call sites. Only **SQLite** is
wired today; **Postgres is demand-gated** — not on the alpha roadmap, built only
when a multi-instance user needs it (see `docs/plan.md` → "Beyond the alpha").

This file is the standing contract that keeps that swap a **bounded** change, plus
the ledger of exactly what a Postgres backend must handle. Keeping this seam clean
is required work **now**; implementing Postgres is not.

## The contract (enforced)

- **All placeholder-bearing SQL routes through `Rebind`.** Repositories write `?`
  placeholders and wrap the query in `q.Rebind(...)` (on the `Execer`/`TxQuerier`
  handle they already hold). `Rebind` is a no-op for SQLite and rewrites `?`→`$1,$2,…`
  for Postgres (quote-aware). This is what makes adding a backend a **one-function
  change in `Rebind`**, not a sweep of every call site.
  - Enforced by `TestNoUnboundPlaceholders` (`internal/database/rebind_guard_test.go`),
    which fails CI if any `Exec/Query/QueryRowContext` call passes a `?`-bearing SQL
    argument — an inline string literal or a bare `?`-bearing const/var — that
    bypasses `Rebind`. (Idents are matched by name; dynamically assembled SQL is not
    inspected — neither pattern exists here.)
  - Validated by `TestRebind` (`querier_test.go`): SQLite passthrough + Postgres
    numbering + quoted-`?` skipping.
- **The dialect lives on the handle.** `*DB` carries it; `BeginTx` returns a thin
  `txQuerier` (embeds `*sql.Tx`, adds `Rebind`) so transaction-scoped SQL reaches
  the same adapter.

## Postgres porting ledger — what's ready vs what a backend must add

| Area | Today (SQLite) | Postgres backend must… | Status |
|---|---|---|---|
| **Bind placeholders** | `?` | nothing — `Rebind` already emits `$N` | ✅ seam wired |
| **Upserts** | `INSERT … ON CONFLICT(col) DO UPDATE SET x = excluded.x` (`appmeta`, `sessionstore`) | nothing — Postgres (9.5+) supports the identical syntax | ✅ portable as-is |
| **Insert + new id** | `res.LastInsertId()` after `INSERT` (`Users`/`APIKeys`/`Instances`) | switch those inserts to `INSERT … RETURNING id` + `QueryRow` (lib/pq has no `LastInsertId`) | ⚠️ finite, ~4 sites |
| **Booleans** | `boolToInt` → `0/1` INTEGER columns (`enabled`, `is_secret`) | keep INTEGER columns, or map to native `BOOLEAN` in the PG migration set | ⚠️ schema choice |
| **Timestamps** | RFC3339 **TEXT** (`timeLayout`); session expiry as **REAL** Unix seconds | works as `TEXT`/`double precision`; optionally migrate to `timestamptz`/`numeric` | ⚠️ schema choice |
| **Migrations** | SQLite DDL in `migrations/*.sql`, applied verbatim (not rebound) | ship a parallel Postgres migration set the DDL mirrors (forward-only, same filenames/order) | ⚠️ separate DDL set |
| **Connection / DSN** | `modernc.org/sqlite`; `busy_timeout`/`foreign_keys`/WAL pragmas; `SetMaxOpenConns(1)` | branch `Open` on dialect: a PG driver + pool sizing (no single-conn serialization needed) | ⚠️ `Open` branch |
| **Generic CRUD** | `count(*)`, `WHERE`, `ORDER BY`, `DELETE`, parameterized reads | nothing — standard SQL | ✅ portable as-is |

Legend: ✅ already handled by the seam / standard SQL · ⚠️ a known, bounded task when
Postgres actually lands (not a rewrite of callers).

The takeaway: a future Postgres backend is the `Rebind` rewrite (done) + a PG
migration set + an `Open` dialect branch + ~4 `RETURNING id` inserts. That list is
the whole job — the repositories above it do not change.
