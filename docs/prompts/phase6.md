# Phase 6 implementation prompt — Operational safety

Paste the block below into an `ultracode` session to implement Phase 6. It begins in **plan mode**: the
agent must first prompt you for the live resources (a Cloudflare tracker + a FlareSolverr instance + a
reachable proxy + any deferred Phase-5 auth trackers), then plan the *entire* work stream and get the
plan approved before writing any code. Most of Phase 6 is OFFLINE-deterministic and runs in CI; only the
FlareSolverr live retest, the per-indexer proxy end-to-end, and the deferred Phase-5 auth-pattern
retests need operator-supplied live resources — those follow the Phase 5 execution protocol (resourced
at planning, or deferred with an explicit `[Tracked: …]` disposition).

---

ultracode — Implement **Phase 6 (Operational safety)** from `docs/plan.md` as ONE reviewable PR. **Begin
in PLAN MODE — do STEP 0 before anything else.**

This PR hardens the running daemon for real-world operation: per-request timeouts + retry backoff +
per-indexer rate limits (anti-blacklist), per-indexer indexer **health & status** surfaced via the
management API, per-indexer **proxies** (HTTP / SOCKS4 / SOCKS5), **secret hardening** (key rotation
via the stored `key_id` + a redaction audit), and the **FlareSolverr solver** completing the Phase 5
`login.Solver` seam — plus the enabling **context.Context threading** the network features all need.
Phase 7 items (download resolver / `/dl` completion, XML edge parity, Avistaz, backup/restore) and
Phase 8 items (web UI, app-sync, Prowlarr import, OIDC, Postgres) are **out of scope** — see the
deferral list in the WORK LIST.

## STEP 0 — PLAN MODE FIRST (mandatory)

Enter plan mode immediately. Do **not** create a branch, write code, edit files, or send any live
request that *mutates* state until the plan is approved. Two things happen in plan mode, in order.

### 0a — Prompt me for the live resources + the test bed (do this FIRST)

Phase 6 is **mostly offline**, but four items need live infrastructure I supply. Before planning, prompt
me (use AskUserQuestion / direct questions) for, and securely intake, what is available:

- **FlareSolverr** — base URL incl. scheme + port (e.g. `http://flaresolverr:8191`) of a reachable
  instance, plus **a real Cloudflare-gated tracker I have an account on** to live-retest the solver. (In
  the Phase 5 env neither existed — trackers were restricted to non-CF and no FlareSolverr ran.)
- **A reachable proxy** — an HTTP and/or SOCKS5 (and SOCKS4 if available) proxy URL the daemon can dial,
  to end-to-end verify per-indexer proxying. Note whether it requires `user:pass` auth.
- **Deferred Phase-5 auth patterns** (retest-only — the code already exists): a clean **user/pass
  form-login** tracker; a **cookie / 2FA (manual-cookie)** tracker; optionally a **.NET-quirk** tracker
  whose inputs exercise `*()'!` / unicode / regexp2. Names only first; confirm before I hand over creds.
- **The Phase 5 test bed** — confirm the same Sonarr/Radarr + Prowlarr (differential oracle) + qBittorrent
  wiring from Phase 5 is still available for any live smoke re-run.

For each resource I cannot supply on the day, you will record the corresponding item as
**DEFERRABLE-with-disposition** (`[Tracked: …]`) rather than faking it — offline coverage already exists
for the auth-pattern retests, and the FlareSolverr/proxy *code* is fully offline-testable (FlareSolverr
against a stub `/v1` server; proxy via doer construction + proxy-URL parsing).

**Credential / live-resource handling rules (NON-NEGOTIABLE — AGENTS.md):**
- **NEVER** write any credential, proxy password, FlareSolverr URL with embedded auth, or API key to a
  repo file, the plan, a fixture, `testdata/**`, a commit, or a log. They live ONLY in harbrr's encrypted
  store (the gitignored data dir) and in working memory for this run.
- Never echo a credential back in plaintext — refer to them by tracker/field name. Redact everywhere.
- At execution time, enter tracker creds + per-indexer proxy + solver config into the **running daemon
  via the management API** (`POST /api/indexers` / `PATCH /api/indexers/{slug}`) so they land
  **AES-256-GCM-encrypted**; never hand-write them into the DB or a file.
- Any fixture captured from a live response (incl. a FlareSolverr `solution`) is **secret-scrubbed**
  (cf_clearance/cookies/passkeys/UA-coupled tokens redacted) **before** it is committed.
- You MAY do **read-only** connectivity checks in plan mode (a FlareSolverr `sessions.list`, a proxy
  reachability probe, an *arr `GET /api/v3/system/status`) to confirm resources work — read-only, no
  writes, no indexer adds.

### 0b — Produce ONE complete plan for the entire Phase 6 work stream

Plan all work-list items end to end (not just the first). Pressure-test the plan with a
validator/architect agent (and revise), then present it with ExitPlanMode for my approval and wait.

The plan must cover:
- **Architecture decisions to confirm** (call these out explicitly so I can approve/redirect; each with a
  stated default):
  - **Context threading is the enabling refactor, done FIRST** (default: yes). Three call sites hard-code
    `context.Background()` — `login/solver.go:84` (the solver call), `login/login.go:168`
    (`NewRequestWithContext`), `search/request.go:325` — and there is no `ctx` in scope to thread.
    Confirm threading a request-scoped `ctx` from the Torznab handler (it has `r.Context()`) down through
    `torznab.Provider.Indexer` / `torznab.Indexer.Search` → `Engine.Search` / `Engine.Test` → login /
    search / `Solver.Solve`. This touches the `torznab.Provider`/`torznab.Indexer` interfaces and the
    engine signatures — a broad-but-mechanical change. Also fix `registry.go:109`'s
    `r.resolve(context.Background(), slug)` once the entrypoint carries `ctx`.
  - **Rate-limiter granularity** (operator decision; default: **per indexer instance**, keyed by
    `definition-id + base URL`, NOT per host). Confirm: a mutex-guarded `map[instanceKey]*rate.Limiter`
    (`golang.org/x/time/rate`), lazily created + idle-evicted, gated with `Wait(ctx)`; backoff
    (`cenkalti/backoff/v4` or hand-rolled exp+jitter honoring `Retry-After`) layered on 429/503. State
    whether per-indexer rate/burst is configurable (a per-instance setting) or a global default.
  - **Timeouts** — confirm extending the existing `registry.WithTimeout` / `newDoer(timeout)` knob to a
    per-instance request timeout, and that the FlareSolverr HTTP client timeout is **strictly greater
    than** its `maxTimeout` (default 60s, allow up to 180s like Prowlarr) — a CF solve legitimately takes
    many seconds.
  - **The `doerFactory` widening** (operator decision; both proxies and the FlareSolverr fetch client ride
    this one seam). Today `doerFactory func() (search.Doer, error)` is **nullary** (registry.go:43) and
    called once per `build()` at registry.go:166 — it cannot vary the client per instance. Default:
    widen it to take the instance/cfg (e.g. `func(inst domain.IndexerInstance, cfg map[string]string)
    (search.Doer, error)`), or resolve the proxy URL inside `build()` before calling it. Confirm the
    choice so proxies and the solver don't fork the client-construction path.
  - **Proxy config schema** (operator decision; default: per-instance settings `proxy_type`
    (`http`|`socks4`|`socks5`) + `proxy_url`, with embedded `user:pass` treated as a **secret** setting).
    Confirm field names + whether the proxy URL/password is a secret (encrypted) setting. HTTP via
    `Transport.Proxy = http.ProxyURL`; SOCKS via `golang.org/x/net/proxy.SOCKS5`/`FromURL` wired through
    `Transport.DialContext` (cast to `proxy.ContextDialer`). Note `net/http`'s env-proxy ignores socks5.
  - **Health-event storage shape** (operator decision; default: an **append-only** `indexer_health_events`
    table in a new migration `0002_indexer_health.sql`, FK to `indexer_instances` ON DELETE CASCADE, a
    `(instance_id, occurred_at)` index, RFC3339 TEXT timestamps — copying `0001_init.sql` + `instances.go`
    conventions; do NOT add status columns to `indexer_instances`). `kind` enum = the four plan.md
    categories `auth_failure | rate_limited | parse_error | anti_bot`; `detail` is `sanitizeTestError`-
    scrubbed. The read API: a single new `GET /api/indexers/{slug}/status`. Confirm the table vs (the
    alternative) a status-columns-on-instance design, and whether a fleet-wide `GET /api/indexers/status`
    is in scope (extra route → extra spec entry).
  - **Health-event classification** — `ErrLoginFailed` → `auth_failure`; `ErrSolverRequired` /
    `detectAntiBot` → `anti_bot`. There is **no** `rate_limited` or `parse_error` sentinel today —
    decide: add a typed error in the login/search layer (or classify on HTTP status) rather than lumping
    them into `auth_failure`. State the choice.
  - **Key rotation mechanics** (operator decision; default: a `harbrr` subcommand that holds **old + new**
    keys, iterates every `indexer_settings` secret row, `Decrypt`-with-old / `Encrypt`-with-new rewriting
    `value_encrypted` + `key_id` per row, then rewrites the canary (`EncryptCanary` + meta
    `secrets_key_id`)). Today `Keyring` holds a **single** key and `Decrypt` always uses the active key
    (a row whose `key_id` ≠ active simply fails) — confirm whether the transition uses a two-Keyring
    handoff or a Keyring that can decrypt by a row's stored `key_id`. The per-row `key_id` + AAD
    (`<instanceID>\x00<setting>`) make rotation feasible; the orchestration is net-new.
  - **FlareSolverr consume-vs-replay** (operator decision; default: **discard-and-replay** like Prowlarr —
    take `solution.cookies` + `solution.userAgent`, then re-issue the real request yourself). cf_clearance
    is **UA-bound**, so the replay MUST carry `solution.userAgent` AND a browser-realistic header set
    (mirror `Accept`/`Accept-Encoding` — a gzip-only header set is a known 403 trigger). Confirm
    consume-vs-replay and a typed request/response model (no `map[string]any`).
  - **Redaction-audit scope** — `RedactURL`/`RedactHeader` are complete and used at all current log/error
    sites; there is **no tracing and no stats/event-log subsystem** in the repo, so those audit targets
    are vacuous today (the new `indexer_health_events.detail` is the one new persisted surface — scrub it).
    Confirm the audit = (1) extend redaction for FlareSolverr request/response bodies (`cookies`,
    `postData`, proxy creds, UA) and proxy-credential URLs/`*Auth`, and (2) prove every existing + new
    log/error/persisted site routes secrets through a chokepoint.
- **Package/file layout** — every package/file to create or modify, each file's responsibility (e.g.: the
  ctx-threading refactor across `internal/web/torznab`, `internal/indexer/registry`,
  `internal/indexer/cardigann` engine + login + search; a rate-limiter/backoff package or registry
  collaborator; `internal/indexer/registry/client.go` `newDoer` transport + proxy wiring + widened
  `doerFactory`; `internal/database/migrations/0002_indexer_health.sql` + a stateless `database.Health`
  repo; `internal/domain` health model; the `GET /api/indexers/{slug}/status` handler + `openapi.yaml`
  path + `components/schemas`; the FlareSolverr `Solver` impl + its `SolverOption` wiring + a new
  `solver_type` value; the key-rotation command in `internal/secrets` + `cmd/harbrr`; redaction
  extensions in `internal/http/redact.go`).
- **The LIVE vs OFFLINE split per item** — which items are gated by committed **offline deterministic
  tests** (everything: rate-limiter/backoff with a fake clock, ctx propagation/cancellation, the
  health-event taxonomy + status API over synthetic engine errors, proxy doer construction, key rotation
  via two keys + canary verify, the FlareSolverr client against a **stub `/v1` server**, redaction
  fixtures) and which need the **operator-supplied live resources** (FlareSolverr CF retest, proxy
  end-to-end, the deferred Phase-5 auth retests). CI stays fully offline.
- **Test strategy per item → oracle mapping**; **commit/box sequencing** — context threading is the
  enabling refactor (it ticks **no plan.md box** — say so in the commit, like Phase 8's Commit 1); then
  which of the four `docs/plan.md` Phase 6 boxes each subsequent commit ticks. Tick a box only when its
  tests are green.
- **Risks + mitigations** — the **150-file CodeRabbit cap** (the ctx-threading refactor touches many
  files; plan the split + merge order up front); a relogin/retry or backoff **loop**; rate-limiter map
  growth / a deadlock under `Wait(ctx)`; SOCKS dial wiring (`net/http` env-proxy ignores socks5); a
  **key-rotation half-write** corrupting the store (atomicity / a single tx / a dry-run + canary-first);
  a credential leak through a FlareSolverr body, a proxy URL, or a new health-event `detail`; the
  UA-coupling / gzip-header 403 on FlareSolverr replay; and a live account ban on the CF retest.

After I approve via ExitPlanMode, leave plan mode and execute the PER-ITEM LOOP below.

## READ FIRST

`AGENTS.md`; `docs/plan.md` (Phase 6 — the four boxes — **+ the Phase 5 execution-protocol blockquote**
for the live-resource discipline); `docs/ideas.md` **§9 (Security model)** + the fetch/auth + indexer-
proxy sections; `docs/architecture.md` — **invariant #3** (the Torznab *arr-facing contract and the
management OpenAPI surface stay SEPARATE — the new `GET …/status` route lives ONLY on the management
tree) and **invariant #5** (SQLite only, no Postgres). `docs/divergences.md` is the divergence-ledger
index; `docs/highlights.md` is the honestly-labelled feature log. Read the seams this work builds on:
`internal/indexer/registry/registry.go` (the `doerFactory` field + `WithDoerFactory` option + `build()`
wiring + `logResolveError` redaction) and `client.go` (`newDoer`); `internal/indexer/cardigann/login/
solver.go` (the `Solver` interface + `NoopSolver`/`ManualCookieSolver` + `fetchLandingPastAntiBot` + the
`context.Background()` call site) and `login.go`/`methods.go` (`ErrLoginFailed`/`ErrSolverRequired`/
`detectAntiBot`/`cloudflareMarkers`/`checkErrors`); `internal/indexer/cardigann/search/request.go`
(`doRequest`'s ctx gap + non-2xx fail-fast); `internal/secrets/keyring.go` + `aead.go` + `canary.go`
(`Encrypt`/`Decrypt`, `key_id`/`deriveKeyID`, the AAD, `EncryptCanary`/`VerifyCanary`) and
`cmd/harbrr/serve.go` (`verifyCanary`); `internal/database/instances.go` + `migrations/0001_init.sql` +
`migrate.go` (the repo + migration conventions); `internal/web/api/router.go` + `indexer_handlers.go`
(`testIndexer` + `sanitizeTestError`) + `internal/web/swagger/openapi.yaml` + the drift tests
(`router_test.go`, `openapi_test.go`); `internal/http/redact.go` (`RedactURL`/`RedactHeader`); and
`internal/indexer/cardigann/degrade_test.go` (the Phase 2 clean-degradation baseline this builds on).

Phase 6 hardens the **already-live** daemon (Phases 4–5 shipped) so it survives real operation without
getting a tracker IP/account blacklisted, surfaces *why* an indexer is unhealthy, can route per-indexer
through a proxy, can rotate its encryption key, and can clear Cloudflare via FlareSolverr. It does **NOT**
touch the parity engine's normalized output, the Torznab serializer contract, the vendored definitions,
or any Phase 7/8 surface; it does **NOT** complete the download resolver or build the web UI.

## CONTEXT (Phase 5 shipped — the daemon, proven LIVE)

- `harbrr serve` is a real daemon: SQLite + migrations, the §9 secrets store (AES-256-GCM tracker creds
  with per-record nonce + AAD=`indexer_id+setting` + stored `key_id`, argon2id password, SHA-256 API
  keys, auto-keyfile, fail-loud startup **canary**), first-run setup + login + `X-API-Key`, the indexer-
  instance registry as the production `torznab.Provider`, the management API (`/api/indexers`,
  `/api/apikeys`, `/api/auth/*`), and the Torznab handler at `/api/v2.0/indexers/...`. Phase 5 closed the
  MVP live (5 non-CF trackers, search → grab, Prowlarr differential) and landed lazy login, the .NET URL
  encoder, category filtering, the served download link, and the indexer **Test** action.
- **Seams Phase 6 builds on (already in place, do NOT re-invent):**
  - `registry.WithDoerFactory(fn func() (search.Doer, error))` + the `doerFactory` field
    (registry.go:43,77-83) — comment already says "Phase 6 can inject a per-indexer proxy client." It is
    **nullary** and called once in `build()` (registry.go:166) → `cardigann.WithDoer(doer)`; widening its
    arity is the net-new part. `newDoer(timeout)` (client.go:24) builds a bare `http.Client{Jar,Timeout}`
    — **no transport / proxy / retry** yet. `defaultHTTPTimeout = 60s`; `WithTimeout` exists.
  - `login.Solver` interface (solver.go:17, `Solve(ctx, targetURL) (SolveResult{Cookies,UserAgent}, err)`)
    with `NoopSolver` (default, returns `ErrNoSolverConfigured` → fail loud) and a functional
    `ManualCookieSolver`. Threaded via `cardigann.WithSolver` / `SolverOption(cfg)` (engine.go:94,105) →
    `buildLogin` → `Executor.Solver`. `fetchLandingPastAntiBot` already detects anti-bot, solves ONCE,
    seeds cookies, retries with the solver UA, and fails loud if still challenged. **The FlareSolverr impl
    is the only missing piece** (`[Tracked: Phase 6 — FlareSolverr solver]` at solver.go:16).
  - The login failure taxonomy already classifies: `ErrLoginFailed` (auth), `ErrSolverRequired` +
    `cloudflareMarkers`/`detectAntiBot` (anti-bot), `ErrCaptchaRequired`, `ErrNoSolverConfigured`;
    `checkErrors` maps HTTP 401 and a 429-style status (login.go:22-35,233-249; methods.go:146-175). **No
    `rate_limited`/`parse_error` sentinel exists** — net-new for the health item.
  - Secrets substrate for rotation is ready: every secret row stores its `key_id`
    (`instances.go:52,81-98`; `validateSettingInvariant` requires a non-empty `key_id` + no plaintext for
    secrets, instances.go:227-241); `deriveKeyID` = `hex(SHA-256(key))[:16]`; the AAD binds each
    ciphertext to one `(instance, setting)` row; the canary detects a key change and **fails loud** but
    does **not** re-encrypt. **No rotation flow exists** — net-new.
  - Redaction chokepoint: `RedactURL` (secret query params + userinfo password + unparseable fallback) and
    `RedactHeader` (Authorization/Cookie/Set-Cookie/X-Api-Key/Proxy-Authorization), used at the torznab
    handler, registry, login, and search error/log sites. **No central redacting logger** — redaction is
    per-call-site; **no tracing / stats-event-log subsystem** exists.
  - Clean degradation (Phase 2, test-gated by `degrade_test.go`): a missing/disabled/unbuildable instance
    returns `ok=false` (not a crash); an unresolved indexer → Torznab `<error>` 201 at HTTP 200; a
    search/internal error → a **redacted** generic 900 doc at HTTP 500; no-results → a valid empty feed; a
    transport error never leaks the passkey. Phase 6's safety additions follow this error-status policy.

## HARD RULES (do not work around)

- **LIVE resources** — per STEP 0: FlareSolverr URLs (incl. embedded auth), proxy passwords, and tracker
  creds are never logged/committed/echoed; encrypted store only; redacted everywhere; any captured fixture
  (incl. a FlareSolverr `solution`) is **secret-scrubbed** before commit.
- **LIVE traffic discipline** — the live FlareSolverr CF retest and proxy/auth retests use a **gentle
  rate**: sequential, low concurrency, sane delays, respect each def's rate limits. If a tracker returns
  rate-limit / anti-bot / a ban signal, **back off and report** — do NOT hammer or risk a ban. The CF
  solve is heavy (one headless browser per session); reuse a session, don't spawn per-request.
- **The live retests are an integration gate** — run manually / under a **build tag** with **env-var**
  resources (reuse Phase 5's smoke harness pattern). They **NEVER** run in normal CI and **never** require
  committed secrets. The FlareSolverr solver itself is offline-tested against a **stub `/v1` server**. CI
  stays fully **offline and deterministic**.
- **Never edit vendored definitions** under `internal/indexer/definitions/vendor/` (consumed byte-for-byte
  from Jackett; a PreToolUse hook blocks it). All behavioral differences are absorbed in the engine; fixes
  go upstream or to `internal/indexer/definitions/dropin/`.
- **Secret redaction stays absolute** — never log/print/commit a passkey, cookie, cf_clearance, proxy
  password, API key, or download token. Extend redaction to the new FlareSolverr/proxy surfaces and scrub
  the new `indexer_health_events.detail` (reuse `sanitizeTestError`). Synthetic test secrets live ONLY in
  `*_test.go` / `testdata/**`, kept in sync across `scripts/check-no-secrets.sh` and `.gitleaks.toml`.
- **Key rotation must be atomic + fail-loud** — never half-rewrite the store: rotate in a single
  transaction (or a verified dry-run + canary-first), and a wrong/missing old key fails loud before any
  write. Carry the §9 invariants: login password stays unrecoverable; decrypted creds never reach logs /
  errors / Torznab responses / a rotation log.
- **SQLite only**; pure-Go driver; **two HTTP contracts stay separate** (invariant #3 — `GET
  …/status` lives only on the management tree, never the Torznab tree); OpenAPI changes →
  `make test-openapi`. Carry **every** Phase 4 and Phase 5 hard rule forward.
- NO AI attribution/co-author/"Generated with" lines. Conventional commits; gofumpt-clean; interfaces
  ≤5 methods; no `map[string]any` for structured data (typed FlareSolverr request/response + health +
  proxy config structs); split god-functions (funlen/gocyclo/gocognit/nestif). Before EVERY commit:
  `make precommit` + `make build` green; tests always `-race -count=1`.
- Branch off main: `phase6/operational-safety`. NEVER touch main (protected; required checks: `test`,
  `build`, the five `cross-build (...)`, `secret scan`; lint + CodeQL also run). One `docs/plan.md` item
  per commit; tick its box in the SAME commit, only when its tests are green.

## ORACLE / FIXTURES (decided): OFFLINE + deterministic, with operator-resourced LIVE retests gated out of CI

- **Offline deterministic** (committed; runs in CI — the gate for all four plan.md boxes):
  - **Timeouts / backoff / rate limits**: per-indexer `rate.Limiter` paces calls (assert spacing with a
    **fake clock**); backoff fires on **429/503** and honors `Retry-After`, with a bounded retry count
    (never loops); a request deadline cancels via the threaded `ctx` (prove propagation + cancellation
    over a replay `Doer`/timeout). The map of limiters is keyed per instance and evicts idle entries.
  - **Context threading**: a cancelled `ctx` at the handler aborts the solver/login/search call (no
    `context.Background()` remains at the three sites); table-driven cancellation tests.
  - **Health & status**: synthetic engine errors map to the four `kind`s (`auth_failure` from
    `ErrLoginFailed`, `anti_bot` from `ErrSolverRequired`/`detectAntiBot`, plus the new
    `rate_limited`/`parse_error` classification); events persist append-only with FK CASCADE; `GET
    /api/indexers/{slug}/status` returns the derived status with a **scrubbed** `detail` (a passkey/cookie
    never lands in the DB or the response); the **bidirectional** OpenAPI drift test passes (route ⇔ spec
    for the exact path `/api/indexers/{slug}/status`); `make test-openapi` green.
  - **Per-indexer proxy**: the widened `doerFactory`/`newDoer` builds the right client per `proxy_type`
    (HTTP via `Transport.Proxy`, SOCKS via `x/net/proxy` → `DialContext`); proxy-URL parsing + a bad
    proxy config fails loud; the proxy password is encrypted at rest and never logged.
  - **Key rotation**: encrypt with key A → rotate to key B → every row's `value_encrypted` + `key_id`
    is rewritten and decrypts under B; the canary + meta `secrets_key_id` update to B; a wrong old key
    fails loud **before** any write; a flipped/half-written row is detected; the rotation log is secret-
    free.
  - **FlareSolverr solver**: against a **stub `/v1` HTTP server**, `request.get` round-trips a typed
    request/response; `solution.cookies` + `solution.userAgent` flow into the seam and the replay carries
    the UA (and the non-fingerprinting header set); a non-`ok` status / timeout fails loud and maps to an
    `anti_bot` health event; the solver wires into `SolverOption` under a new `solver_type`.
  - **Redaction**: fixtures cover a FlareSolverr request body (`cookies`+`postData`), a response
    `solution` (cf_clearance), a SOCKS proxy URL with embedded `user:pass`, and the new health-event
    `detail` — each scrubbed end-to-end. The audit asserts every log/error/persisted site routes through
    a chokepoint.
- **Operator-resourced LIVE retests** (manual / build-tagged; **never** in CI): FlareSolverr clears a
  **real Cloudflare tracker** end-to-end (search → result, optionally grab); a per-indexer **proxy**
  routes a real search; the deferred Phase-5 **form-login**, **cookie/2FA**, and **.NET-quirk** patterns
  retest live. Each item with no resource on the day is **DEFERRABLE-with-disposition** (`[Tracked: …]`).
- **Live evidence** is captured in the PR body / a Phase 6 testdata README (per-resource pass/fail, the CF
  solve proof, the proxy proof) — **NOT** committed creds, NOT raw unscrubbed live responses.

## WORK LIST — each unchecked Phase 6 box is one item, in dependency order

1. **Context threading** (enabling refactor; **no plan.md box** — say so in the commit): thread a
   request-scoped `context.Context` from the Torznab handler through `torznab.Provider`/`torznab.Indexer`,
   `Engine.Search`/`Engine.Test`, login, search, and `Solver.Solve`, removing the three hard-coded
   `context.Background()` sites (`login/solver.go:84`, `login/login.go:168`, `search/request.go:325`) and
   `registry.go:109`. Purely offline; unblocks timeouts/backoff (item 2), proxies (item 4), and the
   FlareSolverr network call (item 5).
2. **Timeouts, backoff, per-indexer rate limits** (anti-blacklist): per-request timeouts, retry backoff
   on 429/503 (honor `Retry-After`, bounded), and a per-indexer `rate.Limiter` so harbrr never gets an IP
   /account blacklisted. *(plan.md "Timeouts, backoff, per-indexer rate limits" box.)*
3. **Indexer health & status**: define the four health events (`auth_failure`, `rate_limited`,
   `parse_error`, `anti_bot`) recorded from the registry (new `database.Health` repo + migration
   `0002_indexer_health.sql`), and surface per-indexer status via a new `GET /api/indexers/{slug}/status`
   (registered in the authenticated group; spec + `components/schemas` + drift test in the SAME commit).
   *(plan.md "Indexer health & status" box.)*
4. **Per-indexer proxies** (HTTP / SOCKS4 / SOCKS5): configure a proxy per indexer instance via the
   widened `doerFactory`/`newDoer` (`Transport.Proxy` for HTTP; `x/net/proxy` → `DialContext` for SOCKS),
   proxy config as per-instance settings (URL/password encrypted). Live-retest if a proxy is supplied,
   else `[Tracked: …]`. *(plan.md "Per-indexer proxies" box.)*
5. **Secret hardening**: (a) **key rotation** — a command that re-encrypts every secret row via its stored
   `key_id` (old key → new key, rewrite `value_encrypted`+`key_id`+canary, atomic, fail-loud); (b) the
   **redaction audit** end-to-end across logs, errors, and the new persisted surfaces (extend redaction
   for FlareSolverr/proxy bodies + scrub `health_events.detail`; note traces/stats-event-log are absent so
   vacuous today). *(plan.md "Secret hardening" box.)*
6. **FlareSolverr solver + live CF retest**: implement the FlareSolverr `Solver` behind the existing
   `login.Solver` seam (typed `/v1` request/response, consume-vs-replay per the locked decision, UA-coupled
   replay), wire it into `SolverOption` under a new `solver_type`, update the stale "Phase 4" text in
   `ErrSolverRequired`/`ErrCaptchaRequired`/`detectAntiBot`, and (co-designed with item 3) map a solver
   failure to an `anti_bot` health event. Offline-tested against a stub `/v1` server; live-retest a real
   CF tracker if FlareSolverr + a CF tracker are supplied, else `[Tracked: …]`. *(Closes the `[Tracked:
   Phase 6 — FlareSolverr solver]` ledger item; folds into the relevant plan.md box per the approved
   sequencing — say which.)*
7. **Deferred Phase-5 auth-pattern live retests** (retest-only — code exists): user/pass **form-login**,
   **cookie / 2FA (manual-cookie)**, and the **.NET-quirk** (`*()'!`/unicode/regexp2) patterns, each
   live-confirmed if the operator supplies a tracker, else **DEFERRABLE-with-disposition** (`[Tracked:
   …]`) — offline coverage already exists. *(No new code expected; a confidence/retest item from the
   smoke README ledger.)*

**Explicitly OUT of scope — separate follow-on PRs (one line each, do NOT build here):**
- Download resolver completion / full `/dl` proxy — `[Tracked: Phase 7 — download resolver]`
- XML backend edge parity (CDATA / mixed-namespace / AngleSharp edges) — `[Tracked: Phase 7 — XML edge parity]`
- Native Avistaz family — `[Tracked: Phase 7 — Avistaz]`
- Backup / restore (config + DB; redacted/encrypted export) — `[Tracked: Phase 7 — backup/restore]`
- Web UI / Swagger UI render / stats display — `[Tracked: Phase 8 — web UI]`
- \*arr app-sync, Prowlarr import, autobrr push, OIDC, Postgres — `[Tracked: Phase 8 — …]`
- A **stats event-log subsystem** (and tracing) — does not exist today, so the redaction audit treats
  those targets as vacuous; building them is out of Phase 6 — `[Tracked: Phase 8 — stats data layer]`.

## SUCCESS CRITERIA — assert as a gate

- harbrr paces every outbound request per indexer (rate limiter), times out, and **backs off on 429/503
  honoring `Retry-After`** without looping — proven offline with a fake clock; a cancelled request `ctx`
  aborts the call (no `context.Background()` at the three sites).
- Per-indexer **health events** (`auth_failure`/`rate_limited`/`parse_error`/`anti_bot`) are recorded and
  surfaced at `GET /api/indexers/{slug}/status` with a **scrubbed** `detail`; the migration applies on a
  fresh DB; the **bidirectional** OpenAPI drift test + `make test-openapi` are green; the two contracts
  stay separate.
- A per-indexer **proxy** (HTTP / SOCKS4 / SOCKS5) routes that indexer's traffic via the widened
  `doerFactory`/`newDoer`; the proxy password is encrypted at rest and never logged.
- **Key rotation** re-encrypts every secret row via its stored `key_id` (rewriting `value_encrypted` +
  `key_id` + canary), is atomic + fail-loud on a wrong old key, and leaves every row decryptable under the
  new key; the rotation path logs nothing secret.
- The **FlareSolverr solver** completes the `login.Solver` seam (offline against a stub `/v1`; live-cleared
  a real CF tracker if resourced, else `[Tracked: …]`); the stale "Phase 4" labels are corrected.
- **No credential** (passkey / cookie / cf_clearance / proxy password / API key) ever appears in a log,
  error, the served feed, the health-event `detail`, a fixture, or a commit; redaction holds end-to-end on
  the new FlareSolverr/proxy/health surfaces; the live harness needs **no committed secrets** and never
  runs in normal CI.
- `make precommit` + `make build` green; all 5 cross-builds green; contracts still separate; SQLite-only;
  PR ≤150 files.

## PER-ITEM LOOP (after plan approval; one commit per item)

(a) brief per-item plan consistent with the approved master plan; (b) IMPLEMENT + table-driven tests
beside it (offline/deterministic where the behaviour allows — fake clock for rate/backoff, stub `/v1` for
FlareSolverr, two keys + canary for rotation; the live items carry their build-tagged harness + captured
evidence); (c) VERIFY `make precommit` + `make build`, `-race`; (d) ADVERSARIAL REVIEW — ≥3 independent
skeptics try to REFUTE it (rate-limiter map growth / deadlock under `Wait(ctx)` / wrong key granularity;
backoff or relogin **loop** / unbounded retry / ignored `Retry-After`; ctx-cancellation not actually
propagating; health-event mis-classification or a **secret in `detail`** / OpenAPI drift; SOCKS dial
wiring / `net/http` socks5 env gap / proxy-password leak; key-rotation **half-write / non-atomic / wrong-
key-not-caught / canary desync**; FlareSolverr UA-coupling + gzip-header 403 / typed-model gaps / a secret
in a FlareSolverr body; any new redaction blind spot; live-rate discipline + ban risk). Fix every
confirmed issue; re-verify. (If skeptic agents die on a spend limit, fall back to rigorous inline
self-review and SAY SO.) (e) COMMIT: one focused conventional commit; tick the box in the same commit
(the context-threading commit ticks no box — it is enabling infrastructure).

## AFTER ALL ITEMS

- f) END-TO-END PHASE REVIEW + completeness critic ("which timeout / backoff / rate / health-classification
  / proxy / rotation-edge / redaction site / live-resource claim is unverified?"); close gaps. Record
  every divergence with an explicit disposition in a Phase 6 testdata README AND `docs/divergences.md`
  (the widened `doerFactory` arity vs the Phase 4 nullary seam; the new `rate_limited`/`parse_error`
  classification; the rotation transition model; the corrected "Phase 4" solver labels; any deferred live
  retest as `[Tracked: …]`; traces/stats-event-log absence). Add the Phase 6 improvements to
  `docs/highlights.md` (honestly labelled `[shipped]`/`[partial]`/`[planned]`).
- g) KEEP THE PR ≤150 FILES (CodeRabbit auto-skips above 150; the ctx-threading refactor touches many
  files, so split a self-contained chunk into a second PR + note merge order if needed). Don't open
  multiple PRs + force-push in rapid succession (CodeRabbit ~1h rate-limit; it auto-reviews on PR-open, so
  do NOT post `@coderabbitai review` redundantly).
- h) OPEN ONE PR: `phase6/operational-safety → main`, with a summary + testing checklist + a coverage
  table (context threading, timeouts/backoff/rate limits, health & status + API + migration + drift,
  per-indexer proxies, key rotation, redaction audit, FlareSolverr solver, the deferred-auth retests). No
  AI attribution. **No creds / proxy URLs / FlareSolverr URLs in the PR body.**
- i) CI GREEN: push, fix until all required checks pass (test, build, cross-build ×5, secret scan). CI is
  fully offline — the live retests do not run here.
- j) CODE REVIEW: let CodeRabbit's auto-review complete; address EACH finding (validate → fix + revalidate,
  or reply inline why it's skipped/intentional). Re-run CI.
- k) PAUSE: once CI + review are green, STOP. Do NOT merge. Wait for my review.

## FINAL REPORT

Items shipped (commit ids); the operational-safety surface as built vs the four plan.md boxes (timeouts +
backoff + per-indexer rate limits; health & status events + `GET /api/indexers/{slug}/status` + migration
`0002`; per-indexer HTTP/SOCKS proxies; key rotation + redaction audit) plus the FlareSolverr solver and
the context-threading refactor; offline test coverage by area (fake-clock rate/backoff, ctx cancellation,
health taxonomy + drift, proxy doer construction, rotation + canary, stub-`/v1` solver, redaction
fixtures); the live-retest results per supplied resource (FlareSolverr CF clear, proxy, form-login,
cookie/2FA, .NET-quirk) or each as `[Tracked: …]`; cross-build status; explicit confirmation that no
credential, proxy password, cf_clearance, or FlareSolverr secret was logged or committed and that
redaction holds on every new surface (incl. health-event `detail`); known divergences + dispositions
(the widened `doerFactory` arity, the new health classification, the rotation transition model, the
corrected "Phase 4" labels, traces/stats-event-log absence, any deferred live retest); and open questions.
