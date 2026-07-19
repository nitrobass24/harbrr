# Architecture

Read this before changing cross-module data flow, service boundaries, API routing, or the engine
pipeline shape. It is the load-bearing summary of how harbrr is built and why. Forward work and the
roadmap live in the project's GitHub issues. The target shape for the autobrr-family app spine lives
in `docs/autobrr-app-template.md`; review rules for structural refactors live in
`docs/architecture-refactor-rules.md`.

## What harbrr is

The autobrr family's native **Torznab/Newznab + on-demand-search provider** — the slot the family
fills today with Prowlarr. Family-native first, Prowlarr-compatible second.

## Why Cardigann parity, and why vendor Jackett

harbrr gives the autobrr family a native indexer search/scrape provider — the role the family
currently outsources to **Prowlarr**, an external .NET service. Rather than invent a new
tracker-definition format, harbrr speaks **Cardigann**, the declarative per-tracker adapter format
Jackett and Prowlarr already share, so the existing community corpus of 550+ tracker definitions runs
**unmodified**. The name is the interoperability contract, kept deliberately.

The project succeeds or fails on **one** thing: reimplementing the Cardigann engine closely enough to
the .NET original that those definitions behave identically. This is a *deeper* risk than "parse YAML
and pass a test suite" — it means reproducing years of accidental behavior, tracker-specific hacks,
and HTTP/session edge cases. The saving grace is the validation strategy: because the correctness
target is **Jackett specifically**, those quirks never have to be *enumerated or understood* — feeding
the same saved input to both engines and diffing reproduces them automatically. The long tail is
intractable to catalog but tractable to **match**. That is why the build order is **test-harness
first, engine second, product third**.

**Why vendor Jackett's corpus, not Prowlarr's.** Two near-identical definition corpora exist, but the
licensing differs decisively. Jackett is **GPL-2.0** and bundles its definitions in-app; harbrr is
**GPL-2.0-or-later**, so vendoring them is clean redistribution. Prowlarr *the application* is
GPL-3.0 — irrelevant here, since harbrr uses no Prowlarr code — but the `Prowlarr/Indexers`
**definitions repo carries no license** (all rights reserved), so those defs are not redistributable.
The one Prowlarr-only tracker and any user/custom defs go through the local drop-in directory instead.
Vendoring Jackett also makes the dependency a *community* one rather than a Prowlarr one. (Not legal
advice.)

## The engine is a pipeline

The Cardigann engine (`internal/indexer/cardigann`) is a compiler-style pipeline of small, decoupled,
independently-tested stages:

```
loader → mapper → dateparse → login → search → normalizer
                                             → torznab (serialize)
```

Data flows one way. A stage takes typed input and returns typed output; stages do not reach across
each other. Each stage owns its fixtures. Engine-private support stages live under
`cardigann/internal/` so they cannot be imported from outside the engine: `template` (Go
text/template evaluation), `selector` (HTML/JSON selection), `regexadapter` (RE2/regexp2 routing)
and `encode` (.NET-equivalent WebUtility URL encoding). The filter registry lives in `search`;
magnet synthesis lives in `normalizer`. This shape is deliberate — it keeps functions small (the
complexity linters enforce it) and makes the parity harness (`cardigann/parity`) tractable. The
Torznab/Newznab serializer lives in `internal/torznab`.

## Regex — RE2 by default, regexp2 on demand

.NET's regex engine backtracks; Go's `regexp` (RE2) does not. You *cannot* statically prove an
arbitrary pattern matches identically under both (equivalence is undecidable), so a "RE2 only when
provably equivalent" rule would force everything onto regexp2 and discard RE2's two real benefits:
linear-time matching and **ReDoS safety** (regexp2 backtracks and can be hung by a hostile def or
tracker response). So the routing rule is behavioral, not a per-def edit:

**RE2 is the default for its ReDoS guarantee; route to `regexp2` (`dlclark/regexp2`, .NET semantics)
only when** the definition opts in, the tracker's `language:` is non-Latin, the pattern fails RE2
compilation, or the pattern uses .NET-only constructs (backreferences, lookarounds, atomic/conditional
groups, `(?<name>)`).

The non-Latin trigger matters because RE2 silently differs from .NET on `\d`/`\w`/`\s` over non-Latin
text (~190 defs are non-Latin-script). The differential suite runs **both engines on the same
fixtures** and is the gate that catches any silent RE2 ≠ .NET case; when found, that pattern's def is
added to the regexp2 routing. This is engine behavior — never silence a parity diff by editing a def.

## Invariants (do not violate)

1. **Definitions are consumed byte-for-byte.** All behavioral differences live in the engine, never in
   the def files. `vendor/` is read-only (a hook enforces it); overrides go in `dropin/`.
2. **Correctness = parity with Jackett's engine on the same input**, established offline. Per-def-vs-live
   correctness is the corpus's responsibility, not ours. Every deliberate or accepted difference from
   Jackett/the spec is recorded once, next to its fixtures, with a `[Tracked]`/`[Deliberate]`/`[Accepted]`
   disposition; `divergences.md` is the index.
3. **Two HTTP contracts, kept separate.** Torznab/Newznab (XML, `internal/torznab`) is the *arr-facing
   contract; the OpenAPI surface (`internal/web/swagger`) is harbrr's own management API. They evolve
   independently.
4. **Regex routing is engine behavior, not a per-def edit.** RE2 by default (ReDoS-safe); regexp2 only
   on the defined triggers (opt-in / non-Latin / compile-fail / .NET-only constructs).
5. **Storage is behind `dbinterface`.** SQLite only for now; Postgres is deliberately **demand-gated**
   — built only when a real multi-instance user needs it. Keep the interface **dialect-portable**: all
   repository SQL routes through the interface and its `Rebind` (`?`→`$N`) seam, no SQLite-specific SQL
   or driver types leak to callers, and schema changes ship as SQLite migrations a Postgres backend can
   mirror. Keeping the seam clean is required now; implementing Postgres is not.
6. **Secrets never leak.** Redact in all logs/traces; encrypt at rest; never commit.

## Repository structure

The layout mirrors the pipeline: one package per engine stage, and product surfaces kept in their own
top-level packages so the engine stays decoupled from the daemon.

```
cmd/harbrr/              # cobra entrypoint: serve, smoke, rotate-key, announce, appsync-source, version
internal/
  indexer/
    cardigann/           # the engine pipeline (below) — the core of the project
      loader/ mapper/ dateparse/ login/ search/ normalizer/   # pipeline stages
      internal/          # engine-private support: template/ selector/ regexadapter/ encode/
      parity/            # differential-vs-Jackett harness — the correctness gate
    core/                # the indexer serving contract: Indexer/Provider/IndexerInfo/CacheInfo + the
                         #   shared SearchReleases read pipeline — the indexer as the rest of harbrr
                         #   consumes it, independent of transport (see ADR-0002)
    definitions/         # //go:embed vendored Jackett snapshot: vendor/ (read-only) + dropin/ (user overrides)
    native/              # bespoke Go drivers for trackers Cardigann YAML can't express
                         #   (avistaz, hdbits, gazelle, iptorrents, myanonamouse, filelist, …) + newznab base
  torznab/               # Torznab/Newznab serializer: caps, category tree, result/error XML (the *arr contract)
  web/
    api/                 # chi router + management-API handlers (indexers, appsync, announce, auth, stats, …)
    swagger/             # hand-authored openapi.yaml (//go:embed) + Swagger UI + drift test
    torznabhttp/         # HTTP/XML serving over core.Indexer: routing, request parsing, the 304
                         #   revalidator, download-token, DL-proxy URL helpers
  appsync/               # sync indexers into the *arr apps (sonarr/radarr/lidarr/readarr/whisparr) + qui
  announce/              # push newly-scraped releases to autobrr / cross-seed v6 / qui
  http/                  # HTTP seam: log/trace redaction, decode-error and transport-error shaping
  secrets/               # AES-256-GCM at rest (aead), argon2id passwords, bearer-token hashing, key canary/rotation
  auth/                  # web-UI / management-API authentication service
  database/              # SQLite repos + migrations …
    dbinterface/         #   … behind one interface + dialect-aware query rebinding (Postgres deferred)
  config/                # viper config load + typed settings (secret-vs-plaintext field types)
  notify/                # notification providers (Discord, webhook)
  domain/                # shared models
  server/                # startup/shutdown wiring
  logger/ version/       # zerolog setup; build/version + the pinned Jackett definition ref
  smoke/                 # live differential smoke harness (build-tagged, manual-only)
```

## Boundaries with the family

- **autobrr** consumes harbrr's Torznab/Newznab feeds as a drop-in for Prowlarr. Beyond the feed,
  harbrr can **push** newly-scraped releases directly (`internal/announce`) instead of waiting on RSS
  polling — the family-only upgrade. harbrr does **not** parse IRC announce; that stays autobrr's job.
- ***arr apps** (Sonarr/Radarr/Lidarr/Readarr/Whisparr) **and qui** — harbrr **syncs its indexers
  into them** (`internal/appsync`) the way Prowlarr does, and serves their Torznab searches. This is
  the Prowlarr-compatibility surface.
- **cross-seed** — harbrr is a Torznab search *source* for cross-seed (`internal/announce` cross-seed
  v6 push; the served feed as a backend).
- **qui / download clients** — harbrr may **hand a release to** a download client (add a
  `.torrent`/`.nzb`/magnet — a one-shot action, the same as Prowlarr/Sonarr/Radarr); it does **not
  manage** the client (categories, ratios, seeding, the UI), which stays qui's job. "Hand off" is in
  scope; "manage" is not.
- **mkbrr / upbrr** own torrent creation / upload; harbrr only shares the tracker-identity layer.

harbrr does not download torrents itself — `internal/download` hands a resolved release to a
configured client's driver (qBittorrent first; the rest are tracked in autobrr/harbrr#8), which
starts the download and takes over management from there; interactive grab (#7) is the remaining
piece of the direct grab-to-client path.

## Search-results cache (design record)

The one differentiator Prowlarr/Jackett lack: because harbrr is the Torznab *server*, a cache hit
spares the **tracker's** infrastructure, not just harbrr's. Shipped in #60; user docs in
`website/docs/features/search-results-cache.md`. The design decisions worth keeping:

- **Seam** — cache-aside around `idx.Search` in the registry adapter, downstream of login/engine and
  **upstream** of dedupe/category-filter/pagination/`/dl`-rewriting, so one entry serves every client
  (the cached value is `[]*normalizer.Release` **before** `/dl` rewriting).
- **Key** — SHA-256 over a schema-versioned canonical payload: `version | instance_id | search_mode |
  keywords | categories(sorted) | ids | season/ep/year | …`. Categories **are** in the key (they change
  the tracker request). `limit`/`offset` are excluded for a **non-paging** instance (applied post-cache,
  so different pagination reuses one entry); a **paging-capable** instance — one whose driver forwards
  `offset`/`limit` upstream (e.g. Newznab/usenet) — folds them into the key so each upstream page gets
  its own entry. Byte-identical queries from a 1080p and a 4k \*arr instance collapse to one entry — the
  multi-instance fix, TTL-independent.
- **Storage** — SQLite as source of truth (survives restart, so no thundering re-poll on boot);
  FK-cascade on instance delete; periodic cleanup.
- **Singleflight** — concurrent misses for one key collapse to a single tracker request.
- **Stale-while-revalidate** — past a refresh-ahead threshold, serve the stale value immediately + kick
  one detached background refresh, so a tracker sees **≤1 request per TTL** regardless of client count.
- **TTL tiers** (all per-indexer + globally tunable at runtime): RSS/empty-query **5 min**, keyword/ID
  **30 min**, thin/empty result **2 min** (adaptive, shortens only — the staggered-release antidote).
  `nocache=1` bypasses. Only successes are cached (incl. legitimately-empty sets); never errors.
- **Secrets at rest** — cached `Link`/`Magnet` embed passkeys, so `results_json` is a secret at the same
  trust level as the session cookies already in the `0600` DB. Decision: rely on the `0600` DB +
  never-log posture, not per-row `internal/secrets` AES (the per-read cost isn't worth it here).

## Scheduled RSS warm-cache poller (design record)

harbrr's first scheduler beyond the reap-style maintenance goroutines (#252; ADR
[0005](adr/0005-rss-warm-scheduler.md)). Another differentiator Prowlarr/Jackett structurally
cannot offer: because harbrr sits between the tracker and every RSS-polling client, it can
proactively keep the shared cache entry warm on the tracker's behalf, instead of only ever
reacting to whichever client happens to poll first after a TTL expiry.

- **Mechanism** — per opted-in indexer, drives the exact served path — `adapter.Search
  (core.WithCacheBypass(ctx), search.Query{})` — so the warm write lands under the identical
  cache key #257's downstream RSS polls read, with zero duplicated cache-key/budget/breaker logic.
- **Opt-in, default-off** — `rss_warm_interval`, a reserved per-instance Go-duration setting
  (like `rate_interval`/`cache_ttl`), clamped to [10m, 120m]; absent/invalid disables it.
  Backend-only, no API/web surface.
- **Budget and breaker fall out for free** — `errBudgetExhausted` / `errCircuitOpen` from the
  served path are logged skips, never a retry loop; the breaker never trips on a budget refusal.
- **Stagger** — a stable per-instance phase offset spreads first-due times across the first
  interval and keeps them spread at every later boundary, avoiding an N-concurrent-fetch herd
  through a shared proxy/FlareSolverr.
- **Loop** — reuses `app/lifecycle.go`'s `reap` skeleton; no new ticker/shutdown code.
