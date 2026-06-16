# Phase 8b — Complete the management API (close the control-plane / data-plane gaps)

## Problem

harbrr serves two HTTP contracts on separate trees (architecture invariant #3): a JSON
**management API** (`/api/*`, documented in `internal/web/swagger/openapi.yaml`, browsable
at `/api/docs`) and the \*arr-facing **Torznab XML** contract (`/api/v2.0/indexers/*`, a
standardized external contract, not in the spec).

A complete gap analysis (a 6-area audit of 106 capabilities surfaced **64 gaps**) found that
today's *documented* API is a **control plane, not a complete API**:

- The operations that deliver value — **search, capabilities, grab** — exist **only** on the
  Torznab tree (XML). There is no JSON way to search or read an indexer's capabilities.
- Several **discovery** needs have no JSON endpoint: a definition's **settings-field schema**
  (required to render an add-indexer form) and a configured indexer's caps/categories.
- A chunk of indexer **config** (`proxy_*`, `timeout`, `solver_*`) is settable-but-undocumented,
  and the spec documents **OIDC endpoints that are stubbed with no handlers** (so `/api/docs`
  shows endpoints that don't work).

So an operator — or a future web UI, which is by design an API client (harbrr is headless +
API-first) — cannot search, see capabilities, build an add-indexer form, or change the admin
password through the API they're shown. This phase closes that gap in **one PR**.

## Verified gaps (in scope)

Each confirmed against the code by a verification pass; the seams + per-endpoint contracts are in
`docs/prompts/phase8b.md`.

1. **No JSON search** — search works only as Torznab XML (`/api/v2.0/indexers/{slug}/api?t=search|
   tvsearch|movie|music|book`). No `GET /api/indexers/{slug}/search`. The engine call
   (`registry.Indexer(...).Search`) and the param mapping (`internal/web/torznab/query.go
   buildQuery`) already exist and are reusable; the `normalizer.Release` struct already has JSON
   tags.
2. **No JSON capabilities** — an indexer's caps/categories are emitted only as Torznab XML
   (`t=caps`). No `GET /api/indexers/{slug}/capabilities`. `mapper.Capabilities` is reachable via
   `registry.Indexer(...).Capabilities()`.
3. **No definition detail** — `GET /api/definitions` returns only id/name/description/type/language;
   a client cannot get a definition's **settings-field schema** (`loader.Definition.Settings` +
   `IsSecret()`) or its caps. No `GET /api/definitions/{id}`.
4. **No change-password** — the DB method `UpdatePassword` (`internal/database/users.go`) exists but
   has no service method, handler, or route. The admin password cannot be rotated via the API.
5. **Undocumented config + loose/wrong spec** — `proxy_type`/`proxy_url`, `timeout`,
   `solver_type`/`flaresolverr_url`, and the reserved-secret settings are settable via the settings
   map but absent from the spec (settings typed as `additionalProperties: string`); error responses
   are `{error: string}` with no machine-readable code; and the spec documents **OIDC endpoints with
   no handlers**.

## Explicitly out of scope (with reasons)

- **`GET /api/info` / `/api/config`** (read-only server facts) — **defer**: needs cross-layer config
  threading for low marginal value (`/healthz` + `/api/openapi.yaml` already expose the non-secret
  facts). Revisit if an observability API is built.
- **Drift-test schema validation** (vs the current route+method parity) — **defer**: route parity is
  sufficient and reliable; only worth it if client-SDK codegen is added. Document the limitation; add
  a small integration test for the new error `code` instead.
- **Runtime server reconfig** (host/port/base-path/log-level/TLS/auth-mode) — **out**: deployment
  config, restart-only by design.
- **Key-rotation API** — **out**: offline/CLI by design (`harbrr rotate-key`, daemon stopped).
- **Logs API, session listing/revocation, multi-user, password reset, global (non-indexer) health** —
  **out**: roadmap / intentional (single-admin, one-way hash).
- **OIDC implementation** — **out** (Phase 9/10); phase8b only resolves the spec/handler **mismatch**
  (mark not-implemented or remove from the spec until then).

## Hard constraints

- **Redaction is absolute.** A JSON search response must **never** leak a passkey-bearing download
  link: for a resolver-needing indexer, the `link` must be the opaque `/dl` proxy URL (reuse
  `dltoken`/`dlRewriter`), exactly as the Torznab feed does. Assert with a test.
- **One engine, two contracts.** The JSON endpoints **reuse** the registry/engine
  (`registry.Indexer(...).Search/.Capabilities`) and the shared query mapping; they do **not** fork
  the Torznab handler or invent a parallel release type. The JSON search must return the **same
  releases** the Torznab search returns for the same query (parity).
- **The OpenAPI drift test stays green** — every new route is documented in `openapi.yaml` in the
  same commit.
- Conventional commits, **no AI attribution**, typed DTOs, gofumpt-clean, SQLite-only.

## Acceptance

- `GET /api/indexers/{slug}/search`, `GET /api/indexers/{slug}/capabilities`,
  `GET /api/definitions/{id}`, and `POST /api/auth/change-password` are implemented, documented in
  the spec, and drift-test-green.
- The undocumented config settings are documented (named optional settings with enums), the error
  schema gains a machine-readable `code`, and the OIDC-stub mismatch is resolved.
- A redaction test proves no passkey/download-link/secret appears in a JSON search response.
- The JSON search returns releases identical to the Torznab search for the same query (parity test).
- `make precommit` + `make build` green; PR ≤150 files; **PAUSE before merge**.

Implementation prompt: **`docs/prompts/phase8b.md`**.
