# Phase 4 implementation prompt — Daemon foundation

Paste the block below into an `ultracode` session to implement Phase 4. It begins in **plan mode**:
the agent must plan the *entire* work stream and get the plan approved before writing any code.

---

ultracode — Implement **Phase 4 (Daemon foundation)** from `docs/plan.md` as ONE reviewable PR.
**Begin in PLAN MODE — do STEP 0 before anything else.**

## STEP 0 — PLAN MODE FIRST (mandatory)

Enter plan mode immediately. Do **not** create a branch, write code, or edit files until the plan is
approved. Produce ONE complete implementation plan for the **entire Phase 4 work stream** (all six
work-list items end to end, not just the first), then present it with ExitPlanMode for my approval and
wait. Pressure-test the plan with a validator/architect agent (and revise) before presenting it.

The plan must cover:
- **Architecture decisions to confirm** (call these out explicitly so I can approve/redirect):
  SQLite driver (default: pure-Go `modernc.org/sqlite` — see hard rules), migration mechanism
  (embedded SQL + a small runner vs a library), session library (default: `alexedwards/scs/v2` per
  qui), HTTP router (default: `chi`, per `ideas.md`), the secrets-store public API shape, how the
  registry implements the existing `web/torznab` `Provider`, how much of the management API to build
  now vs defer, OIDC (stub vs defer), and whether Docker stays in this PR or splits.
- **Package/file layout** — every package and file to create or modify (`internal/database`,
  `internal/database/dbinterface`, `internal/secrets`, the indexer-registry package, the management-API
  handlers, `internal/web/swagger/openapi.yaml`, `cmd/harbrr/serve.go`, migrations dir, Dockerfile),
  with each file's responsibility.
- **The DB schema + migrations** (tables, the `*_encrypted` columns, the user/api-key/indexer-instance
  tables) reflecting the `docs/ideas.md` §9 model.
- **Test strategy per item** and how each Success Criterion maps to a specific test/oracle.
- **Commit/box sequencing** — which `docs/plan.md` Phase 4 box each commit ticks.
- **Risks + mitigations** (cgo/cross-build, key-loss semantics, contract separation, migration safety).

After I approve via ExitPlanMode, leave plan mode and execute the PER-ITEM LOOP below.

## READ FIRST

`AGENTS.md`; `docs/plan.md` (Phase 4); `docs/ideas.md` **§9 (Security model)** and **§6/§8**
(subsystem map + the two API contracts); `docs/architecture.md` — **invariant #3** (Torznab XML in
`internal/torznab` + `internal/web/torznab` is the *arr-facing contract; the OpenAPI surface in
`internal/web/swagger` is harbrr's OWN management API — keep them SEPARATE) and **invariant #5**
(storage behind `dbinterface`, **SQLite only, no Postgres yet**). `docs/divergences.md` is the
divergence-ledger index.

Phase 4 turns the proven, merged engine into a **configurable headless daemon** Sonarr/Radarr/autobrr
can point at. Today `cmd/harbrr serve` loads config and exits, and `internal/web/torznab.NewHandler`
has no production caller. This phase builds persistence, the secrets store, the indexer-instance
registry, the management API + auth, and wires the server. It does **NOT** touch live trackers — that
is Phase 5; all engine searches in tests run offline over saved responses / a replay `Doer`.

## MODEL AFTER autobrr/qui

qui is the reference for api/db/security. Study qui at pinned SHA
`667d6541963a1e809b5a6f77a8ac2e8682d2202c`
(`raw.githubusercontent.com/autobrr/qui/<SHA>/<path>`):
- `internal/auth/argon2.go` (argon2id hash/verify), `internal/auth/service.go` (login/setup/API-key
  service), `internal/api/middleware/auth.go` + `apikey_query.go` (SCS sessions, `X-API-Key`,
  auth-disabled/trusted-proxy)
- `internal/models/api_key.go` (SHA-256 keys, mint-once), `internal/models/instance.go` (AES-256-GCM
  at-rest: 32-byte key, per-record random nonce prepended, `*_encrypted` columns, redacted-sentinel
  edit UX), `internal/models/user.go`
- `internal/database/{open,dialect}.go` + `internal/dbinterface`, `internal/domain/secrets.go`
  (`<redacted>` sentinel)
Follow these patterns. The two places harbrr improves on qui are already in §9: AAD-bind the AEAD to
`indexer_id+setting`; store a `key_id`; keep the encryption key separate from the session secret.

## CONTEXT (Phase 3 shipped, on main)

- Engine: `cardigann.NewEngine(def, WithDoer/WithConfig/WithClock/WithBaseURL)`,
  `Capabilities() *mapper.Capabilities`, `Search(Query)`, `ParseResponseQuery`, `ResolveDownload`.
  Loaders: `loader.New("").LoadAll()` (vendored corpus), `loader.Parse(bytes)`, `mapper.Build`.
- The Torznab handler exists and expects a **Provider** (`internal/web/torznab/provider.go`):
  `Provider.Indexer(id) (Indexer, bool)`; `Indexer{ Info() IndexerInfo; Capabilities()
  *mapper.Capabilities; Search(search.Query) ([]*normalizer.Release, error) }`. Phase 4's registry is
  the **production Provider**.
- `internal/config`: `SecretsConfig{EncryptionKey, KeyFile}`, `HasSecretKey()`, `Redacted()/String()`;
  cobra/viper entrypoint wired.
- Stubs to build (doc.go only): `internal/secrets`, `internal/database`, `internal/database/dbinterface`.
- `internal/web/swagger`: `openapi.yaml` (~`/healthz` only) + `//go:embed` + drift test
  (`make test-openapi`). `internal/http/redact.go` (RedactURL/RedactHeader) is the redaction chokepoint.

## HARD RULES (do not work around)

- **SQLite only.** Keep `dbinterface` clean/Postgres-ready but DO NOT implement Postgres (Phase 8).
  **Use a pure-Go driver (`modernc.org/sqlite`), NOT cgo (`mattn/go-sqlite3`)**: the required
  `cross-build (...)` checks run plain `go build` for 5 GOOS/GOARCH targets and cgo breaks
  windows/linux-arm64. This is a CI gate.
- **Secrets: never log/print/commit.** Build the §9 three-class model EXACTLY: web-UI password →
  argon2id; API keys/session tokens → SHA-256 (mint-once); tracker creds → AES-256-GCM (per-record
  nonce, AAD=`indexer_id+setting`, stored `key_id`). Encryption ALWAYS ON (auto-generate a `0600`
  keyfile under the `0700` data dir on first run); plaintext only behind an explicit **fail-closed**
  `secrets.allow_plaintext`; wrong/changed key **fails loud** (startup canary). Login password NEVER
  recoverable; decrypted tracker creds never in logs/errors/Torznab responses/exports. Synthetic test
  secrets live only in `*_test.go`/`testdata/**`.
- **Two HTTP contracts stay separate** (invariant #3): grow `internal/web/swagger` (management OpenAPI)
  on its own route tree; mount the Torznab handler on `/api/v2.0/indexers/...`. `internal/web/torznab`
  must NOT import `internal/web/swagger` or vice versa. OpenAPI changes → regenerate + `make test-openapi`.
- NO AI attribution/co-author/"Generated with" lines. Conventional commits; gofumpt-clean; interfaces
  ≤5 methods; no `map[string]any` for structured data; split god-functions (funlen/gocyclo/gocognit/
  nestif). Before EVERY commit: `make precommit` + `make build` green; tests always `-race -count=1`.
- Branch off main: `phase4/daemon-foundation`. NEVER touch main (protected; required checks: `test`,
  `build`, the five `cross-build (...)`, `secret scan`; lint + CodeQL also run). One `docs/plan.md`
  item per commit; tick its box in the SAME commit, only when its tests are green.

## ORACLE / FIXTURES (decided): OFFLINE, like Phases 1–3

- (a) Crypto gated by known-answer + property tests: encrypt→decrypt round-trips; flip one ciphertext
  byte → auth fails; wrong key / AAD mismatch → fails loud; the ciphertext blob never contains the
  plaintext; password/api-key columns never round-trip to a recoverable value.
- (b) Migrations apply on a fresh temp/in-memory SQLite and are idempotent; data dir `0700`, db +
  `-wal`/`-journal` `0600` (assert in a test).
- (c) Auth: argon2 verify; session login/logout; `X-API-Key` accept/reject; CSRF enforced on
  cookie-auth mutating routes, exempt on the apikey-auth Torznab surface; auth-disabled/trusted-proxy mode.
- (d) End-to-end (offline): `harbrr serve` boots; first-run setup creates the admin; add an indexer
  (creds persisted **encrypted**); the existing Torznab handler, now backed by the DB-resolved
  registry, serves `t=caps` and `t=search` over a saved response / replay `Doer` — Provider wiring
  proven, no network.
- Do NOT run live trackers or a live *arr (Phase 5).

## WORK LIST — each unchecked Phase 4 box is one item, in dependency order

1. **SQLite store + migrations** behind `internal/database/dbinterface` (pure-Go driver; embedded SQL
   migrations + runner; `0700`/`0600` perms incl. side files; the `Querier`/dialect seam, Postgres-
   shaped but SQLite-only).
2. **Secrets store** (`internal/secrets`): AEAD/argon2id/sha256 primitives + key source (config
   key/keyfile + auto-gen + fail-closed plaintext opt-in + fail-loud), and carry the loader
   `SettingsField` secret-vs-plaintext type through so only secret fields are encrypted/redacted.
3. **Indexer instance registry** (models + store): persist `definition-id + settings + encrypted
   credentials`; add/configure/enable/disable/delete; resolve `id → *cardigann.Engine`; implement the
   `web/torznab` `Provider`/`Indexer` over it (incl. `Info()`).
4. **Management API + auth**: grow `openapi.yaml` (indexer CRUD, settings, API-key mgmt, auth/setup)
   spec-first + handlers + drift test; auth service (argon2 login, SCS sessions, `X-API-Key`, CSRF,
   auth-disabled/trusted-proxy/IP-allowlist; OIDC stubbed/deferred per the approved plan).
5. **Wire the server** in `cmd/harbrr serve`: a chi router mounting the management API + the Torznab
   handler on separate trees; listen addr/data dir/base-path config; graceful shutdown; the
   auto-keyfile/plaintext startup logging.
6. **Docker image + config file**: multi-stage, non-root, data volume, healthcheck; documented sample config.

## SUCCESS CRITERIA — assert as a gate

- `harbrr serve` runs as a daemon; first-run setup + login + API-key mint/validate work.
- Tracker creds stored AES-256-GCM (nonce+AAD+key_id); crypto property tests pass; login password
  unrecoverable; redaction holds end-to-end.
- Encryption always on (auto-keyfile); plaintext only via the fail-closed opt-in.
- An added indexer resolves → engine → the existing Torznab handler serves caps/search from the DB
  (offline) — Provider wiring proven.
- Management API + Torznab served from one binary, contracts separate; `make test-openapi` green; all 5
  cross-builds green (pure-Go SQLite).
- SQLite-only; `dbinterface` stays Postgres-ready with no Postgres code.

## PER-ITEM LOOP (after plan approval; one commit per item)

(a) brief per-item plan consistent with the approved master plan; (b) IMPLEMENT + table-driven tests
beside it; (c) VERIFY `make precommit` + `make build`, `-race`; (d) ADVERSARIAL REVIEW — ≥3 independent
skeptics try to REFUTE it (crypto correctness, key management, auth/session/CSRF, SQL/migration safety,
secret leaks, contract-separation, cgo/cross-build). Fix every confirmed issue; re-verify. (If skeptic
agents die on a spend limit, fall back to rigorous inline self-review and SAY SO.) (e) COMMIT: one
focused conventional commit; tick the box in the same commit.

## AFTER ALL ITEMS

- f) END-TO-END PHASE REVIEW + completeness critic ("what crypto/auth/db/edge/claim is unverified?");
  close gaps. Record any divergence from qui or the §9 spec with an explicit disposition in a secrets/DB
  testdata README and add it to `docs/divergences.md` (keep `[Tracked: Phase N]` tags honest).
- g) KEEP THE PR ≤150 FILES (CodeRabbit auto-skips above 150; split a self-contained chunk into a
  second PR + note merge order if needed). Don't open multiple PRs + force-push in rapid succession
  (CodeRabbit ~1h rate-limit; it auto-reviews on PR-open, so do NOT post `@coderabbitai review`
  redundantly).
- h) OPEN ONE PR: `phase4/daemon-foundation → main`, with a summary + testing checklist + a coverage
  table (crypto, auth, DB/migrations, registry, server wiring, Docker). No AI attribution.
- i) CI GREEN: push, fix until all required checks pass (test, build, cross-build ×5, secret scan).
- j) CODE REVIEW: let CodeRabbit's auto-review complete; address EACH finding (validate → fix +
  revalidate, or reply inline why it's skipped/intentional). Re-run CI.
- k) PAUSE: once CI + review are green, STOP. Do NOT merge. Wait for my review.

## FINAL REPORT

Items shipped (commit ids), the credential-storage model as built vs §9, crypto/auth/DB test coverage,
cross-build status, known divergences + dispositions, and open questions.
