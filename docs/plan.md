# harbrr build plan

The executable checklist. Work **top to bottom, one item at a time**, and check a box only when its
tests are green (`make precommit` clean). Ordered by **risk retirement**, not product completeness —
the engine must prove it can match Jackett on saved inputs before any product surface is built. Full
rationale in `ideas.md`; rules in `../AGENTS.md`.

Legend: `[ ]` todo · `[x]` done · each leaf should land in its own focused commit.

---

## Phase 0 — Foundations (scaffold; mostly done)

- [x] Repo skeleton, package layout, `AGENTS.md`/`CLAUDE.md`, `.golangci.yml`, Makefile, CI, hooks
- [x] `make tools` runs clean on a fresh checkout
- [x] `make vendor-defs` populates `internal/indexer/definitions/vendor/` (pin `JACKETT_REF` to a SHA)
- [x] `make build` and `make test` green with the vendored snapshot embedded
- [x] Author the management-API `openapi.yaml` stub under `internal/web/swagger` + drift test
      (`make test-openapi`)
- [x] Wire `cobra`/`viper` entrypoint and a typed config struct (no `map[string]any`)

## Phase 1 — Engine proof (offline) — *retires the existential risk*

Build the pipeline stage by stage, each table-driven-tested with its own fixtures. Keep stages
decoupled.

- [x] **loader** — parse + schema-validate a definition into a typed model; precedence dropin > vendor
- [x] **mapper** — capabilities document + category mapping (Newznab category system)
- [x] **template** — Go `text/template` with .NET-equivalent truthiness (empty-vs-missing)
- [x] **filter** — the bounded filter registry; start with the 6 dominant ops (`re_replace`, `replace`,
      `append`, `dateparse`, `regexp`, `querystring`), then the tail
- [x] **selector** — HTML (`cascadia`/`goquery`) + JSON selection; start the standing selector fixture
      suite (vs Jackett semantics)
- [x] **dateparse** — .NET format strings → Go layout; cover timezones, relative dates, localized names
- [x] **regexadapter** — RE2 default; route to `regexp2` on opt-in / non-Latin `language:` / RE2
      compile-failure / .NET-only constructs; run both engines on shared fixtures
- [x] **login/session executor** — `form`/`post`/`get`/`cookie`, CSRF, cookie jar, re-login;
      manual-cookie fallback. Test offline against saved login sequences
- [x] **normalizer** — produce normalized release objects (canonical, deterministic JSON)
- [x] Engine assembles the stages end-to-end on a saved response

## Phase 2 — Offline parity — *the gate*

- [x] Port Jackett's GPL-2.0 Cardigann engine tests (`CardigannIndexerHtmlTests`/`JsonTests`) — fixtures
      byte-for-byte under `parity/testdata/jackett/` (+ NOTICE); `jackett_oracle_test.go` asserts
      Jackett's exact request URLs and first-release values (25 / 78 releases)
- [x] Build the differential harness (offline oracle: goldens ported from Jackett's own test
      assertions or hand-derived from documented Jackett semantics, never captured from a live
      Jackett — see `parity/testdata/README.md`; each case records `golden_source` provenance)
- [x] Wire `internal/indexer/cardigann/parity` to the real engine (replace the stub `Process`)
- [x] Pass the **compatibility matrix** offline rows (each archetype has a fixture):
  - [x] HTML / form login
  - [x] HTML / cookie login
  - [x] JSON-API
  - [x] XML / Newznab
  - [x] non-Latin-script (regexp2 path)
  - [x] freeleech (download/uploadvolumefactor)
  - [x] multi-category
  - [x] date-heavy (multiple .NET formats + relative)
  - [x] magnet-only (magnet/infohash synthesis)
  - [x] download-link pre-request
- [x] **Success criteria met:** 100% defs load w/o panic · zero silent schema failures (triaged to a
      visible skip-list) · ported Jackett tests pass · matches Jackett on ≥25 saved fixtures · secrets
      redacted in logs · broken indexers degrade cleanly

## Phase 3 — Minimal Torznab output

- [x] `internal/torznab`: capabilities document + `t=caps|search|tvsearch|movie|music|book`
- [x] **caps/category correctness is a gate** (Sonarr/Radarr failures usually trace here)
- [x] Sonarr/Radarr can search a handful of real trackers through harbrr end-to-end (Phase 5 live smoke:
      5 trackers searched live + grab into qBittorrent; see `internal/smoke/README.md`)

## Phase 4 — Daemon foundation (persistence · secrets · auth · server)

Turns the proven engine into a configurable headless daemon Sonarr/Radarr/autobrr can point at — the
critical path everything product-facing depends on, and where the `docs/ideas.md` §9 security model is
built. (Before this phase, `cmd/harbrr serve` loaded config and exited and the Torznab handler had no
production caller; this phase makes harbrr a runnable, configurable daemon — the registry is now the
production `Provider` the handler resolves through.)

- [x] **SQLite store + migrations** behind `internal/database/dbinterface` (clean interface; Postgres
      stays deferred — Phase 8). Data dir `0700`; db + all SQLite side files (`-wal`/`-journal`) `0600`
- [x] **Secrets store** (`internal/secrets`) — the three-class model from §9: tracker creds
      AES-256-GCM (per-record nonce, AAD = indexer-id + setting, stored `key_id`); web-UI password
      argon2id; API keys SHA-256. Auto-generate a keyfile on first run (encryption always on); fail
      loud on a wrong/changed key
- [x] **Indexer instance registry** — add / configure / enable / disable / delete a configured indexer
      (definition id + settings + encrypted credentials) and resolve an id → engine. This is the
      production `Provider` the Torznab handler already expects, and the core of a Prowlarr-style manager
- [x] **Management API + auth** — grow the hand-authored `openapi.yaml` past `/healthz` (indexer CRUD,
      settings, API-key management); first-run setup; server-side sessions + `X-API-Key`; CSRF on
      cookie-auth surfaces; the qui auth-disabled / trusted-proxy mode
- [x] **Wire the server** — mount the Torznab handler (`internal/web/torznab`) **and** the management
      API in `cmd/harbrr serve`; config file + base-path support
- [x] **Docker image + config file**

## Phase 5 — Live smoke (closes the MVP)

5 real trackers driven through the running daemon by an actual Sonarr/Radarr — the live half of the
Phase 3 "search real trackers end-to-end" goal.

> **Execution protocol (decided).** During the Phase 5 planning step the user hands over the **tracker
> credentials** directly (passkey/cookie/login) — they can't be lifted from Prowlarr's API, which masks
> them (see Phase 8) — and the **API keys for the *arr (Sonarr/Radarr) + Prowlarr**. The agent then
> **selects the 5 trackers** for the smoke test, restricted to **non-Cloudflare** sites (the test
> environment runs no FlareSolverr/proxy). The test bed is a single local Docker LAN that already
> includes qBittorrent + qui (for the grab half); Prowlarr doubles as a live differential oracle
> (same query → Prowlarr feed vs harbrr feed → diff). Treat creds per AGENTS.md (never logged/committed;
> entered into harbrr's encrypted store, redacted everywhere).

- [x] 5 real **non-Cloudflare** trackers (seedpool, OnlyEncodes+, DigitalCore, Darkpeers, Luminarr; no
      FlareSolverr), live login/session, gentle rate — all 5 pass the Prowlarr differential (4 exact, 1
      count-parity for a config-sorted feed). Build-tagged harness in `internal/smoke`.
- [x] **Robustness proof**: search → **grab** end-to-end verified live — harbrr's download link resolved
      to a real `.torrent` and the release downloaded + seeds in qBittorrent2 (left seeding, no H&R; grab
      via direct qBittorrent push — Sonarr→harbrr unreachable from the sandbox, see `internal/smoke/README.md`).
      Plus the offline serializer fuzz/property test (`internal/torznab/results_fuzz_test.go`) asserting
      arbitrary `[]*Release` always produce well-formed, namespace-bindable XML and never panic.
- [x] **Lazy login**: re-login + retry once when a search response looks logged-out (Jackett's
      `CheckIfLoginIsNeeded` via the `login.test` selector / followed redirect — NOT `login.error`).
      Eager first-login is retained by design (parity goldens); the lazy relogin is the added half.
      Bounded to one retry (no loop). Done in `search/logout.go` + `engine.go` relogin.
- [x] **.NET-compatible URL encoder**: replace `url.QueryEscape` in the query/path value encoders so
      they match `WebUtility.UrlEncode` (Phase 2 leaves these escaped; see `parity/testdata/README.md`
      "Known divergences"). Done via `internal/indexer/cardigann/encode`; verified divergence is `!*()`
      + `~` (not `'`). Login form bodies deferred as a deliberate divergence.
- [x] Fetch/auth matrix rows as available: pluggable solver SEAM (`login.Solver`) wired into the login
      anti-bot path via `WithSolver`; `ManualCookieSolver` (2FA/manual-cookie) is functional, selected by
      a `solver_type=manual_cookie` setting + the encrypted `cookie` setting (no migration — rides the
      existing settings map; `cardigann.SolverOption`). **FlareSolverr deferred to Phase 6** (no infra in
      env; the 5 smoke trackers are non-CF) — `NoopSolver` default keeps the fail-loud behavior.
- [x] **Result-category filtering + default categories**: drop result rows whose categories miss the query
      cats (Jackett `FilterResults`) and substitute a def's `default: true` categories when the mapped
      tracker-cat list is empty (request/response category parity for live *arr search; see
      `internal/torznab/testdata/README.md`). Note: Jackett does not force an empty feed when a `cat` maps
      to nothing — it searches defaults/all and the response filter drops non-matches (empty emerges
      naturally). Done in `internal/web/torznab/filter.go` + `query.go` + mapper `DefaultCategories`.
- [x] **Serve resolved/proxied download links**: `ResolveDownload` wired into the served feed via the
      `torznab.Indexer` `NeedsResolver()`/`ResolveDownload()` seam + `resolveDownloadLinks` (per served
      page). Direct-link trackers (the Phase 5 five) serve their link as-is and grab works (live-proven).
      The grab-time `/dl` proxy (resolve through harbrr's session) + full resolver are **[Tracked: Phase 7]**.
      See `internal/torznab/testdata/README.md`
- [x] **Indexer "Test" action**: `POST /api/indexers/{slug}/test` validates a configured indexer's
      credentials/connectivity via the engine's login probe against a FRESH, uncached engine (no impact on
      the cached production session). Returns `{ok:true}` / 200 `{ok:false,error}` / 404; the error is
      secret-scrubbed (`sanitizeTestError`). `engine.Test` + `registry.Test` + OpenAPI path + drift test.

> **MVP = Phases 1–5.** Phase 4 makes harbrr runnable + configurable; Phase 5 proves it live. This is the
> point the central risk is retired. Do not start Phase 6+ before the parity gate is green.

## Phase 6 — Operational safety

- [x] Timeouts, backoff, per-indexer rate limits (anti-blacklist)
- [x] **Indexer health & status**: define health events (auth failure, rate-limited, parse error,
      anti-bot) and surface per-indexer status via the API; broken indexers already degrade cleanly (Phase 2)
- [x] **Per-indexer proxies** (HTTP / SOCKS5; SOCKS4 deferred `[Tracked]` — `x/net/proxy` has no
      socks4 dialer), configured per instance via the widened `doerFactory`/`ClientParams`/`newDoer`
- [x] **Secret hardening**: key rotation (`harbrr rotate-key` — dry-run + atomic re-encrypt via the
      stored `key_id`); secret redaction audited end-to-end (logs/errors + a JSON-body scrubber for
      FlareSolverr bodies + whole-userinfo proxy-URL scrub; traces/stats event-log don't exist — vacuous)

## Phase 7 — Scale coverage

- [ ] Broaden response-mode and definition coverage; expand selector/date edge-case fixtures
- [ ] **Complete the download resolver**: `.DownloadUri` template namespace, `before.inputs`/
      `before.pathselector`, download-selector template eval, `download.infohash`/`method: post`/
      `headers`, `testlinktorrent` (Phase 2 ships selectors + `before.path`; see `parity/testdata/README.md`)
- [ ] **XML backend edge parity**: CDATA / mixed-namespace / AngleSharp-vs-cascadia edge cases beyond the
      common RSS/Newznab shapes Phase 2 covers
- [ ] Native **Avistaz** family (post-parity; the one family the corpus doesn't cover)
- [ ] **Backup / restore** (config + database): scheduled + manual, using the redacted/encrypted export
      from §9

## Phase 8 — Product polish

- [ ] **\*arr application sync** (qui-as-app): push indexer config into Sonarr/Radarr/Lidarr/… via their
      API — the sync contract + add/update/remove lifecycle + per-app enable/disable (its own sub-plan; a
      Prowlarr headline feature)
- [ ] **Jackett/Prowlarr migration import**: import indexer instances + credentials + category overrides.
      Read credentials from the **Prowlarr SQLite database** (`prowlarr.db`, the `Indexers.Settings`
      JSON column), which stores them in plaintext — NOT the REST API, whose `SchemaBuilder` masks
      `ApiKey`/`Password` fields with `********` (verified against Prowlarr source; see
      `docs/divergences.md` / the secrets testdata README). Jackett's config encrypts creds per-install
      (RSA/DPAPI), so a Jackett import falls back to guided re-entry for the protected fields.
- [ ] Native **harbrr → autobrr push** (closes the RSS-polling gap; family-only win)
- [ ] cross-seed search backend
- [ ] **Stats / search history** (query/grab/auth event log + query API; the auth event log populates
      `api_keys.last_used_at`, left unwritten in Phase 4 to keep validation a pure read); **notifications**
      (Discord/webhook, pluggable provider)
- [ ] **Web UI** — the management dashboard (indexer grid, add/edit forms, manual search, stats);
      depends on the Phase 4 management API. Includes rendering the embedded OpenAPI spec as Swagger UI
      (Phase 4 serves the raw spec at `/api/openapi.yaml`).
- [ ] **OIDC authentication** — fully implement the OIDC login flow stubbed in Phase 4 (the
      `/api/auth/oidc/*` endpoints return 501 today; only a config seam exists). A qui/autobrr family
      feature; pairs with the Web UI auth surface.

---

## Beyond the alpha — not scheduled (demand-gated)

- **Postgres** — **out of the alpha roadmap.** harbrr is single-user self-hosted, where SQLite is the
  right default, not a stopgap; Postgres only earns its keep for shared / multi-instance deployments.
  Build it **when a real multi-instance user needs it**, not on a schedule — there is no committed phase.
  - **Standing invariant (so it plugs in later without a rewrite):** `internal/database/dbinterface`
    stays **dialect-portable** — all repository SQL routes through the interface and its `Rebind`
    (`?`→`$N`) seam, no SQLite-specific SQL or driver types leak to callers, and schema changes ship as
    SQLite migrations a Postgres backend can mirror. Keeping this seam clean is required work **now**;
    implementing Postgres is **not**. (AGENTS.md: "keep the interface clean so it can be added later.")

---

## Standing rules while building (see AGENTS.md)

- Never hand-edit `internal/indexer/definitions/vendor/` — absorb differences in the engine.
- Never log/commit secrets. Always `-race -count=1`. Keep functions small (the linters enforce it).
- One plan item per commit; conventional-commit messages; no AI attribution lines.
- **Capture highlights as you go.** When a phase lands a user-facing or competitive
  improvement over Prowlarr/Jackett/qui, add it to `docs/highlights.md` (honestly
  labeled `[shipped]`/`[partial]`/`[planned]`, with a real citation) so the "why
  harbrr" list is ready when the site/docs are published.
