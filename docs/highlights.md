# harbrr highlights

A running list of where harbrr is **deliberately better** than the tools it stands
beside — **Prowlarr** (the external .NET service it replaces), **Jackett** (the
Cardigann engine it reimplements), and **qui** (its autobrr-family sibling, the
reference for db/auth/security). It's the source for "why harbrr" copy when the
site/docs land later.

**Honesty rules for this file** (it will be published):

- Every item cites real code/docs and a status: **`[shipped]`** (implemented +
  tested on `main`/an open PR), **`[partial]`** (some now, more in a later phase),
  **`[planned]`** (designed, not built). Don't list a `[planned]` item as if it
  ships today.
- No fabricated metrics, and no claims about a competitor's internals we can't
  verify — state *harbrr's* property and compare directionally.
- Per the §13 positioning rule in `docs/ideas.md`, harbrr is **not** marketed as a
  "Prowlarr replacement" until app-sync/migration/UI/coverage exist. These are
  concrete advantages, not that claim.
- **Maintain it as you go:** when a phase lands a user-facing or competitive
  improvement, add it here (see `docs/plan.md` standing rules).

---

## Search-results cache (the headline Prowlarr/Jackett can't match)

- **A cache hit spares the *tracker*, not just harbrr.** Because harbrr is the Torznab
  *server*, a cached result means the request **never leaves for the tracker** — unlike
  a cache in Prowlarr/Jackett, which still hits the tracker. One entry per
  `(indexer, query)` is shared by every client, so a 1080p and a 4K *arr instance
  (which issue byte-identical queries — resolution is filtered client-side) **collapse to
  one tracker request**, and concurrent identical misses coalesce via `singleflight`.
  *(`internal/indexer/registry/searchcache.go`, #60)* `[shipped]`
- **Tuned for the real load, not a flat TTL.** RSS/empty-query polls cache **5 min**,
  keyword/ID searches **30 min**, and a thin/empty result set clamps to **2 min** so
  staggered resolution drops (720p→1080p→4K) are caught fast — a clamp that only ever
  shortens. **Stale-while-revalidate** serves a near-expiry hit instantly and refreshes
  once in the background, so the tracker sees ≤1 request per TTL regardless of client
  count. Per-indexer `cache_ttl` override + a `nocache=1` bypass.
  *(`internal/indexer/registry/searchcache_ttl.go`, `website/docs/features/search-results-cache.md`)*
  `[shipped]`
- **Observability is the proof.** `GET /api/cache/stats` surfaces entries / **hit-ratio** /
  size — "X% of searches never touched a tracker" is the metric that demonstrates the
  win over Prowlarr; `POST /api/cache/flush` for control. *(`internal/web/api/cache_handlers.go`)*
  `[shipped]`

## Cardigann parity, proven live

- **Byte-for-byte parity with Prowlarr on real trackers, at scale.** The Phase 5 live
  smoke matched 5 trackers; the **Phase 9 run (2026-06-16) widened this to 14**, fully
  automated from Prowlarr's own DB — **13/14 passed with count parity 1.00** vs the
  live Prowlarr (1 Prowlarr-side skip), and it confirmed the **apikey, form-login, and
  Cloudflare/FlareSolverr** auth paths live, plus real `.torrent` grabs for URL-token
  trackers. *(It also surfaced a `[Tracked]` grab gap — session/header-auth downloads
  need to route through `/dl` — and a daemon-breaking panic now fixed; honest
  accounting in the README.)* *(`internal/smoke/README.md`, `scripts/phase9-smoke.sh`)*
  `[shipped]`
- **Caught real engine parity gaps offline tests can't.** The live smoke surfaced
  and fixed two Jackett behaviors Go doesn't get for free: Newtonsoft's JSON date
  auto-conversion (`DateParseHandling.DateTime`), which every UNIT3D-API def relies
  on, and Jackett's "login never fails on HTTP status" rule.
  *(`internal/indexer/cardigann/selector/jsonpath.go`, `.../login/methods.go`)* `[shipped]`
- **Search → grab end-to-end, Sonarr-orchestrated.** A real Sonarr added harbrr
  (in Docker) as a Torznab indexer, passed its connectivity test, searched it, and
  grabbed a release — downloading in the live qBittorrent client with
  `indexer = harbrr` in Sonarr's history. The served download link resolves to a
  real `.torrent`. *(`internal/smoke/README.md`)* `[shipped]`

## Packaging & architecture

- **Single static Go binary, zero cgo.** Pure-Go throughout, including the SQLite
  driver (`modernc.org/sqlite`), so harbrr cross-compiles cleanly to Linux/macOS/
  Windows × amd64/arm64 with `CGO_ENABLED=0` — no .NET runtime, no C linkage.
  *(vs Prowlarr/.NET — `Dockerfile`, `.github/workflows/ci.yml` 5-target matrix)*
  `[shipped]`
- **Minimal, hardened container.** Multi-stage build to a non-root (uid 1000)
  Alpine image: data dir `0700`, db + side files `0600`, a `/healthz` HEALTHCHECK,
  and `ca-certificates`/`tzdata` for tracker TLS + locale-correct dates — security
  posture is in the image, not a runbook. *(vs Prowlarr — `Dockerfile`)* `[shipped]`
- **SQLite-first behind a clean interface.** One implementation today, no dual-DB
  maintenance burden; the `dbinterface` seam keeps Postgres possible later without a
  rewrite. *(vs Prowlarr's dual SQLite/Postgres — `internal/database/dbinterface`)*
  `[shipped]`
- **Family-native by construction.** A Go/SQLite/GPL-2 service the autobrr family
  already maintains the substrate for, replacing the external heavyweight .NET
  dependency the family's own docs tell users to install. *(vs Prowlarr —
  `docs/ideas.md` §1–§2)* `[shipped]`

## Security

- **Three-class credential model.** Login password → **argon2id** (never
  recoverable); API keys / session tokens → **SHA-256**, shown once; tracker
  credentials → **AES-256-GCM** (replayed at request time). Anything harbrr only
  verifies is hashed; only what it must replay is encrypted — so a database leak
  never yields the admin password or an API key. *(`internal/secrets`,
  `docs/ideas.md` §9)* `[shipped]`
- **Encryption is always on.** First run with no key configured auto-generates a
  `0600` keyfile and uses it; true plaintext is reachable only behind an explicit
  `allow_plaintext` opt-in that fails closed — no silent plaintext in a copied
  `.db` or a pasted bug report. *(`internal/secrets/keyring.go`)* `[shipped]`
- **Fail-loud on a wrong/changed key.** A startup canary is decrypt-verified every
  boot; a changed key (or a plaintext↔encrypted flip) refuses startup rather than
  silently dropping or re-encrypting garbage. *(`internal/secrets/canary.go`,
  `cmd/harbrr/serve.go`)* `[shipped]`
- **Three concrete hardenings over qui.** (1) The AEAD is **AAD-bound** to
  `instanceID + setting`, so a ciphertext can't be replayed across rows/fields
  (qui passes none); (2) a **`key_id` is stored** with every record, making key
  rotation possible later; (3) the encryption key is **separate from the session
  secret** (a DB-backed session store with no signing secret). *(vs qui —
  `internal/secrets/aead.go`, `keyring.go`; `internal/secrets/testdata/README.md`)*
  `[shipped]`
- **Type-aware secret classifier, corpus-audited.** Decides which settings are
  secret from the definition's field *type* + name (so a `usetoken` checkbox isn't
  redacted but a text-typed `cookie`/`apikey`/`2facode`/`pin` is), pinned by a
  golden audit over all 558 vendored definitions so a re-vendor can't silently
  introduce an unencrypted credential. *(`loader.SettingsField.IsSecret`,
  `loader/testdata/secret_audit.txt`)* `[shipped]`
- **One redaction chokepoint.** Secret query params (passkey/apikey/token/…),
  `Authorization`/`Cookie` headers, and URL userinfo are redacted at every log,
  error, and trace site — including the served Torznab self-URL and request logs.
  *(`internal/http/redact.go`)* `[shipped]`
- **Hardened web-UI auth (qui model).** First-run setup creates a single admin;
  cookies are `HttpOnly` + `SameSite=Lax` (+ `Secure` behind TLS) with token renewal
  on login; API keys are header-authenticated. *(`internal/auth`, `internal/web/api`)*
  `[shipped]`
- **Offline key rotation.** `harbrr rotate-key` (daemon stopped) re-encrypts every
  stored tracker secret + the canary from an old key to a new key in one atomic
  transaction, validating (dry-run decrypt of every row) before any write — a wrong
  old key fails loud with the store untouched. The single-key crypto core is
  unchanged; a database leak is recoverable without re-entering credentials.
  *(`cmd/harbrr/rotate_key.go`)* `[shipped]`
- **Redaction beyond URLs/headers.** A shared error chokepoint scrubs both API test
  errors and persisted health-event detail (the wired path). Defensive helpers also
  exist for FlareSolverr request/response bodies (cookies/cf_clearance/userAgent/page
  HTML) and for whole-userinfo proxy-URL scrubbing — built and tested but not yet
  wired, since no path logs those today. *(`internal/http/redact.go`)* `[shipped]`
- **The passkey never reaches the feed.** For a resolver-needing tracker, the served
  Torznab feed carries an opaque, AEAD-sealed `/dl` proxy URL — not the
  passkey-bearing download link — and a stable, passkey-free guid. harbrr resolves
  and fetches the `.torrent` server-side at grab time, so a leaked feed never exposes
  a tracker credential (Jackett/Prowlarr put the passkey straight in the feed).
  *(`internal/web/torznabhttp/dltoken.go`, `internal/web/torznabhttp/handler.go`)* `[shipped]`

## Operational safety (anti-blacklist + observability)

- **Greatly reduces tracker IP/account blacklisting risk.** Every outbound request is paced
  by a process-wide **per-host rate limiter** (`x/time/rate`, burst 1, no eviction —
  bounded host keyspace), with a per-request timeout and bounded **429/503 backoff
  that honors `Retry-After`** (never loops). Pacing + backoff compose with the
  request context: a cancelled request aborts a token wait or a backoff sleep with
  no reservation leak. *(`internal/indexer/registry/pacedclient.go`)* `[shipped]`
- **Per-indexer health & status.** Failures classify into `auth_failure`,
  `rate_limited`, `parse_error`, `anti_bot` and append to a per-indexer event log;
  `GET /api/indexers/{slug}/status` surfaces a derived health + recent events with
  credential-scrubbed detail — so an operator sees *why* an indexer is unhealthy,
  not just that a search returned nothing. *(`internal/database/health.go`,
  `internal/web/api`)* `[shipped]`
- **Per-indexer proxies.** Route any indexer through an HTTP or SOCKS5 proxy
  (per-instance, the URL encrypted at rest), so a geo-blocked or IP-flagged tracker
  works without proxying the whole daemon. *(`internal/indexer/registry/client.go`)*
  `[shipped]` (SOCKS4 not supported — demand-gated)
- **FlareSolverr Cloudflare solver.** Clears an anti-bot interstitial via a
  FlareSolverr instance (typed `/v1`, discard-and-replay with a UA-coupled,
  non-gzip browser header set), completing the pluggable solver seam alongside the
  manual-cookie fallback. *(`internal/indexer/cardigann/login/flaresolverr.go`)*
  `[shipped]` (offline-gated; live CF clear `[planned]`)

## Engine correctness & compatibility

- **ReDoS-safe regex by default.** Go's linear-time RE2 is the default; harbrr
  routes to `regexp2` (.NET semantics) only on four explicit triggers (opt-in,
  non-Latin script, RE2 compile-failure, .NET-only constructs) and bounds it with a
  match timeout. Prowlarr/Jackett run .NET's always-backtracking regex with no such
  guard. *(`internal/indexer/cardigann/regexadapter`)* `[shipped]`
- **Parity proven offline against Jackett.** The correctness oracle is
  Jackett-equivalent normalized output on the same saved input — differential-tested
  offline (port of Jackett's own GPL-2.0 assertions + golden fixtures across the
  compatibility matrix), so quirks are *matched, not enumerated*, without live
  tracker access. *(`internal/indexer/cardigann/parity`)* `[shipped]`
- **Zero silent definition drops.** Every def that fails schema-validation/parse is
  recorded on a visible skip-list with a reason; a test asserts the whole vendored
  corpus loads with an empty skip-list. *(`loader.LoadAll`)* `[shipped]`
- **Deterministic, byte-stable output.** Releases normalize to a canonical JSON
  shape with sorted categories and an injectable clock, so goldens are reproducible
  across runs and platforms. *(`internal/indexer/cardigann/normalizer`)* `[shipped]`
- **Standing compatibility suites.** Selector (`cascadia` vs AngleSharp) and
  .NET-date-format behavior are pinned by ongoing fixture suites treated as standing
  regression gates, not one-time checks. *(`selector/`, `dateparse/`)* `[shipped]`
- **Complete download resolver.** harbrr reproduces Jackett's full
  `CardigannIndexer.Download`: the `.DownloadUri` namespace, `before.inputs`/
  `pathselector`, download-selector templates, `infohash`→magnet (byte-for-byte
  MagnetUtil), `method: post`/`headers`, and `testlinktorrent` — so resolver-needing
  trackers (not just direct-link ones) grab correctly, resolved once at grab time.
  *(`internal/indexer/cardigann/search/download.go`, `.../grab.go`)* `[shipped]`
- **XML backend edge parity.** CDATA (literal content + boundary concatenation),
  comments, default/nested/redeclared/undeclared namespaces, and the `:has`/`:contains`
  selector shims all match AngleSharp's selectable output across HTML/JSON/XML,
  pinned by fixtures — not just the common RSS/Newznab shapes.
  *(`selector/xml_test.go`, `parity/testdata/matrix-xml-cdata`)* `[shipped]`
- **Caps/category correctness is a gate.** Sonarr/Radarr failures usually trace to
  capabilities/category behavior, not the result envelope — so the caps document +
  category tree + tracker→newznab mapping are explicitly oracle-tested. *(Phase 3 —
  `internal/torznab`)* `[shipped]`
- **Pagination that doesn't re-serve page 0.** harbrr pages a query's single tracker
  fetch into **disjoint** windows — page 1 (`offset=100`) never repeats page 0 — and
  emits an honest `<newznab:response offset total>` (Jackett's `ResultPage` omits it,
  leaving clients blind to the match count). Prowlarr forwards no offset to the tracker,
  so the next-100 request re-returns the first 100 (a duplicate page —
  [Prowlarr #1428](https://github.com/Prowlarr/Prowlarr/issues/1428) /
  [#1256](https://github.com/Prowlarr/Prowlarr/issues/1256)); harbrr's disjoint-pages +
  stable-total property is a standing test, not an accident. The conditional-GET `ETag`
  folds in the page window, so revalidating one page is never answered `304` with
  another's body. Advertising `<limits default="100" max="100"/>` (= max) also avoids the
  Sonarr `default<max` small-page crawl ([Sonarr #5373](https://github.com/Sonarr/Sonarr/issues/5373)).
  *(`internal/torznab/results.go`, `internal/web/torznabhttp/{handler,cacheinfo}.go`,
  `internal/torznab/caps.go`)* `[shipped]`
- **Definitions consumed byte-for-byte + drop-in overrides.** Vendored defs are
  never edited (all differences absorbed in the engine); a user drop-in directory
  takes precedence, so a hotfix or custom tracker needs no recompile or fork.
  *(`internal/indexer/definitions`, `docs/ideas.md` §10)* `[shipped]`
- **One engine covers whole families for free.** UNIT3D (~80 defs) and Gazelle (~6)
  come from the vendored Cardigann corpus, so harbrr doesn't need (and skips)
  per-family native drivers Prowlarr maintains. *(`docs/ideas.md` §6)* `[shipped]`
- **The one family the corpus can't express, covered natively.** AvistaZ / CinemaZ /
  PrivateHD / ExoticaZ use a login→Bearer `api/v1/jackett` auth that exceeds the
  declarative Cardigann format (0 vendored defs), so harbrr ships a native driver that
  plugs into the *same* registry, paced client, normalized release, caps mapper, `/dl`
  grab proxy, and redaction as the engine — reproducing Prowlarr's contract while reusing
  every seam instead of forking a parallel stack. Offline-gated against Prowlarr-derived
  goldens; the live differential is the Phase 9 gate.
  *(`internal/indexer/native/avistaz`)* `[shipped]`
- **Bespoke-C# trackers, served natively.** Trackers shipped as bespoke C# in *both*
  Jackett and Prowlarr (no Cardigann YAML to vendor) get native Go drivers on the same
  framework — each just a settings struct + request generator + parser over the shared
  registry/secrets/caps/`/dl` seams. Shipped: **IPTorrents, MyAnonamouse, FileList**
  (Phase 9.5) and **BroadcastTheNet** (#62), all live-confirmed; plus a #63 batch —
  **Redacted, Orpheus, PassThePopcorn, GazelleGames, AnimeBytes, HDBits, BeyondHD,
  TorrentDay** — across every auth shape (header key, passkey, POST-body, session cookie),
  with each tracker's credential kept out of logs and out of the served feed (passkey-
  bearing or cookie-authed downloads sealed behind `/dl`). The build map is in
  `docs/native-roadmap.md`. *(`internal/indexer/native/`)* `[shipped]` *(the #63 batch is
  offline-gated but **live-untested** — no operator creds; honestly tracked in
  `docs/coverage.md` §4)*

## Developer experience & governance

- **Readable, multi-instance indexer URLs.** A configured tracker's Torznab feed
  uses a stable, human-readable slug (`…/indexers/torrentleech/results/torznab`,
  default = definition id, renameable) and supports multiple instances of one
  tracker (e.g. two accounts) — vs Prowlarr's opaque auto-increment ids.
  *(`internal/domain`, `internal/indexer/registry`)* `[shipped]`
- **Spec-first management API with a drift test.** A hand-authored OpenAPI doc is
  embedded and served, and a test walks the live routes against it so the spec can
  never drift from the handlers. The spec is also rendered as **interactive Swagger
  UI at `/api/docs`** (public, read-only) — so the management API is browsable and
  testable without a web UI, the basis for an API-only team alpha. *(`internal/web/swagger`,
  `internal/web/api`; `make test-openapi`)* `[shipped]`
- **A complete control + data-plane API — drive harbrr entirely over JSON.** Beyond
  indexer CRUD, the management API exposes **JSON search** (`GET
  /api/indexers/{slug}/search`), **capabilities** (`…/capabilities`), a definition's
  **settings-field schema** (`GET /api/definitions/{id}`, for rendering an
  add-indexer form), and **change-password** — so an operator (or a future web UI, an
  API client by design) can do everything through the documented API at `/api/docs`,
  no web UI required. The JSON search runs the **same shared pipeline** as the
  Torznab feed (identical result set), and a resolver-needing indexer's link is
  sealed behind the `/dl` proxy so a passkey never reaches the JSON. Errors carry a
  machine-readable `code`. *(`internal/web/api`)* `[shipped]`
- **A divergence ledger, not a backlog.** Every difference from Jackett or the spec
  is recorded once, next to the test that pins it, with an explicit disposition
  (`[Tracked: Phase N]` / `[Deliberate]` / `[Accepted]`) — a complete, auditable
  decision log indexed by `docs/divergences.md`. *(per-layer testdata READMEs)*
  `[shipped]`

## Licensing

- **Clean, redistributable corpus.** harbrr vendors **Jackett's GPL-2.0** definition
  corpus (matching its own GPL-2.0-or-later license); Prowlarr's `Indexers` repo
  carries no license (all rights reserved) and isn't redistributable. The one
  Prowlarr-only tracker / custom defs go through the drop-in dir. *(`docs/ideas.md`
  §14, `LICENSE`)* `[shipped]`

## Family integration (the differentiators Prowlarr structurally resists)

- **One-call indexer sync into Sonarr, Radarr, and qui.** harbrr pushes its configured
  indexers straight into the consuming apps — the Prowlarr headline feature — with a
  full add/update/remove lifecycle: idempotent re-sync (unchanged indexers are a no-op),
  orphan removal gated to a per-app **full** sync level (a safe **add/update-only** level
  never deletes), per-app enable/disable, and an **all** or **selected** indexer scope.
  Each connection gets its own **revocable** harbrr API key (minted + encrypted per
  connection) and its own harbrr feed URL, so it works across Docker/LAN where each app
  reaches harbrr at a different address. Driven entirely over HTTP at `/api/app-connections`
  (no Web UI needed). *(`internal/appsync`, `docs/plan.md` Phase 10)* `[shipped]` —
  offline-proven (stub-server golden tests + end-to-end HTTP), and the driver
  contracts were **live-validated 2026-06-18** against a real Sonarr/Radarr/qui stack:
  qui took the exact create body with a 201 + 204 delete; Sonarr/Radarr accepted the body
  and built the correct Torznab feed request (live `/indexer/schema` matched the field
  mapping). Full-stack indexer save awaits a deployed harbrr in the stack.
- **Native push to autobrr.** A path so newly-scraped releases reach autobrr's
  filters immediately instead of waiting on RSS polling — a family-only upgrade
  Prowlarr can't match. *(`docs/ideas.md` §11, `docs/plan.md` Phase 10)* `[planned]`
- **Cross-seed search backend.** harbrr as cross-seed's native multi-tracker Torznab
  source. *(`docs/ideas.md` §11.4)* `[planned]`
- **Shared tracker identity + qBittorrent.** One tracker id + one credential set
  shared with autobrr's IRC defs; grabs pushed via the family's `go-qbittorrent`
  (qui manages the client); release names via `rls` — harbrr reuses the family
  stack instead of reimplementing it. *(`docs/ideas.md` §11)* `[planned]`/`[partial]`
- **OIDC login + *arr application sync + Jackett/Prowlarr import + web UI.** On the
  roadmap; the management API + OpenAPI spec shipped in Phase 4 are the foundation.
  *(`docs/plan.md` Phase 10)* `[planned]`
