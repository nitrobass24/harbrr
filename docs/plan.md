# harbrr build plan

The executable checklist. Work **top to bottom, one item at a time**, and check a box only when its
tests are green (`make precommit` clean). Ordered by **risk retirement**, not product completeness —
the engine must prove it can match Jackett on saved inputs before any product surface is built. Full
rationale in `architecture.md`; rules in `../AGENTS.md`.

Legend: `[ ]` todo · `[x]` done · `[~]` deferred/moved out of scope (see the note) · each leaf should land
in its own focused commit.

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
critical path everything product-facing depends on, and where the `docs/security.md` security model is
built. (Before this phase, `cmd/harbrr serve` loaded config and exited and the Torznab handler had no
production caller; this phase makes harbrr a runnable, configurable daemon — the registry is now the
production `Provider` the handler resolves through.)

- [x] **SQLite store + migrations** behind `internal/database/dbinterface` (clean interface; Postgres
      stays deferred — demand-gated, see "Beyond the alpha"). Data dir `0700`; db + all SQLite side files
      (`-wal`/`-journal`) `0600`
- [x] **Secrets store** (`internal/secrets`) — the three-class model (`docs/security.md`): tracker creds
      AES-256-GCM (per-record nonce, AAD = indexer-id + setting, stored `key_id`); web-UI password
      argon2id; API keys SHA-256. Auto-generate a keyfile on first run (encryption always on); fail
      loud on a wrong/changed key
- [x] **Indexer instance registry** — add / configure / enable / disable / delete a configured indexer
      (definition id + settings + encrypted credentials) and resolve an id → engine. This is the
      production `Provider` the Torznab handler already expects, and the core of a Prowlarr-style manager
- [x] **Management API + auth** — grow the hand-authored `openapi.yaml` past `/healthz` (indexer CRUD,
      settings, API-key management); first-run setup; server-side sessions + `X-API-Key`; the
      `SameSite=Lax` cookie CSRF posture (token-based CSRF deferred — autobrr/harbrr#15); the qui
      auth-disabled / trusted-proxy mode
- [x] **Wire the server** — mount the Torznab handler (`internal/web/torznabhttp`) **and** the management
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
      `CheckIfLoginIsNeeded` via the `login.test` selector / an unfollowed search 3xx — NOT `login.error`).
      Eager first-login is retained by design (parity goldens); the lazy relogin is the added half.
      Bounded to one retry (no loop). Done in `search/logout.go` + `search/redirect.go` + `engine.go` relogin.
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
      naturally). Done in `internal/web/torznabhttp/filter.go` + `query.go` + mapper `DefaultCategories`.
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
tests through. One PR off `main` (`phase8b/management-api`); offline-gated; **PAUSE before merge**.
(Shipped via #47; the live API contract is the OpenAPI spec at `/api/openapi.yaml`.)

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

- [x] **Every auth/fetch pattern live**, each against an operator-supplied tracker: user/pass
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
      — **2026-06-21: .NET-quirk confirmed live (PR #52).** Added the public **rutor** tracker (ru-RU):
      its `\p{IsCyrillic}` filter patterns route to `regexp2` (RE2 rejects the property) and applied over
      100 parsed results — `regexp2` routing confirmed live. The WebUtility encoder is confirmed via live
      unicode searches AND a real `()!` bug it surfaced: harbrr left `( ) !` literal (matching .NET's
      intermediate string), which tripped HD-Space's Cloudflare WAF and 500'd any "Title (Year)" search;
      the live Prowlarr differential proved it a divergence (Prowlarr returns results), so harbrr now
      percent-encodes them (the on-the-wire form) — "Spider-Man (2002)" now returns results. **Box checked:
      every pattern present in the stack is green.** The two patterns with no qualifying tracker/proxy —
      **cookie/manual-cookie** and **HTTP/SOCKS proxy** — are moved to the demand-gated backlog (offline-
      proven; their live retest awaits the operator standing up a qualifying tracker/proxy).**
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
- [x] **Native drivers for the stack's C# one-off trackers** — **IPTorrents, MyAnonamouse, FileList** have
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
      — **FileList [Resolved] 2026-06-20 (PR #52):** Prowlarr differential run directly against the live
      oracle (Prowlarr indexer 19 "FileList.io") — **count parity 1.00** (dune/matrix/inception: 87/75/17 on
      both) and **title Jaccard 1.00** (all 87 dune titles identical), category mapping matches (4050 +
      100009). The earlier auto-skip was only the harness name match (harbrr slug `filelist` vs Prowlarr
      `FileList.io`), not a functional gap.
      — **MyAnonamouse [Resolved] 2026-06-20 (PR #52):** with a fresh `mam_id`, live search returned parsed
      audiobook results through harbrr. Required the same class of fix as FileList's int-flags — MAM's live
      `loadSearchJSONbasic.php` returns integers where the documented contract used strings/booleans
      (`category`/`main_cat` as numbers, `free`/`personal_freeleech`/`fl_vip` as 0/1); the strict struct
      failed `json.Unmarshal`. Tolerant decode types added. **All three native drivers now live-confirmed**
      (FileList's Prowlarr-name differential is the only belt-and-suspenders item left; search is Resolved).
- [x] **Coverage analysis across toolsets** (now: `website/docs/coverage.md` + the `native-driver`
      issues) — the **tracker × surface × tool × auth**
      matrix. Key results: harbrr owns the **search** surface (autobrr owns announce — disjoint); a
      *Prowlarr-native* tracker is **not** a harbrr gap when Jackett ships YAML (harbrr vendors Jackett — e.g.
      HDSpace). For this stack harbrr covers **all 18 torrent indexers**; only DOGnzb (usenet/Newznab) is out
      of scope. harbrr's real native backlog = C#-in-both trackers: the **Gazelle-API** family (Redacted/
      Orpheus/PTP/BTN — one base driver, the highest-leverage next build) + cookie-scrape (TorrentDay/SpeedCD)
      and passkey (HDBits/BeyondHD) groups, which reuse the IPTorrents/FileList shapes already built.
- [x] **Live-validation ledger (opportunistic, not a gate)** — the standing checklist of offline-proven
      patterns awaiting a live qualifying tracker (cookie/2FA, .NET-quirk, HTTP/SOCKS proxy, + MyAnonamouse
      live search/parse) lives in `internal/smoke/README.md`. Ticks opportunistically;
      not a release gate.

## Phase 10 — \*arr application sync (Sonarr / Radarr / qui)

The one product feature that makes harbrr a drop-in Prowlarr for the core stack: push indexer config
into the apps so they don't each configure indexers by hand.

- [x] **App sync — Sonarr, Radarr, qui** — push indexer config into these three via their API: the sync
      contract + add/update/remove lifecycle + per-app enable/disable (its own sub-plan; a Prowlarr
      headline feature). Scoped to **Sonarr/Radarr/Lidarr/Readarr/Whisparr/qui** — the Servarr-shaped
      forks reuse the v3 driver (Lidarr/Readarr on `/api/v1`, the rest on `/api/v3`). Mylar remains
      demand-gated backlog.
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
      all over HTTP. **This is the alpha's definition of done.** Phase 12 (Web UI) is additive — nicer to
      use, never required. (Phase 11 — alpha feature-complete — adds the differentiators that make the
      alpha *superior* to Prowlarr/Jackett, not just a replacement.)
      — **Exercised end-to-end offline** (the sync surface round-trips over HTTP in the api tests);
      checks once the app-sync live validation above is green.
      — **`[Tracked]` live deep-set paging differential (#75)** — forwarding `offset`/`limit` to a
      paginating upstream (native Newznab first) returns the tracker's genuine page-2+ results, beating
      Prowlarr (#1428) on paginating trackers. **Proven offline** (parity untouched + a cache-wrapped
      deep-page replay test asserting page-2 content; honest `<newznab:response total>` floor). The
      **live** Prowlarr differential vs a real paginating tracker is owed from the smoke stack
      (`192.168.10.220`), unreachable from the build env. (Superiority item; pairs with the Phase 11
      "Better pagination support" entry — verify here as part of the live replacement gate.)

## Phase 11 — Alpha feature-complete (the "superior to Prowlarr/Jackett" bar)

harbrr is already at **parity** with Prowlarr/Jackett — Cardigann engine, native drivers across **every
auth shape** (Phase 8/9.5 + #62/#63), \*arr/qui app-sync (Phase 10), and a shipped search-results cache
(#60). This phase is what makes the alpha **immediately superior the day it lands**, not merely
equivalent. It is a **hard gate on the Web UI (Phase 12): no UI work begins until every blocking
box here is green** (items explicitly marked *non-blocking* don't hold the gate). The bar is a *feature-superset* of Prowlarr (now including usenet) **plus** the "kind to
trackers" differentiators Prowlarr/Jackett don't have, **plus** the autobrr-family sync reach that makes
harbrr the single source of truth for the whole stack.

Two consciously-accepted alpha gaps are deferred to the backlog: **automated migration import** (alpha
ships with manual indexer setup — existing Prowlarr/Jackett users re-enter rather than import) and
**Upbrr credential sync** (Upbrr exposes no machine/API-key integration today — only a session-guarded
internal UI API on a primarily-desktop app — so the push is gated on coordinating a stable contract with
the autobrr/Upbrr team first, the same way *harbrr → autobrr push* is; see the Tier-1 backlog entry).

- [x] **Usenet / Newznab support** — harbrr was torrent-only; this closes the **last capability Prowlarr
      had that harbrr lacked** (Jackett is torrent-only, so Prowlarr is the sole parity target). **Shipped:**
      generic Newznab driver + ~18 presets on the `native.Family` seam (`internal/indexer/native/newznab/`;
      parity with Prowlarr, no YAML), protocol derived from the driver + denormalized into the
      `indexer_instances.protocol` column, `.nzb` proxied server-side via `/dl` (apikey sealed). User docs:
      `website/docs/features/usenet-newznab.md`; design divergences:
      `internal/indexer/native/newznab/testdata/README.md`. Build leaves:
   - [x] **Protocol primitive + DB column** — `Protocol` on the definition/`Family` (default `torrent`) +
         `0005` migration on `indexer_instances` + `domain.IndexerInstance`, populated on add; threaded
         through caps/`IndexerInfo` → `FeedInfo`. No behavior change.
   - [x] **Protocol-aware serializer** — gates `enclosureType` (`x-nzb`) + `appendTorrentAttrs` on protocol.
   - [x] **Protocol-aware normalizer** — *no change needed*: native drivers build `Release` structs
         directly and never hit the `seeders` validator (the `/dl` content-type is set driver-side).
   - [x] **Newznab client core** — `internal/indexer/native/newznab/`: settings + request gen + XML parser
         + server-side grab, offline stub-server goldens (`NeedsResolver=false`, `DownloadNeedsAuth=true`).
   - [x] **Caps fetch + category mapping** — live `?t=caps` → mapped newznab cats, cached (7-day TTL,
         `PersistSetting`), primed in `Test()`.
   - [x] **Grab path** — fetches `.nzb` via `/dl`, `application/x-nzb`, apikey-redaction tested (folded
         into the driver core leaf).
   - [x] **Family registration + presets** — `newznab.Families()` (generic + ~18 presets); registry e2e.
   - [x] **App-sync protocol** — `DesiredIndexer.Protocol`; registers usenet as the remote **Newznab** impl;
         `List()` orphan-trap fixed; qui skips usenet.
   - [x] **End-to-end** — offline HTTP e2e (stub server): configure → search → usenet feed → `/dl` grab.
   - [x] **User-facing docs + divergence** — `website/docs/features/usenet-newznab.md` (MkDocs nav) +
         `[Deliberate]` proxy divergence in `internal/indexer/native/newznab/testdata/README.md`.
         Live validation deferred (needs a real usenet apikey; opportunistic, not a gate).
- [x] **Shared RSS-feed caching** — **shipped in substance by #60** and verified 2026-06-26. The search
      cache keys on `(instanceID, canonical query)` with **no consumer identity**
      (`internal/indexer/registry/searchcache_key.go`), wraps **both** the Torznab feed and the JSON search,
      caches empty/RSS polls under the `rss` TTL tier (`searchcache_ttl.go`), and uses **SWR +
      singleflight** so a tracker sees **≤1 fetch per TTL across all consumers**
      (`searchcache.go`). So every consumer (Sonarr/Radarr/autobrr/cross-seed) is served from one cached
      copy instead of each polling the tracker. The literal residual — **cross-category-set superset
      dedup** (consumers polling disjoint `cat=` subsets make separate entries) — is **deferred**: categories
      flow into the real tracker request via `{{ .Categories }}` and the thin-clamp measures raw result
      count, so a superset-fetch-then-slice would change the outbound request shape and break thin-clamp /
      parity semantics for a narrow real-world win.
- [x] **Fabric-wide tracker-friendly pacing** — **shipped** and verified 2026-06-26. A **process-wide
      per-host `rate.Limiter`** (`hostLimiters`, **strictest-wins**) fronts **every** Cardigann + native
      driver request, the caps probe, and `/dl` (`internal/indexer/registry/pacedclient.go`, `client.go`),
      so the aggregate rate harbrr presents to a tracker stays polite no matter how many apps sit behind it.
      The per-host limiter is global (keyed by host, not instance), with bounded 429/503 backoff honoring
      `Retry-After`. **User-configurable** per-host rate is **deferred** (Tier-2): `ClientParams.RateInterval`
      is captured at adapter-build time and limiters are created per-host strictest-wins, so a *live* global
      floor is a real plumb-through, not a settings-only change.
- [x] **Negative-result circuit breaker** *(new, kind-to-trackers)* — after a live search to a tracker
      fails, further cache misses for that instance short-circuit to the recorded error for a short window
      instead of re-driving the tracker (anti-thundering-herd; rate-limit responses honor `Retry-After`). A
      still-fresh positive cache entry is never affected. Runtime-tunable `negative_ttl` knob (default 1m;
      `0s` disables) via the #70 app-settings path. `internal/indexer/registry/searchcache_breaker.go`.
- [x] **Cache/pacing observability** *(new)* — `GET /api/cache/stats` exposes `trackerHitsSaved` (durable
      tracker requests served from cache — the headline value metric), `breakerSuppressed`, and a
      `byIndexer[]` per-indexer breakdown (hit ratio, hits saved, breaker open-state). Store
      `StatsByInstance` (`internal/database/searchcache.go`) + engine merge (`searchcache_manage.go`).
- [x] **Persist cache stat counters across restart** *(new)* — the global + per-instance
      hit/miss/breakerSuppressed counters were in-memory only and reset to zero on every container
      relaunch. A `cache_counters` table (`0007`, per-instance, `ON DELETE CASCADE`) + a stateless
      `CacheCountersStore` (`internal/database/cachecounters.go`); the registry rehydrates the atomics at
      boot and flushes absolute values on the existing cleanup-tick/shutdown machinery
      (`searchcache_counters.go`), gated by a `countersRehydrated` flag so a failed boot load can't clobber
      stored totals. Globals = sum of per-instance rows (the increment pairing is invariant).
- [x] **HTTP cache validators on the feed (conditional GET)** *(new — ze0s's "ETags/cache tags" ask)* —
      a cache-backed Torznab/Newznab feed emits a strong `ETag` (content hash of the cached result set)
      + `Cache-Control: private, max-age=<remaining TTL>`; an `If-None-Match` match is answered `304 Not
      Modified` (no body, no tracker hit), and a request `Cache-Control`/`Pragma: no-cache` forces a live
      fetch (the header sibling of `nocache=1`). Standards-only — clients (autobrr) can adopt it with no
      harbrr-side coupling. Prowlarr/Jackett emit no validators. `internal/web/torznabhttp/cacheinfo.go`,
      `handler.go`; the registry cache surfaces the validators via a `CacheInfo` context sink.
- [x] **Cross-seed backend + freeleech-aware matching** — make harbrr the smartest *source* and *messenger*
      for cross-seed (qui cross-seed + cross-seed v6), and let one tracker serve both ratio-building and
      cross-seed off a single config. harbrr never matches — the x-seed tools do that. **Absorbs issue #10.**
      The "search reuse / aggregation / reduced duplicate tracker activity" framing from the README is already
      delivered by the shipped shared search cache + fabric-wide pacing + negative breaker; what remains is the
      freeleech control and the push integrations below. Build order: FL toggle/bypass first, then announce
      push/source. Leaves:
   - [x] **Per-indexer freeleech toggle (Prowlarr-style)** — a user-configurable `freeleech` setting on the
         indexer instance. When ON, the default Torznab/Newznab feed serves freeleech-only (cheap-ratio releases
         for arrs). harbrr clears `freeleech` from the engine config at build (so the cache holds the FULL
         catalog) and applies the filter at SERVE time on `downloadVolumeFactor==0` — so the toggle costs no extra
         tracker fetch and the full set is still available to the bypass feed. (`registry.go` buildAdapter +
         `freeleech.go` decorator.)
   - [x] **Freeleech-bypass feed variant** — a second feed surface (`/results/torznab/full` + `/full/api`) for the
         *same* indexer that returns the full catalog (cross-seed must see everything). **No vendored-def edits.**
         Decided in plan mode: **dedicated variant URL** (a `bypass` request flag on `search.Query`, set by the
         route, NOT in the cache key) + a **serve-time filter** outside the cache — so honor + bypass share ONE
         cached full-set entry (no redundant tracker fetch). (`handler.go` routes, `freeleechbypass.go`,
         `freeleech.go` decorator, OpenAPI.)
   - [x] **Per-app freeleech routing** *(absorbs #85)* — a `freeleech_mode` ('honor'|'bypass') column on `app_connections`,
         **defaulted by app kind** (Sonarr/Radarr/Lidarr/Readarr/Whisparr → honor; qui → bypass) and
         operator-overridable. app-sync pushes the matching feed variant per connection (hook: `feedURL` in
         `sync.go`, already branched on `conn.Kind`). One tracker configured once → the right variant reaches
         each app automatically; no Prowlarr-style tracker duplication, no doubled config-time tracker hits.
         (Note: qui uses a single shared Torznab indexer pool for *both* cross-seed and manual search, so qui
         gets the bypass variant for both — there is no per-feature switch on qui's side.)
         The migration (`0008`) also **drops the drifted `kind` CHECK** so lidarr/readarr/whisparr connections
         can be created (#85), with a standing all-6-kinds round-trip test; `validateKind` is the single source
         of truth.
   - [x] **cross-seed v6 config snippet** — `GET /api/indexers/{slug}/crossseed-snippet` emits a copy-paste
         `torznab:` `config.js` entry (the bypass `/full` URL + an apikey placeholder). cross-seed v6 has no
         indexer API (file config + restart), so this is its config-push equivalent. (`crossseed_handlers.go`.)
   - [x] **Announce push (qui + cross-seed v6)** — an `AnnounceTarget` interface mirroring app-sync's
         `Target`, one driver per kind: **qui** two-step (`POST /api/cross-seed/webhook/check`
         `{torrentName,size,indexer}` → on `recommendation:"download"`, fetch the `.torrent` via `/dl`, base64,
         `POST /api/cross-seed/apply` `{torrentData,indexer,tags}`); **cross-seed v6** one-step
         (`POST /api/announce` `{name,guid,link,tracker}`, header `x-api-key`, `link` = harbrr `/dl?apikey=…`
         URL since cross-seed fetches it itself). Per-connection storage + minted key, secrets redacted. Pushes
         *releases*, not config. The `.torrent` is fetched only on a confirmed match → strictly less tracker
         load than the consumer polling + grabbing (grabs left seeding, no hit-and-run).
   - [x] **Announce source (new-release tap)** — derive the "what's new" stream by tapping the search-cache
         write-back (`storeBestEffort`), diffing fresh GUIDs (`tzn.GUIDFor`) against the prior cached set
         (`SearchCacheStore.FetchAny` before the upsert — reads the expired-but-present row, so the diff works
         on a miss and survives restart) for RSS/empty-query fills only (`isEmptyQuery`), with an
         instance-namespaced, hard-bounded announced-GUID dedup window. Default policy: **announce only what a consumer is already polling** (zero added tracker
         load); a proactive per-indexer RSS watcher for un-polled trackers is a deferred opt-in (more load).
   - [x] **Docs + divergence** — user docs (`website/docs/features/cross-seed-freeleech.md` + MkDocs nav) for the
         FL toggle/bypass, the per-app routing defaults, the qui + cross-seed v6 push setup, and the cross-seed v6
         snippet; deliberate divergences recorded in the registry + app-sync testdata READMEs (scenetime-api
         honor-empty, honor-feed pagination dilution, freeleech_mode routing, dropped kind CHECK).
- [x] **Better pagination support** — **issue #3**: a spec-correct, *superior-to-Jackett/Prowlarr*
      feed — honest `<newznab:response offset total>` (Jackett omits it), correct offset/limit
      windowing, and a paging-aware conditional-GET ETag (folds the page window so a revalidation
      of one page can't be answered 304 with another's body) — plus a qui-shaped JSON API envelope
      (`{results, total, hasMore, limit, offset}`). Page size stays default=max=100. Deep
      server-side multi-page *upstream* fetching is deferred (post-alpha → **issue #75**): `total`
      reflects the single engine fetch that backs every page of a query. Lenient offset/limit
      clamping is a recorded deliberate divergence (no strict spec-201 error). The disjoint-pages +
      stable-`total` property Prowlarr violates (#1428) is now a **standing test** (feed + JSON +
      shared pipeline: `TestFeedCrossPageNoDuplicate`, `TestSearchJSONEnvelopeCrossPage`,
      `TestSearchReleasesCrossPageDisjoint` / `TestSearchReleasesTotalIsHonest`).
- [x] **More \*arr sync targets** — Lidarr / Readarr / Whisparr shipped (Mylar still backlog). The
      Phase-10 sync contract (target-neutral `DesiredIndexer` reconciled behind the `Target` interface)
      reuses the Servarr v3 driver with the indexer API version parameterized (v1 for Lidarr/Readarr,
      v3 for Whisparr); each adds a thin constructor + golden coverage.
- [~] **Upbrr credential sync** — **moved to the Tier-1 backlog** (consciously-accepted alpha gap; decided
      2026-06-30, so it no longer gates Phase 11 / the Web UI). harbrr, as the single source of truth for
      tracker auth, would **push** credentials into Upbrr (the same outbound model as the Phase-10 \*arr/qui
      push) so a tracker configured once in harbrr provisions Upbrr automatically. **Blocker:** Upbrr exposes
      no machine integration — every `/api/app/*` endpoint is session-guarded (password → `ua_web_session`
      cookie + CSRF, no `X-Api-Key`) and is an internal UI RPC of a primarily-desktop (Wails) app, not a
      documented contract. Gated on a design conversation with the autobrr/Upbrr team for a stable
      credential-push API, exactly like *harbrr → autobrr push*. Full scope + design sketch in the Tier-1
      backlog entry.

### Pre-alpha hardening (operability + polish — from the 2026-06-25 review)

- [x] **Runtime-tunable config — DB-backed settings store** *(alpha gate)* — **shipped** (#70 + #71 + the
      `cleanup_interval` follow-up). A DB-backed `app_settings` table (migration 0006) + `GET/PUT
      /api/cache/config` let the operator tune every cache knob at runtime **without a restart**:
      `SearchCache` reads an atomically-swapped `cacheTuning` per request, and the `enabled` toggle works
      live because the cache decorator is **always installed and self-gates** (`cmd/harbrr` `buildSearchCache`)
      — no add/remove-decorator dance needed. **All cache knobs are now runtime-tunable**: `enabled`,
      `rss_ttl`, `keyword_ttl`, `thin_ttl`, `thin_threshold`, `refresh_ahead_pct`, `negative_ttl`, and
      `cleanup_interval` (the cleanup ticker re-reads its interval each cycle, so a change applies on the
      next cycle). The per-indexer `cache_ttl`/`timeout` overrides were already DB-backed. **Deliberately
      stays in the config file** (deploy-time / security): data dir, DB path, listen address, base URL,
      **secrets/encryption key (must stay out of the DB it protects)**, auth mode + IP allowlist/trusted
      proxies.
- [x] **User-facing docs** — the **full operator set** shipped (#76 + the 2026-06-30 doc-gap pass):
      MkDocs site with **Getting Started / Install**, **Configuration reference**, **Adding an indexer**,
      **App Sync setup**, **API / Swagger pointer**, plus feature pages for the search cache, circuit
      breaker, usenet/Newznab, cross-seed & freeleech, and **pagination**. The root **`README.md`** is
      fixed (` ```mermaid ` fences, "Alpha — operated via the API" status, roadmap points at `plan.md`),
      and the internal-doc refresh is done (`docs/ideas.md` has since been retired, its design merged
      into `docs/architecture.md`; `docs/highlights.md` has since been retired, its user-facing content
      folded into `README.md` and the docs site). The 2026-06-30 audit (docs vs shipped features
      vs the OpenAPI surface) found the set accurate; the only gaps it surfaced — prose pagination, a
      stale API table, no pacing note — are closed. **Decision: the full operator set is the gate, and
      it is met.**
- [x] **Code cleanup (non-blocking)** — the scaffolding + dead-code review found **no alpha blockers**:
      codebase is clean (no `panic`/`TODO`/`FIXME` in non-test code, no parsed-but-dead config; OIDC `501`,
      two AnimeBytes parity nuances, and the captcha boundary are intentional deferrals). `deadcode -test`
      found only **3 unused option setters** (`auth.WithClock`, cardigann `WithSolver`, `registry.WithTimeout`)
      — **decision (2026-07-03): removed all three** (no speculative forward-API per AGENTS.md; zero callers
      incl. tests; each capability stays reachable — auth timestamps use `time.Now` directly, the solver is
      wired via `SolverOption`, the registry fallback timeout via `defaultHTTPTimeout` + the per-instance
      `timeout` setting). Shipped with the #19 engine-correctness batch.

## Phase 12 — Web UI

- [x] **Web UI** — the management dashboard (indexer grid, add/edit forms, manual search, stats);
      depends on the Phase 4 management API. **Gated behind Phase 11 — does not start until the alpha is
      feature-complete.** **Stack: match qui's** — **verified 2026-07-03 against the qui repo**: Vite +
      React 19 + TypeScript + Tailwind CSS v4 + shadcn/ui + TanStack Router/Query/Table/Form, embedded
      qui-style (`web/` + `go:embed`). (Interactive
      **Swagger UI already shipped** at `/api/docs`, separate from the SPA — the web UI just links to
      it; raw spec at `/api/openapi.yaml`.) **Shipped 2026-07-04 (#54, `36d7bd0`)** — full SPA
      (indexers, search, applications, cache stats, Proxies & Solvers, settings) embedded in the
      binary; follow-up fixes #56/#57 landed in #60.

---

## Beyond the alpha — backlog (demand-gated)

Everything here is **off the alpha critical path** — built when a real user needs it. The alpha's
core-thesis differentiators (RSS-feed caching, fabric-wide pacing, cross-seed) were pulled **into**
Phase 11; what remains below is adoption smoothing, Web-UI-paired product polish, and the demand-gated
tail. New items carry lighter detail (*detail TBD*); fill in as we have it.

### Tier 1 — Adoption

- **Jackett/Prowlarr migration import** — import indexer instances + credentials + category overrides.
  Read creds from the **Prowlarr SQLite database** (`prowlarr.db`, the plaintext `Indexers.Settings` JSON
  column) — NOT the REST API, whose `SchemaBuilder` masks `ApiKey`/`Password` with `********`. Needs a
  **Prowlarr-impl → harbrr-def name table** for Prowlarr-native trackers harbrr serves as Cardigann (e.g.
  HDSpace — tracked in autobrr/harbrr#42). Jackett's RSA/DPAPI-encrypted config falls back to guided re-entry.
  **Consciously deferred from the alpha** (Phase 11 ships manual re-entry); this lowers the switching cost
  afterward and is the highest-leverage post-alpha adoption win.
- **harbrr → autobrr push** — a *native release push* (data-plane), distinct from config sync: scraped
  releases reach autobrr's filters immediately instead of waiting on RSS polling. Note autobrr **already
  consumes harbrr as a Generic Torznab feed today** (zero-code drop-in, same as Prowlarr), so this only
  earns its keep for sub-poll latency on non-IRC trackers — a marginal, family-only win. **Blocked on a
  design conversation with the autobrr team** before any implementation: scope the push contract/endpoint,
  whether it's wanted, and how it relates to announce. Lowest-priority Tier-1 item until that lands.
- **Upbrr credential sync** — *(moved here from Phase 11, 2026-06-30 — a consciously-accepted alpha gap.)*
  Upbrr (`autobrr/upbrr`) is an **upload-preparation** tool with its **own** ~30 bespoke tracker drivers; it
  doesn't consume a Torznab feed, it logs in to *upload*. harbrr, as the single source of truth for tracker
  auth, would **push credentials** onto Upbrr's own defs so a tracker configured once in harbrr provisions
  Upbrr — distinct from the \*arr/qui push (which pushes harbrr's *feed*). The \*arr/qui machinery does
  **not** carry over (no feed URL, no embedded-slug ownership, no minted harbrr feed key, no caps/protocol).
  - **Blocker (why it's gated, not just deferred):** Upbrr exposes **no machine integration**. Every
    `/api/app/*` endpoint is session-guarded — Upbrr web auth is password → `ua_web_session` cookie + CSRF
    (`/api/auth/bootstrap` + `/api/auth/login`), **no `X-Api-Key`/bearer** — and these are internal UI RPCs
    of a primarily-desktop (Wails) app, not a documented contract. Building against them means reverse-
    engineering an internal API that can break on any Upbrr release and requires the operator to run Upbrr
    in web-server mode with a known password. **Gated on a design conversation with the autobrr/Upbrr team**
    for a stable credential-push API (same posture as *harbrr → autobrr push* above).
  - **Target shape (when unblocked):** Upbrr per-tracker creds live in `config.TrackerConfig`
    (`TrackersConfig.Trackers map[string]TrackerConfig`, keyed by Upbrr's tracker code): credential fields
    `APIKey`, `ApiUser`+`ApiKey` (PTP-style), `Username`+`Password`, `Passkey`, `AnnounceURL`/`MyAnnounceURL`,
    `BhdRSSKey`, `PronfoAPIKey`, `OTPURI` (2FA seed) — alongside upload-behavior fields (`ModQ`/`Draft`/`Anon`/
    `UploaderName`/`ImageHost`/…) harbrr must **never** touch. Config creds push via `GetConfig`→merge→
    `SaveConfig`; cookie/session creds use `ImportTrackerAuthCookieContent` + `LoginTrackerAuth`(+2FA).
    Discovery via `ListKnownTrackers` / `ListTrackerAuthCapabilities`.
  - **Decided scope (v1 intent):** push **everything** harbrr can supply — apikey/passkey/user+pass/announce/
    upload-relevant fields — for trackers both tools support, harbrr-slug ↔ Upbrr-code matched by site host
    (manual override), **merge-safe** (never clobber Upbrr's upload settings, never delete an Upbrr tracker).
  - **harbrr-side reuse:** `validateKind` single-source + `newDriver` extension (`internal/appsync/`), the
    encrypted per-connection secret store (but storing Upbrr's web username+password, not a minted harbrr
    key), payload_hash idempotency, redaction chokepoints, and the CRUD+test+sync HTTP surface. Driver auth =
    bootstrap/login → cache `ua_web_session` + CSRF per request. Structurally closer to `internal/announce/`
    (fire-and-forget, no feed-URL ownership) than to feed-based app-sync.

### Tier 2 — Product / UX (pair with the Web UI)

- **Stats / search history** (query/grab/auth event log + query API; the auth log populates
  `api_keys.last_used_at`, left unwritten in Phase 4) **+ notifications** (Discord/webhook, pluggable).
- **Fleet-wide indexer status** — a `GET /api/indexers/status` aggregate over all indexers (the
  per-indexer `GET /api/indexers/{slug}/status` exists; this is the roll-up). The health event log +
  derived per-instance status already exist (Phase 6); this is the fan-out endpoint. Pairs with the
  Web UI dashboard.
- **Backup / restore** (config + database): scheduled + manual, using the redacted/encrypted export (`docs/security.md`)
  (secrets behind a `<redacted>` sentinel; including secrets is a separately-passphrase-encrypted opt-in).
- **OIDC authentication** — implement the flow stubbed in Phase 4 (`/api/auth/oidc/*` return 501 today;
  only a config seam exists). Pairs with the Web UI auth surface.
- **User-configurable request rate** — a global default + per-indexer override. Phase 6 paces per target
  domain from the def's `requestDelay`; the `ClientParams.RateInterval` seam already carries the value, so
  this is a settings surface + plumb-through. Pairs with the Web UI settings.

### Tier 3 — Reach / more trackers (demand-gated)

The native framework is **leverage-complete** — it covers **every auth shape**, so what remains is a
per-tracker tail, not a missing capability. (Usenet/Newznab, the one *capability* gap, moved up to
Phase 11.)

- **Native-driver long tail** — 12 drivers shipped (see the `native-driver` label +
  `docs/native-indexer-pattern.md`): AvistaZ family,
  IPTorrents, MyAnonamouse, FileList, BroadcastTheNet (#62), and the #63 set — Gazelle base (Redacted +
  Orpheus), PassThePopcorn, GazelleGames, AnimeBytes, HDBits, BeyondHD, TorrentDay. Remaining is the
  demand-gated tail: SpeedCD + the cookie-scrape tail (AlphaRatio/FunFile/BitHDTV…), MTeam/NorBits/SceneHD
  (passkey, reuses FileList/HDBits), the username/password Gazelle sites (DICMusic/GPW/BrokenStones, need a
  login-flow addition), and Nebulance. Build per tracker on demand.
- **Live-validate the #63 drivers** — all 8 are **offline-gated but live-untested** (no operator creds);
  BroadcastTheNet (#62) is the one live-confirmed bespoke driver. Per-tracker Prowlarr differential + a
  `/dl` grab via the container, when creds exist. Tracked under the `native-driver` label and
  `internal/smoke/README.md`.

### Tier 4 — Live validation (offline-proven; needs infra in the operator's stack)

- **Live retest of the two no-tracker auth patterns** (moved from Phase 9's "every auth/fetch pattern
  live", which is otherwise green). Both are **offline-proven**; they need a qualifying tracker/proxy in
  the operator's stack to confirm live:
  - **cookie / manual-cookie** (`ManualCookieSolver`) — none of the stack's Cardigann trackers use cookie
    login (IPTorrents/MyAnonamouse are native cookie drivers, already resolved). Test by configuring any
    Cardigann tracker with `solver_type=manual_cookie` + a browser-exported cookie (e.g. a temporary
    HD-Space flip), or a real `method: cookie` tracker if one is added.
  - **per-indexer proxy (HTTP/SOCKS5)** — no HTTP/SOCKS proxy in the stack (only FlareSolverr). Test by
    standing up a local proxy (microsocks/tinyproxy) and routing any search via `proxy_type`+`proxy_url`;
    the doer/transport plumbing is offline-tested. SOCKS4 unsupported (`x/net/proxy` has no socks4 dialer).

### Tier 5 — Future / explicitly demand-gated

- **Release intelligence / metadata correlation** — smarter cross-app release matching and metadata
  correlation (README Phase 3/4). *Detail TBD.*
- **Distributed architecture** — multi-node / horizontally-scaled deployments (README Phase 4), beyond
  the single-binary model. *Detail TBD.*
- **Postgres** — **out of the alpha roadmap.** harbrr is single-user self-hosted, where SQLite is the
  right default, not a stopgap; Postgres only earns its keep for shared / multi-instance deployments.
  Build it **when a real multi-instance user needs it**, not on a schedule — there is no committed phase.
  - **Standing invariant (so it plugs in later without a rewrite):** `internal/database/dbinterface`
    stays **dialect-portable** — all repository SQL routes through the interface and its `Rebind`
    (`?`→`$N`) seam, no SQLite-specific SQL or driver types leak to callers, and schema changes ship as
    SQLite migrations a Postgres backend can mirror. Keeping this seam clean is required work **now**;
    implementing Postgres is **not**. (AGENTS.md: "keep the interface clean so it can be added later.")

---

## Search-results caching — **shipped (#60, 2026-06-24)**

The one headline harbrr can offer that Prowlarr/Jackett do not: **a search-results cache, because
harbrr is the Torznab *server*, so a cache hit spares the *tracker's* infrastructure, not just
harbrr's.** This is the most-requested differentiator and directly serves harbrr's stated reason to
exist. **Shipped ahead of Phase 12** (built off the locked design below); the only deferred piece is the
Web-UI hit-ratio surface (Phase 12). User docs: `website/docs/features/search-results-cache.md`. The
design below is the as-built record (TTL values + encryption + v1 scope settled 2026-06-23; see
"Decisions locked" at the end).

**Motivating evidence (from prod logs + PT-user patterns, 2026-06-23).** There are **two distinct load
problems**, with different solutions:

1. **Redundant duplicate polling (the bulk of the waste).** The dominant tracker traffic is **empty-query
   RSS polling** — Sonarr/Radarr/Prowlarr re-issuing the *identical* `categories[]=…&perPage=100&
   sortField=created_at` fetch to UNIT3D trackers every ~6 min (the luminarr/lst/onlyencodes timeout
   loop). It is multiplied by **multi-instance setups**: many PT users run a 1080p *and* a 4k *arr
   instance, and both poll/search the **same** tracker for the same title — and because resolution lives
   in the *arr's quality profile and is filtered **client-side**, those requests are **byte-identical**
   (the Torznab query carries no resolution). This whole class is killed by the cache *existing*: identical
   `(instance, query)` → one entry serves every instance, and singleflight collapses concurrent misses —
   **TTL-independent**, zero tuning. This is the headline win.
2. **Staggered-resolution releases (the freshness tension).** On TV especially, 720p drops first, then
   1080p, then 4k minutes later. A user (or a 4k-only instance) relying on RSS must see each drop promptly,
   so a long cache on a *thin* result set would hide the later resolutions until expiry. The antidote is
   **adaptive TTL on result richness** (thin/empty sets get a short TTL so they re-check fast and catch the
   next resolution; rich sets get the long TTL), plus **stale-while-revalidate** so the tracker still sees
   ≤1 fetch per TTL regardless of client count.

**Heterogeneous user base (drives the knobs).** harbrr is for *all* PT users, not one workflow: some are
**announce/autobrr-primary** (RSS is a tolerant backstop → aggressive TTL fine), others are **RSS-primary**
(no racing; RSS is how releases get grabbed → TTL must stay tight). The natural unit of that split is the
**tracker**, so the **per-indexer TTL override is v1, not phase-2.**

> **Design (decided direction).** Mirror qui's proven precedent — it already caches Torznab search
> results in SQLite (`qui/internal/models/torznab_search_cache.go`, key built in
> `qui/internal/services/jackett/service.go:1203` `buildSearchCacheSignature`) — but **drop qui's
> multi-indexer coverage/superset machinery** (`selectCacheEntryForCoverage`, `indexer_matcher`): qui
> fans one search across many indexers, whereas **harbrr serves exactly one indexer per Torznab request**
> (`registry.Indexer(slug)` → one adapter), so the key is a single `(instance, query)` pair.
>
> - **Seam:** cache-aside around `idx.Search(ctx, query)` in the registry adapter
>   (`internal/indexer/registry/adapter.go`) — downstream of login/engine, **upstream** of
>   dedupe/category-filter/pagination/`/dl`-rewriting (`internal/web/torznabhttp`). The cached value is
>   `[]*normalizer.Release` **before** `/dl` rewriting, so it's independent of the caller's
>   base-URL/apikey and one entry serves every client.
> - **Key:** SHA-256 over a **schema-versioned**, canonicalized payload — `version | instance_id |
>   search_mode(t=) | keywords(trim+casefold) | categories(sorted,deduped) | imdbid/tmdbid/tvdbid/
>   tvmazeid/traktid/doubanid/rid | season/ep/year | artist/album/label/track/author/booktitle`.
>   Categories **are** in the key (they change the outbound tracker request). `limit`/`offset` are
>   **not** (applied post-cache, so different pagination reuses one entry). Bump the version constant to
>   invalidate the whole format at once.
> - **Storage:** **SQLite as source of truth** (port + simplify qui's table) so a harbrr restart does
>   **not** trigger a thundering re-poll of every tracker. Reuse `dbinterface` + a forward-only migration
>   (`0004_search_cache.sql`). An in-memory L1 (autobrr `ttlcache`, already vendored by qui, or
>   `hashicorp/golang-lru/v2`) is **optional/phase-2**, added only if profiling shows deserialization
>   cost. Table shape: `cache_key PK · instance_id FK ON DELETE CASCADE · results_json BLOB ·
>   total_results · cached_at · last_used_at · expires_at · hit_count`, indexed on `instance_id` and
>   `expires_at`.
> - **Cache collapse = the multi-instance fix (TTL-independent):** because the 1080p and 4k instances
>   issue byte-identical queries, they hash to the **same** key and **share one entry** (each filters
>   client-side by its own quality profile). No tuning required — the cache existing is the fix.
> - **Singleflight (the win qui lacks):** wrap the cache-miss path in `golang.org/x/sync/singleflight`
>   keyed on the cache key, so N concurrent identical misses (Sonarr + Radarr + Prowlarr, or 1080p + 4k
>   instances, polling the same indexer in the same second) collapse to **one** tracker request. This is
>   what tames the thundering herd when a new resolution drops and every instance searches at once.
> - **Stale-while-revalidate (v1 — the real RSS load fix):** on a hit where the entry is past a
>   *refresh-ahead* threshold (e.g. ≥80% of TTL elapsed), serve the cached value **immediately** and kick
>   off **one** background refresh (guarded by the same singleflight key, success-only write-back). The
>   client never waits, the cache stays warm, and the tracker sees **≤1 request per TTL per indexer
>   regardless of client count** — decoupling client latency from tracker load. Background fetch uses a
>   detached context (not the request's) so it survives the client disconnecting.
> - **Only cache success:** cache successful responses including legitimately-empty result sets
>   (negative caching of "0 results" is good). **Never** cache errors (the 5xx/timeout/TLS failures from
>   the prod logs). A short-TTL error/circuit-breaker entry is a possible phase-2 add.
> - **TTL tiers + adaptive richness (decided values):** global defaults + per-indexer override (copy the
>   `resolveTimeout` per-instance-setting pattern, `internal/indexer/registry/client.go:110`), **both v1**.
>   - **RSS / empty-query: 5 min** — the multi-instance collapse + singleflight already kill the herd here,
>     so keep it short for RSS-primary users; per-indexer bump for announce-covered trackers.
>   - **Keyword / ID: 30 min** — specific-title result sets are near-static, so a long TTL is efficient…
>   - **…adaptive override — thin/empty result set: 2 min** — …*except* mid-stagger. A result set at/below a
>     small count threshold caches for the short TTL so it re-checks fast and catches the next resolution
>     (720p→1080p→4k); rich sets keep the full TTL. This is the staggered-release antidote qui lacks.
> - **`nocache=1` bypass (v1):** a `nocache=1` query param skips the lookup and forces a live fetch +
>   write-back — the power-user "I know it just dropped" override for manual searches.
> - **Invalidation:** TTL primary; FK cascade on instance delete; explicit purge on instance
>   settings-change/disable (hook the existing registry adapter-cache invalidation in
>   `internal/indexer/registry/registry.go`); periodic `CleanupExpired`; a manual flush endpoint.
> - **Secrets at rest (decided):** cached `Release.Link`/`Magnet` embed tracker passkeys, so
>   `results_json` contains secrets — same trust level as the session cookies / encrypted settings
>   already in the `0600` DB, acceptable for single-user self-hosted. **Decision: rely on the `0600`
>   DB + never-log posture** (matches how session cookies are stored today); the blob must never hit a
>   log. Not routed through `internal/secrets` — the per-row AES cost on every cache read/write isn't
>   worth it at this trust level.
> - **Observability is the selling point:** surface `hit_count` / hit-ratio / entries / approx-size
>   (qui's `Stats` is the template) in the management API and the Web UI — "X% of searches served from
>   cache" is the metric that proves the value over Prowlarr.

Build order (all shipped in #60; each landed in its own commit with green tests):

- [x] **Store + migration** — `0004_search_cache.sql` + a `SearchCacheStore` behind `dbinterface`
      (`Fetch`/`Store`/`CleanupExpired`/`Flush`/`InvalidateByInstance`/`Stats`/`Touch`/`BumpHits`).
- [x] **Cache-aside + singleflight** — wrap `idx.Search` via a `cachedIndexer` decorator in the registry;
      versioned canonical key (the multi-instance collapse falls out for free); only-cache-success (incl.
      empty); `golang.org/x/sync/singleflight` coalescing on miss; a `Fetch` error degrades open.
- [x] **TTL tiers + adaptive richness + per-indexer override + `nocache`** — RSS/empty-query **5 min** vs
      keyword/ID **30 min** globals; thin/empty result set → **2 min** adaptive clamp (shortens only);
      per-instance `cache_ttl` override; `nocache=1` bypass (context-threaded, both surfaces).
- [x] **Stale-while-revalidate** — refresh-ahead threshold on hit; one detached-context background refresh
      per key (singleflight-guarded, success-only write-back); serve cached value immediately.
- [x] **Lifecycle** — periodic `CleanupExpired` + coalesced hit-touch flush on the cleanup ticker;
      invalidation on instance mutation/disable/delete.
- [x] **Observability + control** — `GET /api/cache/stats` (entries/hitRatio/size/timestamps) + `POST
      /api/cache/flush` in the management API (OpenAPI + drift-test green). Web-UI hit-ratio surface is
      Phase 12.

**Decisions locked (2026-06-23 use-case discussion):**
- **TTL values:** RSS/empty-query **5 min** · keyword/ID **30 min** · thin/empty result **2 min** (adaptive).
  All overridable per-indexer and globally.
- **v1 scope** (not phase-2): adaptive thin-result TTL, stale-while-revalidate, per-indexer override, and
  the `nocache=1` bypass — driven by the two-load-problem analysis above (multi-instance collapse +
  staggered releases) and the heterogeneous RSS-primary vs announce-primary user base.
- **Encryption of `results_json`:** **rely on the `0600` DB + never-log posture** (matches how session
  cookies are stored today); not routed through `internal/secrets` — the per-row AES cost isn't worth it
  at this trust level for single-user self-hosted.
- **Still phase-2 (deferred):** in-memory L1 over SQLite (only if profiling shows deserialization cost);
  short-TTL error/circuit-breaker caching.

---

## Standing rules while building (see AGENTS.md)

- Never hand-edit `internal/indexer/definitions/vendor/` — absorb differences in the engine.
- Never log/commit secrets. Always `-race -count=1`. Keep functions small (the linters enforce it).
- One plan item per commit; conventional-commit messages; no AI attribution lines.
- **Document user-facing wins as you go.** When a phase lands a user-facing improvement,
  add it to `README.md` and/or the docs site (`website/docs/`) — stated as harbrr's own
  capability, positively and factually (respectful of Prowlarr/Jackett/qui, not disparaging).
