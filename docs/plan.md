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
      stays deferred — demand-gated, see "Beyond the alpha"). Data dir `0700`; db + all SQLite side files
      (`-wal`/`-journal`) `0600`
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
> them (see Phase 10) — and the **API keys for the *arr (Sonarr/Radarr) + Prowlarr**. The agent then
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

- [x] Timeouts, backoff, per-host rate limits (anti-blacklist) — paced per **target
      domain**, in-process; rate from the def's `requestDelay` or a 1s default (a
      user-configurable per-indexer override + global default is deferred → Phase 10)
- [x] **Indexer health & status**: define health events (auth failure, rate-limited, parse error,
      anti-bot) and surface per-indexer status via the API; broken indexers already degrade cleanly (Phase 2)
- [x] **Per-indexer proxies** (HTTP / SOCKS5; SOCKS4 unsupported `[Accepted]`, demand-gated — `x/net/proxy`
      has no socks4 dialer), configured per instance via the widened `doerFactory`/`ClientParams`/`newDoer`
- [x] **Secret hardening**: key rotation (`harbrr rotate-key` — dry-run + atomic re-encrypt via the
      stored `key_id`); secret redaction audited end-to-end (logs/errors + a JSON-body scrubber for
      FlareSolverr bodies + whole-userinfo proxy-URL scrub; traces/stats event-log don't exist — vacuous)

> **Shipped this phase without a `docs/plan.md` box** (enabling infra + ledger items):
> the request-scoped `context.Context` threading (PR #1); the `ClientParams`
> doer-factory seam; and the **FlareSolverr anti-bot solver** — a real, typed-`/v1`,
> discard-and-replay implementation (NOT a stub; the `/v1` *test server* is the stub),
> which **closes** the Phase-5 `[Tracked: FlareSolverr]` deferral. All ship on their
> committed offline gates. Their LIVE confirmation — a real Cloudflare clear, proxy
> routing, and the deferred Phase-5 auth-pattern retests — is the **Phase 9**
> validation gate below.
>
> The traces/stats **event-log** the redaction audit calls "vacuous" is not a gap
> here — it is the Phase-8 *Stats / search history* item; redaction must be wired in
> when that subsystem is built.

## Phase 7 — Complete the engine

The last parity-engine work: finish the download path and close the remaining selector/XML
edge gaps so harbrr matches Jackett on **every** tracker shape, not just direct-link ones.
This is the deliberate, scoped **un-freeze** of the engine (Phase 6 froze it for operational
safety); it stays offline-gated against the parity oracle.

- [x] **Complete the download resolver**: `.DownloadUri` template namespace, `before.inputs`/
      `before.pathselector`, download-selector template eval, `download.infohash`/`method: post`/
      `headers`, `testlinktorrent` (Phase 2 ships selectors + `before.path`; see `parity/testdata/README.md`).
      Includes the grab-time **`/dl` proxy** (resolve a link through harbrr's session at grab time) — the
      output-layer half of the same feature.
- [x] **XML backend edge parity**: CDATA / mixed-namespace / AngleSharp-vs-cascadia edge cases beyond the
      common RSS/Newznab shapes Phase 2 covers
- [x] Broaden response-mode and definition coverage; expand selector/date edge-case fixtures

## Phase 8 — Native Avistaz family

- [x] Native **Avistaz** family (AvistaZ / CinemaZ / PrivateHD / ExoticaZ) — the one *popular* family the
      Jackett corpus can't express (its login→Bearer `api/v1/jackett` auth exceeds the declarative
      Cardigann format, so there are **0 defs**). A native driver under `internal/indexer/native/avistaz/`,
      plugged into the indexer registry alongside the Cardigann engine via a native catalog + `defResolver`
      (the `indexerAdapter` generalized to a `native.Driver`); it reuses every engine seam (paced client,
      secrets, normalized release, caps mapper, the `/dl` grab proxy, redaction). **Offline-gated**: a stub
      auth/API server + synthetic fixtures whose goldens are derived from Prowlarr's documented contract
      (`develop` @ `d6e8466`), never a live capture. Prowlarr supports Avistaz natively, so the live
      Prowlarr differential + a real search/grab are the **Phase 9** gate (recorded
      `[Tracked: Phase 9]`). Divergences in `internal/indexer/native/avistaz/testdata/README.md`.

## Phase 8b — Complete the management API (team-alpha enabler)

Product surface, post-engine-parity. Today's JSON management API is a **control plane**: search,
capabilities, and grab live only on the Torznab XML tree, discovery is incomplete, and some config is
settable-but-undocumented. This phase closes the control-plane / data-plane gaps so the documented API
at `/api/docs` can **drive harbrr entirely over HTTP** — letting the team run an **alpha with no web
UI, just the Swagger API** to add indexers, search, read capabilities, and manage credentials by hand.
It lands **before Phase 9** so the live-validation pass is exercised against the API the team actually
tests through. One PR off `main` (`phase8b/management-api`); offline-gated; **PAUSE before merge**. Full
gap analysis + per-endpoint contracts: `docs/archive/issue-phase8b.md` + `docs/archive/prompts/phase8b.md`.

- [x] **Shared query mapping + router wiring** — extract/reuse `buildQuery` (+ `parsePaging`) so the JSON
      search and the Torznab feed map params identically; wire the keyring/`/dl` tokenizer + base path into
      the management router (enabling — ticks no box on its own)
- [x] **`GET /api/indexers/{slug}/search`** — Torznab param set → `idx.Search` → JSON `normalizer.Release`;
      resolver links `/dl`-tokenized (the passkey never reaches the JSON); spec + **parity test** (JSON ≡
      Torznab `t=search` for the same query) + **redaction test**
- [x] **`GET /api/indexers/{slug}/capabilities`** — `Capabilities()` → JSON (modes / params / categories /
      limits); spec + test
- [x] **`GET /api/definitions/{id}`** — a definition's settings-field schema (with `secret` flags) + caps,
      so a client can render an add-indexer form; id-validation / traversal guard; spec + test
- [x] **`POST /api/auth/change-password`** — verify the current password (reuse the login verifier) →
      `UpdatePassword` → session renewal; `400` weak new password, `401` wrong current password; spec + test
- [x] **Spec hardening** — document the config settings (`proxy_*` / `timeout` / `solver_*` / reserved
      secrets) with enums; add a machine-readable `code` to the error schema. **OIDC untouched — deferred to
      Phase 10.**
- [x] **Gate**: four endpoints documented + drift-test-green; JSON search ≡ Torznab (parity proven); **no
      passkey/secret in any JSON response/error/log** (redaction proven); `make precommit` + `make build`
      green; PR ≤150 files

## Phase 9 — Live validation & acceptance (alpha gate)

The end-of-alpha live pass: exercise **every auth/fetch pattern against real trackers** (Cardigann +
the native Avistaz family) and parity-check harbrr against a **live Prowlarr** — the single home for
every `[Tracked: deferred]` live retest the offline gates can't cover (Phase-5 deferred several auth
patterns; Phase-6 the live half of timeouts/proxy/FlareSolverr; Phase-7 the resolver-needing grabs;
Phase-8 the native Avistaz family). Operator-resourced; run via the build-tagged `internal/smoke`
harness (`//go:build smoke`, `SMOKE_*` env-var creds, gentle rate, **never CI**); each row records
secret-free pass/fail evidence in `internal/smoke/README.md`. A bug it surfaces is `[Tracked]` against
the owning layer — the engine stays frozen during validation; fixes are scoped, not ad-hoc.

- [ ] **Every auth/fetch pattern live**, each against an operator-supplied tracker: user/pass
      **form login**; **cookie / 2FA** (manual-cookie solver); **.NET-quirk** (`*()'!` / unicode /
      `regexp2`); **Cloudflare via FlareSolverr** (the Phase-6 solver clears a real CF tracker end
      to end); **per-indexer proxy** (HTTP + SOCKS5 route a real search).
      — **2026-06-16: apikey (11), form login (racingforme), and Cloudflare/FlareSolverr (torrentleech)
      confirmed live; cookie/2FA, .NET-quirk, and HTTP/SOCKS proxy `[Tracked]` (no qualifying tracker in
      the stack — see `internal/smoke/README.md`).**
      — **2026-06-20: HD-Space (Cloudflare + `method: post` form login) live-confirmed end-to-end (search
      returns parsed releases). It exposed two engine gaps the first CF tracker did not, both fixed as
      general engine behaviour (PR #52): (1) CF challenges the login **POST** specifically — harbrr now
      GET-solves the challenged login URL for a host-wide `cf_clearance`, persists the solver's bound
      User-Agent across login + search, then retries the POST (Jackett/Prowlarr's approach); (2) the row
      selector carried a `{{ if .Config.freeleech }}…{{ end }}` guard that must be template-evaluated
      before CSS compilation. Cloudflare/form-login is now double-confirmed (torrentleech + HD-Space).**
- [x] **Broad live Prowlarr differential** — many trackers (not just the Phase-5 five), **Cardigann +
      Avistaz**: same query → Prowlarr feed vs harbrr feed → diff, confirming request/response + category
      parity at scale against the live oracle. — **2026-06-16: 13/14 PASS, count parity 1.00 across the
      board** (1 Prowlarr-side skip; AvistaZ not in the stack).
- [x] **Grab end-to-end per pattern** — search → resolved `.torrent` → seeding in qBittorrent (left
      seeding, no hit-and-run), for ≥1 tracker per auth pattern, **including a resolver-needing tracker
      via the Phase-7 `/dl` path**. — **2026-06-16: 11/13 resolved a real `.torrent` (URL-token trackers,
      apikey + form). Found a real gap `[Tracked: needs a fix PR]`: harbrr serves a bare download link for
      non-resolver trackers, so downloads that authenticate by session cookie (torrentleech, ~all
      cookie-login trackers) or request header (digitalcore X-API-KEY) are NOT grabbable by *arr — harbrr
      is search-only for them until their downloads route through `/dl`. See `internal/smoke/README.md`.
      → owned by Phase 9.5 item 1.**
      — **Closed 2026-06-18:** the cookie/header-auth grab gap is fixed (Phase 9.5 item 1, the
      authenticated-`/dl` path) and live-confirmed in **#44** — torrentleech (session cookie) and
      digitalcore (X-API-KEY) both resolve a real `.torrent` through `/dl`. Patterns with no qualifying
      tracker in the stack (2FA, proxy) stay `[Tracked]` per the item above.
- [x] **Acceptance** — every pattern green, or its gap recorded `[Tracked]` with a disposition.
      — **2026-06-16: every pattern is green or `[Tracked]` with a disposition. The live run also caught +
      fixed a daemon-breaking nil-`Transport` panic (PR #42) and surfaced a native-indexer coverage gap —
      harbrr has no def for one-off C# native trackers (IPTorrents/MyAnonamouse/FileList) `[Tracked]`
      → owned by Phase 9.5 item 2.**
      — **Met 2026-06-18:** the acceptance criterion (every pattern green or `[Tracked]` with a
      disposition) holds, and the native-coverage gap this surfaced is now shipped (Phase 9.5 native
      drivers, #45/#46). Phase 9 is accepted; the remaining `[Tracked]` items are gated on externals
      (no qualifying tracker for 2FA/proxy; the stack's MyAnonamouse session is dead at source).
      This is the live half of "match Jackett/Prowlarr on real trackers"; the offline parity gate
      (Phase 2) proves it deterministically.

## Phase 9.5 — Functionality hardening (close the alpha-blocking gaps before any product surface)

Phase 9 proved search parity (count 1.00 across 13/14 live trackers) but surfaced two gaps that leave
harbrr **search-only** or **can't-serve** for a real slice of private trackers. These are
correctness/coverage, **not** polish, so they land **before** Phase 10 — there is no point building UI /
app-sync / migration on top of trackers harbrr can't fully serve. The engine stays parity-frozen; this
is additive grab-path + native-driver work. Items 1 and 2 share machinery (the authenticated-`/dl` grab
path), so item 1 comes first. Pattern reference: [`native-indexer-pattern.md`](native-indexer-pattern.md).

- [x] **Grab via `/dl` for login-authenticated downloads** — today harbrr serves a bare download link and
      only routes through the `/dl` proxy when a def has a `download:` block (`NeedsResolver()`). Extend
      `/dl` to also resolve downloads that authenticate **out-of-band**: by **session cookie** (torrentleech
      + ~every cookie-login tracker) and by **request header** (digitalcore X-API-KEY / UNIT3D). harbrr
      fetches the `.torrent` server-side with its authenticated session and serves the bytes, so *arr never
      sees the unauthenticated bare link. Scoped engine PR + smoke re-test. (Gap recorded in
      `internal/smoke/README.md`; the AvistaZ `/dl` path is the template.)
      — **Code shipped + offline-proven:** `DownloadNeedsAuth()` (def has a login block) widens the
      `/dl` routing predicate, and `renderDownloadHeaders` now applies `search.headers` to the
      authenticated download fetch (nil-guarded for no-`download:`-block defs). Box stays unchecked until
      the live grab retest against torrentleech + digitalcore is green.
      — **Live-confirmed 2026-06-18 (#44):** torrentleech (cookie) and digitalcore (X-API-KEY) both resolve a
      real `.torrent` through `/dl`. `[Resolved: Phase 9.5]`.
- [ ] **Native drivers for the stack's C# one-off trackers** — **IPTorrents, MyAnonamouse, FileList** have
      no Cardigann YAML (Jackett/Prowlarr ship them as bespoke C# indexers), so harbrr can't serve them at
      all. Build them on the AvistaZ native pattern (`native.Driver` = settings POCO + request generator +
      parser), reusing the authenticated-`/dl` grab path above. Two reusable auth shapes cover all three —
      a **session-cookie** driver (IPTorrents HTML scrape; MyAnonamouse JSON API, **must persist the rotated
      `mam_id`** per response) and a **passkey/Basic-auth** driver (FileList JSON). Offline-gated like
      AvistaZ (stub server + synthetic goldens from the documented contract), then the **live Prowlarr
      differential is the gate** (the stack runs all three live). Redact `mam_id`/`passkey`/`Cookie`/
      `Authorization` everywhere. Per-tracker divergences recorded beside each driver's fixtures.
      — **Shipped (#45/#46), live status 2026-06-18:** IPTorrents — count parity 1.00 + grab, `[Resolved]`;
      FileList — search live-confirmed (int-flags fix #46), Prowlarr differential pending a name match;
      MyAnonamouse — driver + `mam_id` write-back seam (#46) correct, live search/parse `[Tracked: pending a
      fresh dedicated MAM session]` (the stack's session is dead at source — fails in Prowlarr too).
- [x] **Coverage analysis across toolsets** (`docs/coverage.md`) — the **tracker × surface × tool × auth**
      matrix. Key results: harbrr owns the **search** surface (autobrr owns announce — disjoint); a
      *Prowlarr-native* tracker is **not** a harbrr gap when Jackett ships YAML (harbrr vendors Jackett — e.g.
      HDSpace). For this stack harbrr covers **all 18 torrent indexers**; only DOGnzb (usenet/Newznab) is out
      of scope. harbrr's real native backlog = C#-in-both trackers: the **Gazelle-API** family (Redacted/
      Orpheus/PTP/BTN — one base driver, the highest-leverage next build) + cookie-scrape (TorrentDay/SpeedCD)
      and passkey (HDBits/BeyondHD) groups, which reuse the IPTorrents/FileList shapes already built.
- [x] **Live-validation ledger (opportunistic, not a gate)** — the standing checklist of offline-proven
      patterns awaiting a live qualifying tracker (cookie/2FA, .NET-quirk, HTTP/SOCKS proxy, + MyAnonamouse
      live search/parse) lives in `internal/smoke/README.md` and `docs/coverage.md` §6. Ticks opportunistically;
      not a release gate.

## Phase 10 — \*arr application sync (Sonarr / Radarr / qui)

The one product feature that makes harbrr a drop-in Prowlarr for the core stack: push indexer config
into the apps so they don't each configure indexers by hand.

- [x] **App sync — Sonarr, Radarr, qui** — push indexer config into these three via their API: the sync
      contract + add/update/remove lifecycle + per-app enable/disable (its own sub-plan; a Prowlarr
      headline feature). Scoped to **Sonarr/Radarr/qui only** — other \*arrs (Lidarr/Readarr/Mylar/Whisparr)
      are demand-gated backlog.
      — **Code shipped + offline-proven:** new `internal/appsync` package — a target-neutral
      `DesiredIndexer` reconciled by a pure engine (idempotent add-or-update via `payload_hash`,
      remote-id recovery from the feed-URL slug, orphan removal gated to `sync_level=full` and only
      harbrr-owned rows, partial-failure isolation) behind a `Target` interface with three drivers
      (Sonarr/Radarr share the Servarr v3 `fields[]` dialect; qui is the snake_case `native`-backend
      dialect). Per-connection storage (`0003_appsync.sql`): a dedicated harbrr API key minted +
      encrypted per connection, a per-app **harbrr feed URL**, `sync_level` (full | add_update) and
      `index_scope` (all | selected, with a `PUT …/indexers` selection endpoint). Management API under
      `/api/app-connections` (CRUD + enable/disable + test + sync + status), OpenAPI + drift green.
      Secrets redacted everywhere (app response bodies are never echoed into errors).
      — **Live-validated 2026-06-18** against the stack's real apps (192.168.10.220). **qui**
      (`:7476`): full round-trip — the driver's exact create body returned **201** (indexer id
      assigned, snake_case `backend:"native"` + `categories[]` accepted) and **204** on delete; the
      list shape matches the `quiIndexer` struct. **Sonarr** (`:8989`) / **Radarr** (`:7878`): the
      live `GET /indexer/schema` confirms the exact field set (Sonarr has `animeCategories`, Radarr
      does not; both use `apiPath` — the C1 fix), and a `POST /indexer` with the driver's body was
      accepted to the connectivity-test stage, building the correct `{baseUrl}?t=caps&apikey=…`
      request — proving the body schema + feed-URL/apiKey handshake. (The save returned the expected
      connectivity 400 only because no harbrr feed is deployed at that host to authenticate the probe
      key; the Sonarr error echoed `apikey=…` in a URL, confirming the never-echo-app-bodies fix.) No
      driver changes were needed. The doc-derived goldens are confirmed.
      — **Gold-standard live test passed 2026-06-19** (harbrr deployed at `192.168.10.220:7575`, driven
      entirely over the API): 10 real apikey indexers added + tested green in harbrr, then 3
      app-connections (Sonarr/Radarr/qui) created and synced. **Radarr 10/10 created, qui 10/10 created,
      Sonarr 8/10** — the 2 misses (`reelflix`, `retromoviesclub`) are movie-only defs (no `tv-search`)
      that Sonarr *correctly* rejects and that landed in Radarr fine, so the full-stack save is confirmed
      green. Indexers + connections left persisted. `[Resolved: Phase 10]`
- [x] **Gate — a legitimate Swagger-only Prowlarr replacement.** With Phase 10 done, harbrr fully replaces
      this stack's Prowlarr **operated entirely through the Swagger API** at `/api/docs` — no Web UI: add +
      configure + test every indexer, search, grab through `/dl`, and sync indexers into Sonarr/Radarr/qui,
      all over HTTP. **This is the alpha's definition of done.** Phase 11 (Web UI) is additive — nicer to
      use, never required.
      — **Exercised end-to-end offline** (the sync surface round-trips over HTTP in the api tests);
      checks once the app-sync live validation above is green.

## Phase 11 — Web UI

- [ ] **Web UI** — the management dashboard (indexer grid, add/edit forms, manual search, stats);
      depends on the Phase 4 management API. **Stack: match qui's** — believed Vite + React + Tailwind CSS;
      **verify against the qui repo during Phase 11 scoping** before committing. (Interactive **Swagger UI
      already shipped** at `/api/docs`, separate from the SPA — the web UI just links to it; raw spec at
      `/api/openapi.yaml`.)

---

## Beyond the alpha — not scheduled (demand-gated)

Built when a real user needs it, not on a schedule. Each is self-contained and off the alpha→beta
(Phase 10 app-sync → Phase 11 Web UI) critical path.

- **More \*arr sync targets** — Lidarr / Readarr / Mylar / Whisparr. The Phase-10 sync contract is built
  for Sonarr/Radarr/qui; extending it to another app is mostly a per-app adapter.
- **Jackett/Prowlarr migration import** — import indexer instances + credentials + category overrides.
  Read creds from the **Prowlarr SQLite database** (`prowlarr.db`, the plaintext `Indexers.Settings` JSON
  column) — NOT the REST API, whose `SchemaBuilder` masks `ApiKey`/`Password` with `********`. Needs a
  **Prowlarr-impl → harbrr-def name table** for Prowlarr-native trackers harbrr serves as Cardigann (e.g.
  HDSpace — see `docs/coverage.md` §5). Jackett's RSA/DPAPI-encrypted config falls back to guided re-entry.
- **Native-driver backlog** — the C#-in-both-engines trackers (`docs/coverage.md` §4): highest-leverage is
  one **Gazelle-API** base driver (Redacted/Orpheus/PTP/BTN/AnimeBytes); cookie-scrape (TorrentDay/SpeedCD)
  and passkey (HDBits/BeyondHD) reuse the IPTorrents/FileList shapes. Build per tracker on demand.
- **harbrr → autobrr push** — closes the RSS-polling gap (family-only win).
- **Usenet / Newznab support** — harbrr is torrent-only today, so a stack's usenet indexers (e.g.
  DOGnzb) can't migrate. Add Newznab provider support (the `caps`/`search` surface already speaks
  Newznab-compatible XML; the gap is the usenet *fetch* + indexer kind). Surfaced by the Phase 10
  gold-standard migration — DOGnzb was the one stack indexer harbrr couldn't serve.
- **cross-seed search backend.**
- **Stats / search history** (query/grab/auth event log + query API; the auth log populates
  `api_keys.last_used_at`, left unwritten in Phase 4) **+ notifications** (Discord/webhook, pluggable).
- **Backup / restore** (config + database): scheduled + manual, using the §9 redacted/encrypted export
  (secrets behind a `<redacted>` sentinel; including secrets is a separately-passphrase-encrypted opt-in).
- **OIDC authentication** — implement the flow stubbed in Phase 4 (`/api/auth/oidc/*` return 501 today;
  only a config seam exists). Pairs with the Web UI auth surface.
- **User-configurable request rate** — a global default + per-indexer override. Phase 6 paces per target
  domain from the def's `requestDelay`; the `ClientParams.RateInterval` seam already carries the value, so
  this is a settings surface + plumb-through. Pairs with the Web UI settings.
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
