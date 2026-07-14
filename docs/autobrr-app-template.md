# autobrr-family app template target

This document is the target shape for harbrr's architecture refactor series and
the eventual reusable autobrr-family app template. It is not a second roadmap:
roadmap work lives in GitHub issues. This is the durable contract those issues
move toward.

The template's job is to provide a tested application spine for single-binary,
self-hosted autobrr-family apps: command entry, composition root, lifecycle,
configuration, logging, secrets, persistence, authentication, HTTP serving,
OpenAPI, embedded web UI, and CI/security gates. It must not carry product
domain behavior from harbrr, autobrr, qui, or any future app.

## Goals

- Give new apps a working, boring base before domain features are added.
- Give maintainers and agents local rules for where new code belongs.
- Keep startup, shutdown, secret handling, generated contracts, and CI behavior
  consistent across the family.
- Make architectural drift reviewable by comparing code to this document.

## Non-goals

- No tracker, torrent, indexer, announce, download-client, license, or premium
  feature code in the template.
- No dependency-injection container, reflection registry, or framework layer.
- No placeholder packages for future behavior. A package exists when behavior
  exists.
- No Postgres baseline. SQLite is the default; keep storage seams clean enough
  that another backend can be added by a product that needs it.

## Target layout

```text
cmd/<app>/
  main.go          # execute the root command only
  root.go          # command registration
  serve.go         # flags -> config/logger -> app.New -> app.Run
  version.go

internal/
  app/             # composition root: dependency graph and lifecycle
    app.go
    deps.go
    lifecycle.go
    adapters.go

  server/          # HTTP mount only: chi root router and graceful HTTP server
  web/
    api/           # management API handlers, middleware, encoding
    swagger/       # hand-authored OpenAPI spec, Swagger UI, drift tests
    ui/            # embedded SPA handler

  config/          # typed config, defaults, load, validation, path creation
  logger/          # zerolog setup and logging helpers
  http/            # outbound/inbound HTTP helpers, redaction, error shaping
  secrets/         # password hashing, token hashing, at-rest encryption, redaction
  auth/            # setup/login/session/API-key/CSRF service
  database/        # SQLite open/migrate/health/session store/repos
    dbinterface/   # Querier, TxQuerier, Rebind, dialect helpers
  domain/          # shared domain types and sentinel errors
  version/         # build version, commit, date

web/
  src/
    components/
    hooks/
    lib/
      api.ts       # thin wrapper around generated OpenAPI client
      query.ts     # QueryClient defaults and typed query keys
    routes/
    types/
      api.gen.ts   # generated from OpenAPI and committed
```

`pkg/` is empty by default. Add to `pkg/` only when the code is intentionally
usable by other modules. Most code starts under `internal/`.

## Composition root

`internal/app` is the only composition root. It owns:

- construction order;
- dependency graph fields;
- process lifecycle;
- background worker/reaper startup and shutdown;
- cross-package adapter wiring;
- full-daemon `Handler()` for `httptest`;
- `Run(ctx)` for listener-backed serving.

`cmd/<app>` parses commands and flags. It must not own daemon wiring. `serve.go`
should load config, build the logger, create a signal context, call `app.New`,
then call `app.Run`.

`internal/server` is not the composition root. It mounts HTTP handlers and owns
HTTP server behavior: base path, route tree, timeouts, request logging, embedded
UI fallback, OpenAPI/Swagger routes, and graceful HTTP shutdown.

## Lifecycle

Lifecycle code should be explicit and testable:

- Startup order must be represented by code structure, not only comments.
- Shutdown order must be covered by tests when reordering can lose data.
- Background loops should share a small skeleton only when their lifecycle is
  actually the same.
- Runtime-retunable intervals must re-read their source each cycle.
- Cycles in the dependency graph are allowed only when explicit and documented
  at the composition root.

## Persistence

Services should depend on `dbinterface.Querier` when they only need query/exec
behavior. Use concrete `*database.DB` only at the composition root or when a
caller genuinely needs driver-specific lifecycle methods.

Repository SQL should route placeholder-bearing queries through `Rebind`.
SQLite-specific behavior belongs in `internal/database`, not in services or HTTP
handlers.

## Security

Security behavior is part of the template baseline:

- login passwords are hashed, not encrypted;
- bearer tokens are randomly generated and stored as hashes;
- replayable credentials are encrypted at rest;
- session cookies are HttpOnly and CSRF-protected for cookie-auth mutations;
- auth-disabled mode requires an allowlist;
- secrets are redacted in logs, traces, errors, URLs, headers, and API responses;
- data directories and database files are owner-only.

Any new secret-bearing table or API surface must update rotation/redaction tests
in the same PR.

## HTTP and API contracts

Keep machine contracts separate from product contracts:

- OpenAPI is the management API contract.
- App-specific wire protocols, feeds, or compatibility formats live in their
  own packages and are not modeled as management API shortcuts.

Every added or moved management route updates `internal/web/swagger/openapi.yaml`
and the OpenAPI drift tests. The served OpenAPI spec is the source for generated
frontend types.

## Frontend

The family frontend baseline is React, Vite, TypeScript, TanStack Router,
TanStack Query, Radix-style UI primitives, Tailwind, Vitest, and ESLint.

Rules:

- API types are generated from OpenAPI and committed.
- Frontend API calls go through one client wrapper.
- QueryClient defaults live in `web/src/lib/query.ts`.
- Query keys come from a typed query-key registry; components do not inline key
  arrays.
- Generated route and API artifacts have CI drift gates.
- Secret sentinels such as `<redacted>` round-trip without exposing stored
  values.

## Documentation baseline

Every app using this shape should have:

- `CONTEXT.md` for ubiquitous project language;
- `docs/architecture.md` for product-specific boundaries and invariants;
- ADRs for load-bearing architecture decisions;
- `docs/security.md` for credential classes and redaction policy;
- issue-scoped refactor plans for structural work.

Docs must distinguish shipped behavior from planned behavior. Planned work lives
in GitHub issues and must not be described as shipped.
