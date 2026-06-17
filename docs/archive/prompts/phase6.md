# Phase 6 implementation prompt — Operational safety

Paste the block below into an `ultracode` session to implement Phase 6. It begins in **plan mode**: the
agent must first prompt you for the live resources (a Cloudflare tracker + a FlareSolverr instance + a
reachable proxy + any deferred Phase-5 auth trackers), then plan the *entire* work stream and get the
plan approved before writing any code. Most of Phase 6 is OFFLINE-deterministic and runs in CI; only the
FlareSolverr live retest, the per-indexer proxy end-to-end, and the deferred Phase-5 auth-pattern
retests need operator-supplied live resources — those follow the Phase 5 execution protocol (resourced
at planning, or deferred with an explicit `[Tracked: …]` disposition).

**Phase 6 ships as TWO PRs (decided up front, not contingent):**
- **PR #1 — context threading** (the enabling refactor; ticks **no** `docs/plan.md` box). Broad-but-
  mechanical, purely offline; lands first and merges before PR #2 opens.
- **PR #2 — the four Phase 6 boxes + the FlareSolverr solver**, built on top of PR #1.

This split is deliberate: a blast-radius walk puts ctx-threading alone at ~25-45 files and all of Phase 6
at ~80-130+, and CodeRabbit **silently auto-skips any PR over 150 files**. Splitting at the clean ctx
boundary keeps each PR reviewable and removes the 150-file coin-flip. (See RISKS.)

A note on the line-number citations below: they are as-of authoring. If a cited line has moved, **trust
the named symbol/function and re-locate it** — do not treat a stale line number as evidence the site is
gone.

---

ultracode — Implement **Phase 6 (Operational safety)** from `docs/plan.md` as **TWO reviewable PRs**
(`phase6/ctx-threading` then `phase6/operational-safety`). **Begin in PLAN MODE — do STEP 0 before
anything else.**

This work hardens the running daemon for real-world operation: per-request timeouts + retry backoff +
per-**host** rate limits (anti-blacklist), per-indexer indexer **health & status** surfaced via the
management API, per-indexer **proxies** (HTTP / SOCKS4 / SOCKS5), **secret hardening** (key rotation via
the stored `key_id` + a redaction audit), and the **FlareSolverr solver** completing the Phase 5
`login.Solver` seam — plus the enabling **context.Context threading** the network features all need.
Phase 7 items (download resolver / `/dl` completion, XML edge parity, Avistaz, backup/restore) and
Phase 8 items (web UI, app-sync, Prowlarr import, OIDC, Postgres) are **out of scope** — see the
deferral list in the WORK LIST.

## STEP 0 — PLAN MODE FIRST (mandatory)

Enter plan mode immediately. Do **not** create a branch, write code, edit files, or send any live
request that *mutates* state until the plan is approved. Two things happen in plan mode, in order.

### 0a — Prompt me for the live resources + the test bed (do this FIRST)

Phase 6 is **mostly offline**. There are **three live-gated test areas** — FlareSolverr CF retest,
per-indexer proxy end-to-end, and the deferred Phase-5 auth retests — sourced via **four intake
questions** (the Phase-5 test bed is a reconfirm, not new infra). Before planning, prompt me (use
AskUserQuestion / direct questions) for, and securely intake, what is available:

- **FlareSolverr** — base URL incl. scheme + port (e.g. `http://flaresolverr:8191`) of a reachable
  instance, plus **a real Cloudflare-gated tracker I have an account on** to live-retest the solver. (In
  the Phase 5 env neither existed — trackers were restricted to non-CF and no FlareSolverr ran.)
  FlareSolverr runs a real headless Chromium per session (a multi-hundred-MB container) — I may not have
  one; if so, the solver still ships on its offline gate (see below).
- **A reachable proxy** — an HTTP and/or SOCKS5 (and SOCKS4 if available) proxy URL the daemon can dial,
  to end-to-end verify per-indexer proxying. Note whether it requires `user:pass` auth.
- **Deferred Phase-5 auth patterns** (retest-only — the code already exists): a clean **user/pass
  form-login** tracker; a **cookie / 2FA (manual-cookie)** tracker; optionally a **.NET-quirk** tracker
  whose inputs exercise `*()'!` / unicode / regexp2. Names only first; confirm before I hand over creds.
- **The Phase 5 test bed** — confirm the same Sonarr/Radarr + Prowlarr (differential oracle) + qBittorrent
  wiring from Phase 5 is still available for any live smoke re-run.

For each resource I cannot supply on the day, record the corresponding item as
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

### 0b — Produce ONE complete plan for the entire Phase 6 work stream (both PRs)

Plan all work-list items end to end across both PRs (not just the first). Pressure-test the plan with a
validator/architect agent (and revise), then present it with ExitPlanMode for my approval and wait.

The architecture decisions below are **already decided** (operator-locked); present them so I can confirm
or redirect, but do not re-open them as undecided forks. The plan must cover:

- **Architecture decisions (DECIDED — confirm or redirect):**
  - **Two-PR split, ctx-threading first (decided).** PR #1 is the ctx refactor only; PR #2 is items 2-7.
    Budget each PR independently against the 150-file cap; state the merge order (PR #1 merges before
    PR #2 opens).
  - **Context threading is the enabling refactor, done FIRST in PR #1 (decided).** Thread a
    request-scoped `ctx` from the Torznab handler (it has `r.Context()` at
    `internal/web/torznab/handler.go:85`) down through `torznab.Provider.Indexer` /
    `torznab.Indexer.Search` → `Engine.Search` / `Engine.Test` → login / search / `Solver.Solve`. This
    removes the three hard-coded `context.Background()` request-path sites — `login/solver.go:84` (the
    solver call), `login/login.go:168` (`NewRequestWithContext`), `search/request.go:325` — and
    `registry.go:109`'s `r.resolve(context.Background(), slug)`. It touches the `torznab.Provider` /
    `torznab.Indexer` interfaces (and their fakes) and ~7 engine/login/search signatures
    (`Engine.Search`, `Engine.Test`, `EnsureLoggedIn`, `Login`, `search.Execute`, `doRequest`, the
    `Executor` `do`/`get`/`fetchLandingPastAntiBot` chain). **Asymmetry to reuse, not re-invent:**
    `Registry.Test(ctx, slug)` (`manage.go:269`) ALREADY threads a request `ctx` to `build()` — only
    `engine.Test()` drops it; have `Engine.Test(ctx)` consume that existing ctx (symmetric with Search),
    don't greenfield it. **Scope guard:** the goal is the three named search-path sites + `registry.go:109`
    ONLY. Four OTHER `context.Background()` sites are off-path and **correct — leave them**:
    `database/db.go:66` (startup ping), `server/server.go:96` (graceful-shutdown timeout), and
    `database/sessionstore.go:60/65/70` (scs.Store adapters — the scs library owns that ctx; "fixing"
    them breaks the scs.Store interface). PR #1 also folds in the stale-label fix (next bullet), since it
    touches the same files at zero behavior cost.
  - **Stale "Phase 4" solver labels → "Phase 6" (folded into PR #1).** `grep -rn 'Phase 4'
    internal/indexer/cardigann/login` and fix ALL of them — there are ~5-6, not 3: `login.go:24-25`
    (`ErrCaptchaRequired` doc), `login.go:27-28` (`ErrSolverRequired` doc), `login.go:81` (`checkCaptcha`
    doc), `login.go:236` (`detectAntiBot`/`cloudflareMarkers` doc), `form.go:18`. Do NOT hand-fix a list
    of three; grep so none are missed. (Pure doc-string change, zero behavior.)
  - **Rate-limiter = `golang.org/x/time/rate`, keyed per tracker HOST (decided — matches qui).** Mirror
    qui's `sharedLimiters` pattern (`internal/services/crossseed/gazellemusic/client.go`): a process-wide
    `sync.Map[host]*rate.Limiter`, lazily created via `LoadOrStore`, `rate.NewLimiter(rate.Every(interval),
    1)` (burst 1), gated with `limiter.Wait(ctx)`. **No eviction** — the key space is bounded by configured
    hosts, so a plain process-wide map cannot grow unboundedly and there is no evict-vs-`Wait` race (this is
    why per-host beats per-instance here). Rate values come from the def's rate-limit metadata, not a
    hardcoded table. **Backoff = `github.com/avast/retry-go`** (qui's retry lib) on **429/503**, bounded
    attempts, honoring `Retry-After` (compute the delay from the header, feed it as retry-go's per-attempt
    delay). **Both `golang.org/x/time/rate` and `avast/retry-go` are NEW deps** (neither is in harbrr's
    `go.mod` today — only `golang.org/x/net` is); `go get` both and update `go.sum` + the 5 cross-builds.
    **Determinism (important):** `x/time/rate.Limiter.Wait` exposes **no injectable clock** — do NOT write
    a "fake clock" test of `Wait`. Test limiter spacing via `Reserve().Delay()` arithmetic (deterministic),
    test backoff via a controllable sleep/clock seam in the retry wrapper, and test ctx-cancellation by
    cancelling during `Wait` AND during a backoff delay.
  - **Composed cancellation + request budget (decided — new risk closed).** Within one logical request the
    order is: acquire a limiter token (`Wait(ctx)`) → issue → on 429/503 sleep backoff → loop → re-acquire a
    token. Therefore: the backoff sleep MUST be ctx-aware (`select` on `ctx.Done()` vs the delay); each
    retry MUST re-acquire a limiter token (never retry token-free, or you defeat the rate limit); and the
    per-request **timeout bounds the SUM** of all Waits + sleeps, not each step. `x/time/rate` cancels its
    reservation when `Wait`'s ctx is cancelled (no token leak) — assert this. Add a test that cancels ctx
    during a `Wait` and during a `Retry-After` backoff and asserts prompt abort with no leak.
  - **Timeouts (decided).** Extend the existing `registry.WithTimeout` / `newDoer(timeout)` knob to a
    per-instance request timeout (preserve a single clamp point — `newDoer` currently re-clamps `<=0` to
    `defaultHTTPTimeout=60s`; don't lose that). The FlareSolverr HTTP-client timeout is **strictly greater
    than** its `maxTimeout` (default 60s, allow up to 180s like Prowlarr) — a CF solve legitimately takes
    many seconds.
  - **`doerFactory` widening via a `ClientParams` struct (decided).** Today `doerFactory func() (search.Doer,
    error)` is **nullary** (`registry.go:43`), called once per `build()` (`registry.go:166`) →
    `cardigann.WithDoer(doer)`, so it can't vary the client per instance. Widen it to take a single
    **`ClientParams` struct** (e.g. `{Instance domain.IndexerInstance; Cfg map[string]string}`) — NOT
    positional args — so adding the proxy/solver client later never re-breaks the signature again. This is a
    **breaking change to the public `WithDoerFactory` Option**: every caller (the production default in
    `New()` + every test that injects an offline replay `Doer`) must update in lockstep, in the same commit
    (a stale nullary closure will not compile). Both the proxy client and the FlareSolverr fetch client ride
    this one seam. (Lands in PR #2; PR #2 updates the replay-Doer test helper PR #1 introduced.)
  - **Proxy config schema (decided).** Per-instance settings `proxy_type` (`http`|`socks4`|`socks5`) +
    `proxy_url`, with any embedded `user:pass` treated as a **secret** (encrypted) setting. HTTP via
    `Transport.Proxy = http.ProxyURL`; SOCKS via `golang.org/x/net/proxy.SOCKS5`/`FromURL` wired through
    `Transport.DialContext` (cast to `proxy.ContextDialer`). `golang.org/x/net` is **already** a dep, so the
    `proxy` subpackage adds no module-graph change. Note `net/http`'s env-proxy ignores socks5 — you must set
    an explicit `Transport`. **Proxy-URL redaction caveat:** `RedactURL` redacts only the userinfo *password*
    and **preserves the username** (`redact.go:85-92`); decide whether to scrub the whole userinfo for proxy
    URLs (default: yes, scrub the whole userinfo for proxy URLs).
  - **Health-event storage (decided).** An **append-only** `indexer_health_events` table in a new migration
    `0002_indexer_health.sql`, FK `instance_id INTEGER ... REFERENCES indexer_instances(id) ON DELETE
    CASCADE` (copying the `indexer_settings` precedent at `0001_init.sql:47`), a `(instance_id, occurred_at)`
    index, `occurred_at` RFC3339 TEXT (the universal convention — `instances.go:22-23`). CASCADE **is
    enforced** (`_pragma=foreign_keys(ON)` at `db.go:101`, proven by `TestPragmasApplied`) — still add a
    test that seeds an instance, writes health rows, deletes the instance, and asserts the rows cascade. Do
    NOT add status columns to `indexer_instances`. `kind` enum = the four plan.md categories `auth_failure |
    rate_limited | parse_error | anti_bot`; `detail` is **scrubbed** (lift `sanitizeTestError` into a shared
    helper — see redaction). The repo is a **stateless empty struct** `type Health struct{}` (NO constructor)
    whose methods take `dbinterface.Execer` and route every placeholder query through `q.Rebind(...)`
    (mandatory — enforced by `rebind_guard_test.go`); mirror `database.AppMeta`. Read API: a **single** new
    `GET /api/indexers/{slug}/status`. A fleet-wide `GET /api/indexers/status` is **out of scope** this PR.
  - **Health classification + mint sites (decided).** `ErrLoginFailed` → `auth_failure`; `ErrSolverRequired`
    / `detectAntiBot` → `anti_bot`. **There is NO `rate_limited` or `parse_error` sentinel today, and
    `checkErrors` maps ONLY HTTP 401** (form/post, `methods.go:155`) — there is **no** 429/503/`Retry-After`
    handling anywhere. So both are net-new typed errors with named mint sites: `rate_limited` is minted at
    the `doRequest`/`checkErrors` non-2xx boundary on 429/503 (today `doRequest` returns a generic
    `fmt.Errorf` with the status only in the string — change it to carry a typed, status-bearing error the
    registry can classify); `parse_error` is minted at the normalizer/selector failure boundary. **Verify
    `parse_error` has a REAL code path** — the engine is designed to *degrade* (return empty/partial, not
    error) on bad markup, so confirm a genuine error-producing path exists rather than only a test stub.
  - **Key rotation = the command holds old + new keys explicitly (decided).** Today `Keyring` holds a
    **single** key and `Decrypt` always uses the active key (a row whose `key_id` ≠ active just fails the GCM
    open) — so the rotation command takes **both** keys as inputs; the `Keyring` crypto core is **untouched**
    (do NOT add a dual-key Keyring). Flow: a `harbrr` subcommand (offline — run with the daemon stopped, as
    the canary is verified once at startup) **dry-runs first**, decrypting EVERY `indexer_settings` secret
    row under the OLD key (fail loud before any write if any fails); then in **ONE SQLite transaction**
    rewrites every row's `value_encrypted` + `key_id` (re-sealed under the SAME AAD `<instanceID>\x00<setting>`
    so it still decrypts) AND the `app_meta` canary (`EncryptCanary` → key `secrets_canary`) AND `app_meta`
    key `secrets_key_id` — together, atomically. Plaintext-mode stores (`key_id`=`"plaintext"`, pass-through
    crypto) are an **error/no-op** for rotation (state which). Carry the §9 invariants: decrypted creds never
    reach logs / errors / the rotation log.
  - **FlareSolverr consume-vs-replay = discard-and-replay (decided — like Prowlarr).** Take `solution.cookies`
    + `solution.userAgent`, then re-issue the real request yourself. cf_clearance is **UA-bound**, so the
    replay MUST carry `solution.userAgent` AND a browser-realistic header set (mirror `Accept` /
    `Accept-Encoding` — a gzip-only header set is a known 403 trigger). Use a **typed** `/v1` request/response
    model (no `map[string]any`). FlareSolverr's base URL + `maxTimeout` arrive as **new cfg settings**
    (e.g. `flaresolverr_url`) consumed by `SolverOption(cfg)` (its only input channel today, `engine.go:105`);
    add a `solver_type=flaresolverr` branch. A solver failure maps to an `anti_bot` health event (co-designed
    with the health item). **The offline stub-`/v1` test asserts the replay HEADER CONTRACT** (UA + non-gzip
    Accept set), not that real Cloudflare accepts it.
  - **Redaction audit (decided — stated once, canonically).** There is **no tracing and no stats/event-log
    subsystem** in the repo and you must **NOT create one** — those audit targets are vacuous (record the
    absence as a known divergence). The audit therefore covers EXACTLY: (1) every existing log/error site
    (already routed through `RedactURL`/`RedactHeader`, incl. the already-present `Proxy-Authorization`
    header); (2) the **new FlareSolverr request/response JSON bodies** (`cookies`, `postData`, `userAgent`,
    cf_clearance) — `RedactURL`/`RedactHeader` **cannot** scrub JSON bodies, so build a NEW body scrubber in
    `internal/http/redact.go`; (3) proxy-credential URLs (per the proxy bullet — scrub the whole userinfo);
    (4) the new `indexer_health_events.detail` (lift `sanitizeTestError` out of `web/api` into a shared
    chokepoint). There is no central redacting logger — redaction is per-call-site, so the audit must *prove*
    each new site wraps secrets.
- **Package/file layout** — every package/file to create or modify, each file's responsibility, split by PR:
  - **PR #1 (ctx threading):** `internal/web/torznab` (handler + `torznab.Provider`/`torznab.Indexer`
    interfaces + their fakes — NOTE this is the *handler/interface* package; the serializer at
    `internal/torznab`, a different same-named package, is **not** touched), `internal/indexer/registry`,
    `internal/indexer/cardigann` engine + login + search; the folded "Phase 4"→"Phase 6" label fix. The
    nullary `doerFactory`/`WithDoerFactory` seam and its replay-Doer test helper are left untouched (PR #2
    widens them to `ClientParams`).
  - **PR #2 (the four boxes + FlareSolverr):** a rate-limiter/backoff collaborator (x/time/rate +
    avast/retry-go); `internal/indexer/registry/client.go` `newDoer` transport + proxy wiring + the widened
    `doerFactory`/`ClientParams`; `internal/database/migrations/0002_indexer_health.sql` + a stateless
    `database.Health` repo + `internal/domain` health model; the `GET /api/indexers/{slug}/status` handler +
    `internal/web/swagger/openapi.yaml` path + `components/schemas`; the FlareSolverr `Solver` impl + its
    `SolverOption` wiring + the `solver_type=flaresolverr` value + new cfg keys; the key-rotation command in
    `internal/secrets` + `cmd/harbrr`; redaction extensions in `internal/http/redact.go` (JSON-body scrubber
    + lifted `sanitizeTestError`).
- **The LIVE vs OFFLINE split per item** — which items are gated by committed **offline deterministic
  tests** (everything: limiter spacing via `Reserve().Delay()` + bounded backoff honoring `Retry-After`,
  ctx propagation/cancellation, the health-event taxonomy + status API over synthetic engine errors, proxy
  doer construction, key rotation via two keys + canary verify, the FlareSolverr client against a **stub
  `/v1` server**, redaction fixtures) and which need the **operator-supplied live resources** (FlareSolverr
  CF retest, proxy end-to-end, the deferred Phase-5 auth retests). CI stays fully offline.
- **Test strategy per item → oracle mapping**; **commit/box sequencing** (see the box rule in HARD RULES).
- **Risks + mitigations** — see the RISKS section; carry them into the plan with concrete tests.

After I approve via ExitPlanMode, leave plan mode and execute the PER-ITEM LOOP below.

## READ FIRST

`AGENTS.md`; `docs/plan.md` (Phase 6 — the four boxes — **+ the Phase 5 execution-protocol blockquote**
at lines 102-109 for the live-resource discipline); `docs/ideas.md` **§9 (Security model)** + the
fetch/auth + indexer-proxy capability rows (note: these are short capability-table rows + §12 archetype
bullets, NOT a deep design section — the proxy schema / FlareSolverr protocol live in THIS prompt, not
ideas.md); `docs/architecture.md` — **invariant #3** (the Torznab *arr-facing contract `internal/torznab`
and the management OpenAPI surface `internal/web/swagger` stay SEPARATE — the new `GET …/status` route
lives ONLY on the management tree) and **invariant #5** (SQLite only, no Postgres). `docs/divergences.md`
is the divergence-ledger **index** (it is NOT a flat append target — see AFTER ALL ITEMS); `docs/highlights.md`
is the honestly-labelled feature log. Read the seams this work builds on:
`internal/indexer/registry/registry.go` (the nullary `doerFactory` field at :43 + `WithDoerFactory` option
at :77-83 + `build()` wiring at :166 + `logResolveError` redaction at :208-216) and `client.go` (`newDoer`
at :24, `defaultHTTPTimeout=60s`); `internal/indexer/registry/manage.go` (`Registry.Test(ctx,…)` at :269 —
already ctx-plumbed to `build()`); `internal/indexer/cardigann/login/solver.go` (the `Solver` interface at
:17 + `NoopSolver`/`ManualCookieSolver` + `fetchLandingPastAntiBot` at :76 + the `context.Background()`
call site at :84 + the `[Tracked: Phase 6 — FlareSolverr solver]` note at :16) and `login.go` /
`methods.go` (`ErrLoginFailed`/`ErrSolverRequired`/`ErrCaptchaRequired` + `detectAntiBot`/`cloudflareMarkers`
at :237-255 + `checkErrors` at `methods.go:146-179` — **maps 401 ONLY, no 429**); `engine.go`
(`WithSolver` at :94, `SolverOption(cfg)` at :105 reading `cfg["solver_type"]`); `search/request.go`
(`doRequest`'s ctx gap at :325 + non-2xx fail-fast at :342-344, generic error — no status-bearing type);
`internal/secrets/keyring.go` + `aead.go` + `canary.go` (`Encrypt`/`Decrypt`, single-key `Keyring`,
`deriveKeyID`=`hex(SHA-256(key))[:8→16]`, the AAD `<instanceID>\x00<setting>`, `EncryptCanary`/`VerifyCanary`
— verify-only, never re-seals) and `cmd/harbrr/serve.go` (`verifyCanary` at :86/:147; `app_meta` keys
`secrets_canary`/`secrets_key_id` at :33-37); `internal/database/appmeta.go` (the stateless `AppMeta`
repo — the template for `Health`); `internal/database/instances.go` (`type Instances struct{}` stateless,
methods take `dbinterface.Execer`, `q.Rebind`, RFC3339 `timeLayout`) + `migrations/0001_init.sql` +
`migrate.go` (embed.FS + lexical-order + `schema_migrations` bookkeeping — adding `0002_*.sql` needs no
code change) + `db.go:101` (`foreign_keys(ON)`); `internal/web/api/router.go` (chi router; the inner
authed group at :96-115 — `resolveAuth`+`requireAuth`; **flat** route patterns, per the :79-80 comment, so
they match the OpenAPI path strings) + `indexer_handlers.go` (`testIndexer` + `sanitizeTestError` at
:194/:225) + `internal/web/swagger/openapi.yaml` (the spec; copy the `/api/indexers/{slug}/test` entry at
:292) + the drift tests — **the bidirectional route⇔spec gate is `internal/web/api/router_test.go`
(`TestOpenAPIDriftRoutesMatchSpec`), NOT `swagger/openapi_test.go`** (which only does structural OpenAPI-3
validation + a `/healthz` spot-check); `internal/http/redact.go` (`RedactURL`/`RedactHeader`, the
5-header set incl. `Proxy-Authorization`); `internal/server/server.go` (the two separate handler mounts:
`/api/v2.0/indexers/*`→Torznab, `/*`→Management); and the degradation baselines — `internal/indexer/
cardigann/degrade_test.go` (engine-surface passkey redaction + parse-without-panic) AND `internal/web/
torznab/handler_test.go` (the `<error>` HTTP-status policy: 201 at HTTP 200, generic 900 at HTTP 500,
empty feed) — the prompt's "follows the clean-degradation error-status policy" spans BOTH, not just
`degrade_test.go`.

Phase 6 hardens the **already-live** daemon (Phases 4–5 shipped) so it survives real operation without
getting a tracker IP/account blacklisted, surfaces *why* an indexer is unhealthy, can route per-indexer
through a proxy, can rotate its encryption key, and can clear Cloudflare via FlareSolverr. It does **NOT**
touch the parity engine's normalized output, the Torznab serializer contract (`internal/torznab`), the
vendored definitions, or any Phase 7/8 surface; it does **NOT** complete the download resolver or build
the web UI.

## CONTEXT (Phase 5 shipped — the daemon, proven LIVE)

- `harbrr serve` is a real daemon: SQLite + migrations, the §9 secrets store (AES-256-GCM tracker creds
  with per-record nonce + AAD=`<instanceID>\x00<setting>` + stored `key_id`, argon2id password, SHA-256
  API keys, auto-keyfile, fail-loud startup **canary**), first-run setup + login + `X-API-Key`, the
  indexer-instance registry as the production `torznab.Provider`, the management API (`/api/indexers`,
  `/api/apikeys`, `/api/auth/*`), and the Torznab handler at `/api/v2.0/indexers/...`. Phase 5 closed the
  MVP live (5 non-CF trackers, search → grab, Prowlarr differential) and landed lazy login, the .NET URL
  encoder, category filtering, the served download link, and the indexer **Test** action.
- **Seams Phase 6 builds on (already in place, do NOT re-invent):**
  - `registry.WithDoerFactory(fn func() (search.Doer, error))` + the **nullary** `doerFactory` field
    (`registry.go:43,77-83`), called once in `build()` (`registry.go:166`) → `cardigann.WithDoer(doer)`.
    Widening its shape (to a `ClientParams` struct) is the net-new part. `newDoer(timeout)` (`client.go:24`)
    builds a bare `http.Client{Jar,Timeout}` — **no Transport / proxy / retry** (so it inherits
    `http.DefaultTransport`, which honors HTTPS_PROXY env but ignores socks5 env). `defaultHTTPTimeout=60s`;
    `WithTimeout` exists. `Registry.Test(ctx,…)` (`manage.go:269`) already threads ctx to `build()`.
  - `login.Solver` interface (`solver.go:17`, `Solve(ctx, targetURL) (SolveResult{Cookies,UserAgent}, err)`)
    with `NoopSolver` (default, returns `ErrNoSolverConfigured` → fail loud) and a functional
    `ManualCookieSolver`. Threaded via `cardigann.WithSolver` / `SolverOption(cfg)` (`engine.go:94,105`,
    selecting on `cfg["solver_type"]`) → `buildLogin` → `Executor.Solver`. `fetchLandingPastAntiBot` already
    detects anti-bot, solves ONCE, seeds cookies, retries with the solver UA, and fails loud if still
    challenged. **The FlareSolverr impl is the only missing piece** (`solver.go:16`).
  - The login failure taxonomy classifies: `ErrLoginFailed` (auth), `ErrSolverRequired` +
    `cloudflareMarkers`/`detectAntiBot` (anti-bot, CF-substring matchers only), `ErrCaptchaRequired`,
    `ErrNoSolverConfigured`. **`checkErrors` maps HTTP 401 ONLY** (form/post, `methods.go:155`); there is
    **no** 429/503/`Retry-After` handling and **no `rate_limited`/`parse_error` sentinel** — both are net-new
    for the health item.
  - Secrets substrate for rotation is ready: every secret row stores its `key_id` (`instances.go:52,81-98`;
    `validateSettingInvariant` requires a non-empty `key_id` + no plaintext value for secrets, but ALLOWS an
    empty `value_encrypted` for an empty secret — `instances.go:227-241`); `deriveKeyID`=`hex(SHA-256(key))`
    first 8 bytes; the AAD binds each ciphertext to one `(instance, setting)` row; the canary detects a key
    change and **fails loud** but does **not** re-encrypt; the meta lives in `app_meta` (keys
    `secrets_canary`/`secrets_key_id`). `Keyring` holds a **single** key. **No rotation flow exists** —
    net-new. (qui, the sibling project, has NO rotation and no AAD/key_id at all — harbrr is ahead here, so
    there is no pattern to copy.)
  - Redaction chokepoint: `RedactURL` (secret query params + userinfo **password only** + unparseable
    fallback) and `RedactHeader` (Authorization/Cookie/Set-Cookie/X-Api-Key/**Proxy-Authorization**), used at
    the torznab handler, registry, login, and search error/log sites. **No central redacting logger** —
    per-call-site; **no tracing / stats-event-log subsystem** exists; JSON bodies are not covered by either
    helper.
  - Clean degradation (Phase 2/4, test-gated): `degrade_test.go` proves a transport error never leaks the
    passkey + parse-degrades-without-panic; `internal/web/torznab/handler_test.go` proves the `<error>`
    status policy (unresolved indexer → Torznab `<error>` 201 at HTTP 200; search/internal error → a
    **redacted** generic 900 at HTTP 500; no-results → a valid empty feed). Phase 6's safety additions follow
    this error-status policy.

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
  committed secrets. The FlareSolverr solver itself is offline-tested against a **stub `/v1` server**; the
  FlareSolverr ledger item closes on that offline gate — the live CF clear is `[Tracked: …]` confidence
  evidence, **never a merge blocker** (operators without FlareSolverr are expected). CI stays fully
  **offline and deterministic**.
- **Never edit vendored definitions** under `internal/indexer/definitions/vendor/` (consumed byte-for-byte
  from Jackett; a PreToolUse hook blocks it). All behavioral differences are absorbed in the engine; fixes
  go upstream or to `internal/indexer/definitions/dropin/`.
- **Secret redaction stays absolute** — never log/print/commit a passkey, cookie, cf_clearance, proxy
  password, API key, or download token. Extend redaction to the new FlareSolverr/proxy surfaces (incl. a
  JSON-body scrubber) and scrub the new `indexer_health_events.detail`. Synthetic test secrets live ONLY in
  `*_test.go` / `testdata/**`, kept in sync across `scripts/check-no-secrets.sh` and `.gitleaks.toml`.
- **Key rotation must be atomic + fail-loud** — the command holds old+new keys explicitly; **dry-run
  decrypts every row under the old key before any write**; one SQLite transaction rewrites every secret row
  + the canary + meta `secrets_key_id` together; a wrong/missing old key fails loud before any write. Carry
  the §9 invariants: login password stays unrecoverable; decrypted creds never reach logs / errors / Torznab
  responses / a rotation log.
- **SQLite only**; pure-Go driver; **two HTTP contracts stay separate** (invariant #3 — `GET …/status`
  lives only on the management tree, never the Torznab tree); OpenAPI changes → `make test-openapi`. Carry
  **every** Phase 4 and Phase 5 hard rule forward.
- NO AI attribution/co-author/"Generated with" lines. Conventional commits; gofumpt-clean; interfaces ≤5
  methods; no `map[string]any` for structured data (typed FlareSolverr request/response + health + proxy +
  `ClientParams` structs); split god-functions (funlen/gocyclo/gocognit/nestif). Before EVERY commit:
  `make precommit` + `make build` green; tests always `-race -count=1`.
- **Branches & box rule.** PR #1 off `main`: `phase6/ctx-threading`. PR #2 off `main` (after #1 merges):
  `phase6/operational-safety`. NEVER touch main (protected; required checks: `test`, `build`, the five
  `cross-build (...)`, `secret scan`; lint + CodeQL also run). **Box rule:** box-bearing items (WORK LIST
  2-5) tick their `docs/plan.md` box in the SAME commit, only when its tests are green. **Enabling-infra
  (item 1 ctx threading), the FlareSolverr solver (item 6 — closes the `[Tracked: FlareSolverr]` ledger
  item only), and retest-only items (item 7) tick NO box — say so in the commit message.**

## ORACLE / FIXTURES (decided): OFFLINE + deterministic, with operator-resourced LIVE retests gated out of CI

- **Offline deterministic** (committed; runs in CI — the gate for all four plan.md boxes):
  - **Timeouts / backoff / rate limits**: per-host `rate.Limiter` paces calls (assert spacing via
    `Reserve().Delay()` arithmetic — NOT a fake clock; `x/time/rate.Wait` has no injectable clock); backoff
    (avast/retry-go) fires on **429/503** and honors `Retry-After`, with a bounded retry count (never loops);
    a request deadline cancels via the threaded `ctx` (prove propagation + cancellation over a replay
    `Doer`/timeout, incl. cancelling DURING a `Wait` and DURING a backoff delay, with no token/reservation
    leak). The per-host limiter map is process-wide and **not** evicted (bounded key space).
  - **Context threading**: a cancelled `ctx` at the handler aborts the solver/login/search call (no
    `context.Background()` at the three named search-path sites + `registry.go:109`; the four off-path sites
    stay); table-driven cancellation tests.
  - **Health & status**: synthetic engine errors map to the four `kind`s (`auth_failure` from
    `ErrLoginFailed`, `anti_bot` from `ErrSolverRequired`/`detectAntiBot`, plus the new `rate_limited` minted
    at the `doRequest`/`checkErrors` 429/503 boundary and `parse_error` at the normalizer boundary); events
    persist append-only; the migration applies on a **fresh DB AND on an already-migrated Phase-5 DB** (seed
    0001 + instance rows, apply 0002), and a parent-instance delete **cascades** the health rows (CASCADE is
    enforced — `foreign_keys(ON)`); `GET /api/indexers/{slug}/status` returns the derived status with a
    **scrubbed** `detail` (a passkey/cookie never lands in the DB or the response); the **bidirectional**
    OpenAPI drift test (`router_test.go::TestOpenAPIDriftRoutesMatchSpec`) passes for the exact path
    `/api/indexers/{slug}/status`; `make test-openapi` green.
  - **Per-indexer proxy**: the widened `doerFactory`/`newDoer` (via `ClientParams`) builds the right client
    per `proxy_type` (HTTP via `Transport.Proxy`, SOCKS via `x/net/proxy` → `DialContext`); proxy-URL parsing
    + a bad proxy config fails loud; the proxy password is encrypted at rest and never logged (whole userinfo
    scrubbed in any URL that reaches a log).
  - **Key rotation**: encrypt with key A → rotate to key B → every row's `value_encrypted` + `key_id` is
    rewritten and decrypts under B (same AAD); the canary + meta `secrets_key_id` update to B; a wrong old
    key fails loud **before** any write (dry-run); an empty-secret row (key_id present, value_encrypted
    empty) rotates without tripping `validateSettingInvariant`; plaintext-mode is a no-op/error; the rotation
    log is secret-free.
  - **FlareSolverr solver**: against a **stub `/v1` HTTP server**, `request.get` round-trips a typed
    request/response; `solution.cookies` + `solution.userAgent` flow into the seam and the replay carries the
    UA AND a non-gzip-only `Accept`/`Accept-Encoding` set (the test asserts this **header contract**, not
    real-CF acceptance); a non-`ok` status / timeout fails loud and maps to an `anti_bot` health event; the
    solver wires into `SolverOption` under `solver_type=flaresolverr` (base URL via a new cfg key).
  - **Redaction**: fixtures cover a FlareSolverr request body (`cookies`+`postData`), a response `solution`
    (cf_clearance) via the new JSON-body scrubber, a SOCKS proxy URL with embedded `user:pass` (whole
    userinfo scrubbed), and the new health-event `detail` — each scrubbed end-to-end. The audit asserts every
    log/error/persisted site routes through a chokepoint.
- **Operator-resourced LIVE retests** (manual / build-tagged; **never** in CI): FlareSolverr clears a **real
  Cloudflare tracker** end-to-end (search → result, optionally grab); a per-indexer **proxy** routes a real
  search; the deferred Phase-5 **form-login**, **cookie/2FA**, and **.NET-quirk** patterns retest live. Each
  item with no resource on the day is **DEFERRABLE-with-disposition** (`[Tracked: …]`).
- **Live evidence** is captured in the PR body / a Phase 6 testdata README (per-resource pass/fail, the CF
  solve proof, the proxy proof) — **NOT** committed creds, NOT raw unscrubbed live responses.

## WORK LIST — items in dependency order, mapped to the two PRs

**PR #1 — `phase6/ctx-threading` (ticks no plan.md box):**

1. **Context threading** (enabling refactor): thread a request-scoped `context.Context` from the Torznab
   handler (`internal/web/torznab/handler.go`) through `torznab.Provider`/`torznab.Indexer`,
   `Engine.Search`/`Engine.Test` (reusing `Registry.Test`'s existing ctx), login, search, and `Solver.Solve`,
   removing the three search-path `context.Background()` sites (`login/solver.go:84`, `login/login.go:168`,
   `search/request.go:325`) and `registry.go:109` (leaving the four off-path sites). Fold in the stale
   "Phase 4"→"Phase 6" solver-label grep-fix (same files, zero behavior). The nullary `doerFactory` seam is
   left as-is here (PR #2 widens it). Purely offline; unblocks timeouts/backoff (item 2), proxies (item 4),
   and the FlareSolverr network call (item 6).

**PR #2 — `phase6/operational-safety` (the four boxes + FlareSolverr), built on PR #1:**

2. **Timeouts, backoff, per-host rate limits** (anti-blacklist): per-request timeouts, retry backoff
   (avast/retry-go) on 429/503 (honor `Retry-After`, bounded), and a per-**host** `rate.Limiter`
   (x/time/rate, process-wide `sync.Map`, no eviction) so harbrr never gets an IP/account blacklisted, with
   the composed-cancellation budget. *(plan.md "Timeouts, backoff, per-indexer rate limits" box.)*
3. **Indexer health & status**: define the four health events (`auth_failure`, `rate_limited`, `parse_error`,
   `anti_bot`) recorded from the registry (new stateless `database.Health` repo + migration
   `0002_indexer_health.sql`, mint `rate_limited`/`parse_error` typed errors at their named boundaries), and
   surface per-indexer status via a new `GET /api/indexers/{slug}/status` (registered FLAT in the
   authenticated group; spec + `components/schemas` + the bidirectional drift test in the SAME commit).
   *(plan.md "Indexer health & status" box.)*
4. **Per-indexer proxies** (HTTP / SOCKS4 / SOCKS5): configure a proxy per indexer instance via the widened
   `doerFactory`/`ClientParams`/`newDoer` (`Transport.Proxy` for HTTP; `x/net/proxy` → `DialContext` for
   SOCKS), proxy config as per-instance settings (URL/password encrypted). Live-retest if a proxy is
   supplied, else `[Tracked: …]`. *(plan.md "Per-indexer proxies" box.)*
5. **Secret hardening**: (a) **key rotation** — an offline command holding old+new keys that dry-runs then
   atomically re-encrypts every secret row via its stored `key_id` (rewrite `value_encrypted`+`key_id`+canary
   +meta `secrets_key_id` in one tx, fail-loud); (b) the **redaction audit** end-to-end (new FlareSolverr
   JSON-body scrubber + proxy-userinfo scrub + lifted `sanitizeTestError` for `health_events.detail`; note
   traces/stats-event-log are absent so vacuous — do NOT build them). *(plan.md "Secret hardening" box.)*
6. **FlareSolverr solver** (ticks NO box — closes the `[Tracked: Phase 6 — FlareSolverr solver]` ledger
   item): implement the FlareSolverr `Solver` behind the existing `login.Solver` seam (typed `/v1`
   request/response, discard-and-replay, UA-coupled + non-gzip replay), wire it into `SolverOption` under
   `solver_type=flaresolverr` with a new base-URL cfg key, and (co-designed with item 3) map a solver failure
   to an `anti_bot` health event. Offline-tested against a stub `/v1` server (header contract); live-retest a
   real CF tracker if FlareSolverr + a CF tracker are supplied, else `[Tracked: …]`.
7. **Deferred Phase-5 auth-pattern live retests** (retest-only — code exists; ticks no box): user/pass
   **form-login**, **cookie / 2FA (manual-cookie)**, and the **.NET-quirk** (`*()'!`/unicode/regexp2)
   patterns, each live-confirmed if the operator supplies a tracker, else **DEFERRABLE-with-disposition**
   (`[Tracked: …]`). **If no auth tracker is supplied, this item produces NO code commit** — record it only
   as `[Tracked: …]` in the Phase 6 README + the divergences-table row; do NOT manufacture an empty commit.
   Any bug surfaced by these retests is `[Tracked: …]`, **not** fixed here (the parity engine is frozen).

**Explicitly OUT of scope — separate follow-on PRs (one line each, do NOT build here):**
- Download resolver completion / full `/dl` proxy — `[Tracked: Phase 7 — download resolver]`
- XML backend edge parity (CDATA / mixed-namespace / AngleSharp edges) — `[Tracked: Phase 7 — XML edge parity]`
- Native Avistaz family — `[Tracked: Phase 7 — Avistaz]`
- Backup / restore (config + DB; redacted/encrypted export) — `[Tracked: Phase 7 — backup/restore]`
- Web UI / Swagger UI render / stats display — `[Tracked: Phase 8 — web UI]`
- \*arr app-sync, Prowlarr import, autobrr push, OIDC, Postgres — `[Tracked: Phase 8 — …]`
- A fleet-wide `GET /api/indexers/status` route — out of scope this PR — `[Tracked: Phase 8 — fleet status]`.
- A **stats event-log subsystem** (and tracing) — does not exist today, so the redaction audit treats those
  targets as vacuous; building them is out of Phase 6 — `[Tracked: Phase 8 — stats data layer]`.

## RISKS (carry into the plan with concrete tests/mitigations)

- **150-file CodeRabbit cap** — mitigated by the decided two-PR split; budget each PR independently and state
  the merge order (PR #1 before PR #2 opens). Don't open both + force-push in rapid succession (CodeRabbit
  ~1h rate-limit; it auto-reviews on PR-open).
- **`doerFactory` arity is a breaking `Option` change** — widen via a `ClientParams` struct (not positional
  args) so future fields never re-break it; update the production default + every replay-Doer test caller in
  the same commit.
- **Composed ctx-cancellation** — limiter `Wait` + backoff sleeps + per-request timeout share one
  ctx/deadline; backoff sleep is ctx-aware; each retry re-acquires a token; total bounded by the timeout; no
  reservation leak on a cancelled `Wait`. Test cancel-during-`Wait` and cancel-during-backoff.
- **Backoff / relogin loop or ignored `Retry-After`** — bounded attempts; honor `Retry-After`; never loop.
- **Limiter map** — per-host, process-wide, NOT evicted (bounded key space → no growth, no evict-vs-`Wait`
  race). Do not add eviction.
- **SOCKS dial wiring** — `net/http` env-proxy ignores socks5; set an explicit `Transport.DialContext` via
  `x/net/proxy`. Proxy-password leak — encrypt at rest, scrub whole userinfo in any logged URL.
- **Health mis-classification / secret in `detail` / OpenAPI drift** — typed mint sites for
  `rate_limited`/`parse_error`; scrub `detail`; the bidirectional drift gate is `router_test.go`.
- **Migration on a deployed DB** — test 0002 on an already-migrated Phase-5 DB (not just fresh); assert
  CASCADE actually fires (`foreign_keys(ON)`); new-repo SQL MUST use `q.Rebind` (`rebind_guard_test.go`).
- **Key-rotation half-write / wrong-key-not-caught / canary desync** — dry-run before any write; one tx for
  rows + canary + meta; offline (daemon stopped); plaintext-mode no-op.
- **FlareSolverr** — UA-coupling + gzip-header 403 (replay carries UA + non-gzip Accept set); typed `/v1`
  model (no `map[string]any`); a secret in a FlareSolverr body (JSON-body scrubber); the offline stub proves
  the header contract, NOT real-CF acceptance; a live account ban on the CF retest (gentle rate, back off).

## SUCCESS CRITERIA — assert as a gate

- harbrr paces every outbound request per **host** (rate limiter), times out, and **backs off on 429/503
  honoring `Retry-After`** without looping — proven offline (spacing via `Reserve().Delay()`; backoff under a
  controllable seam); a cancelled request `ctx` aborts the call (no `context.Background()` at the three
  named search-path sites + `registry.go:109`; the four off-path sites are unchanged).
- Per-indexer **health events** (`auth_failure`/`rate_limited`/`parse_error`/`anti_bot`) are recorded and
  surfaced at `GET /api/indexers/{slug}/status` with a **scrubbed** `detail`; the migration applies on a
  fresh DB **and an already-migrated Phase-5 DB** and CASCADE fires on instance delete; the **bidirectional**
  OpenAPI drift test (`router_test.go`) + `make test-openapi` are green; the two contracts stay separate.
- A per-indexer **proxy** (HTTP / SOCKS4 / SOCKS5) routes that indexer's traffic via the widened
  `doerFactory`/`ClientParams`/`newDoer`; the proxy password is encrypted at rest and never logged.
- **Key rotation** re-encrypts every secret row via its stored `key_id` (rewriting `value_encrypted` +
  `key_id` + canary + meta `secrets_key_id`), is atomic + fail-loud on a wrong old key (dry-run first), and
  leaves every row decryptable under the new key; the rotation path logs nothing secret.
- The **FlareSolverr solver** completes the `login.Solver` seam (offline against a stub `/v1` — header
  contract; live-cleared a real CF tracker if resourced, else `[Tracked: …]`); the stale "Phase 4" labels
  are corrected.
- **No credential** (passkey / cookie / cf_clearance / proxy password / API key) ever appears in a log,
  error, the served feed, the health-event `detail`, a FlareSolverr body, a fixture, or a commit; redaction
  holds end-to-end on the new FlareSolverr/proxy/health surfaces; the live harness needs **no committed
  secrets** and never runs in normal CI.
- `make precommit` + `make build` green; all 5 cross-builds green; contracts still separate; SQLite-only;
  **each PR ≤150 files**.

## PER-ITEM LOOP (after plan approval; one commit per item)

(a) brief per-item plan consistent with the approved master plan; (b) IMPLEMENT + table-driven tests
beside it (offline/deterministic where the behaviour allows — `Reserve().Delay()` + controllable backoff
seam for rate/backoff, stub `/v1` for FlareSolverr, two keys + canary for rotation; the live items carry
their build-tagged harness + captured evidence); (c) VERIFY `make precommit` + `make build`, `-race`;
(d) ADVERSARIAL REVIEW — ≥3 independent skeptics try to REFUTE it (per-host limiter correctness / no
growth; backoff or relogin **loop** / unbounded retry / ignored `Retry-After`; composed ctx-cancellation
not propagating or leaking a reservation; health-event mis-classification or a **secret in `detail`** /
OpenAPI drift / drift-test-in-the-wrong-file; SOCKS dial wiring / `net/http` socks5 env gap /
proxy-password or proxy-username leak; key-rotation **half-write / non-atomic / wrong-key-not-caught /
canary desync / plaintext-mode**; FlareSolverr UA-coupling + gzip-header 403 / typed-model gaps / a secret
in a FlareSolverr body; migration on a deployed DB / CASCADE inert / missing `q.Rebind`; the breaking
`doerFactory` arity orphaning a caller; any new redaction blind spot; live-rate discipline + ban risk).
Fix every confirmed issue; re-verify. (If skeptic agents die on a spend limit, fall back to rigorous inline
self-review and SAY SO.) (e) COMMIT: one focused conventional commit; tick the box in the same commit only
for box-bearing items (items 2-5) — item 1, item 6, and item 7 tick NO box (say so in the commit message).

## AFTER ALL ITEMS (per PR)

- f) END-TO-END PHASE REVIEW + completeness critic ("which timeout / backoff / rate / health-classification
  / proxy / rotation-edge / redaction site / live-resource claim is unverified?"); close gaps. Record every
  divergence with an explicit disposition in the relevant **layer testdata README** (rotation → the secrets
  README; rate/proxy/health/timeouts → a registry/network README) AND add ONE corresponding row to
  `docs/divergences.md`'s layer table pointing at it — `docs/divergences.md` is an INDEX, **do NOT free-text
  entries into it**. Divergences to record: the widened `doerFactory` `ClientParams` shape vs the Phase-4
  nullary seam; the per-**host** rate-limiter key (vs the original per-instance idea); the new
  `rate_limited`/`parse_error` classification + mint sites; the rotation transition model (command holds
  old+new keys); the corrected "Phase 4" solver labels; the absence of traces/stats-event-log; any deferred
  live retest as `[Tracked: …]`. Add the Phase 6 improvements to `docs/highlights.md` (honestly labelled
  `[shipped]`/`[partial]`/`[planned]`).
- g) KEEP EACH PR ≤150 FILES — guaranteed by the two-PR split; if PR #2 still threatens 150, split a
  self-contained box (e.g. proxies or rotation) into a third PR and note merge order.
- h) OPEN THE PR. PR #1: `phase6/ctx-threading → main` (enabling refactor, no box). After it merges, PR #2:
  `phase6/operational-safety → main`, with a summary + testing checklist + a coverage table (context
  threading, timeouts/backoff/rate limits, health & status + API + migration + drift, per-indexer proxies,
  key rotation, redaction audit, FlareSolverr solver, the deferred-auth retests — mark live-only rows
  "may be fully deferred"). No AI attribution. **No creds / proxy URLs / FlareSolverr URLs in the PR body.**
- i) CI GREEN: push, fix until all required checks pass (test, build, cross-build ×5, secret scan). CI is
  fully offline — the live retests do not run here.
- j) CODE REVIEW: let CodeRabbit's auto-review complete; address EACH finding (validate → fix + revalidate,
  or reply inline why it's skipped/intentional). Re-run CI.
- k) PAUSE: once CI + review are green, STOP. Do NOT merge. Wait for my review.

## FINAL REPORT

Items shipped (commit ids), per PR; the operational-safety surface as built vs the four plan.md boxes
(timeouts + backoff + per-host rate limits; health & status events + `GET /api/indexers/{slug}/status` +
migration `0002`; per-indexer HTTP/SOCKS proxies; key rotation + redaction audit) plus the FlareSolverr
solver and the context-threading refactor; offline test coverage by area (`Reserve().Delay()` rate +
bounded backoff, ctx cancellation, health taxonomy + drift, proxy doer construction, rotation + canary,
stub-`/v1` solver header contract, redaction fixtures); the live-retest results per supplied resource
(FlareSolverr CF clear, proxy, form-login, cookie/2FA, .NET-quirk) or each as `[Tracked: …]`; cross-build
status; explicit confirmation that no credential, proxy password, cf_clearance, or FlareSolverr secret was
logged or committed and that redaction holds on every new surface (incl. health-event `detail` and
FlareSolverr bodies); known divergences + dispositions (the `ClientParams` `doerFactory` shape, the per-host
rate-limiter key, the new health classification + mint sites, the rotation transition model, the corrected
"Phase 4" labels, traces/stats-event-log absence, any deferred live retest); and open questions.
