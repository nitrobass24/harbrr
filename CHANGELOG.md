# Changelog

All notable changes to **harbrr** are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and harbrr aims to follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) once it leaves alpha.

For each release, the section between its heading and the `release-header-end`
marker is published verbatim as the GitHub Release's highlights; goreleaser appends
the grouped feature/fix commit list beneath it.

## [Unreleased]

## [0.2.0-alpha] - 2026-09-09

Two months of hardening since the first alpha: a download-client platform, a tri-state
health model with circuit breakers and request budgets, aggregate feeds, app identity
across every surface, OIDC login, encrypted backups, and five new native drivers. Still
an **alpha** — back up your `/config` directory (SQLite DB **and** the encryption
keyfile) before upgrading.

### ⚠️ Breaking (management API)

- Indexer health is now tri-state. `unhealthy` is renamed `failing`, a new `unknown`
  state appears when nothing recent is known, and the fleet-status tally replaces
  `unhealthy` with `failing` + `unknown` fields. Pre-1.0 change, no compatibility shim.
- The MoreThanTV torznab preset is retired (site shutdown).

### ⬇️ Download clients

- A download-client platform with drivers for **qBittorrent**, **Transmission**, **Deluge**,
  **rTorrent**, **qui**, **Flood**, **Synology Download Station**, **SABnzbd**, **NZBGet**
  and a **blackhole** watch folder (torrent + usenet).
- Send any search result straight to a configured client from the web UI; proxied
  downloads are named after the release title.

### 🩺 Indexer health, budgets & resilience

- Derived health is tri-state (healthy / failing / unknown) and sticky rather than
  time-expiring; a hard-down tracker can never derive healthy. Transport failures and
  gateway statuses (502/504/522) degrade status as their own health kind.
- Consecutive-failure circuit breaker with per-kind escalation backoff and a recovery
  notification; every outbound search funnels through breaker, circuit, then budget.
- Cap-aware per-indexer request budgets with reactive quota learning, Newznab budgets
  seeded from `t=user`, a user-configurable request rate (global default + per-indexer
  override), and a per-indexer budget usage meter.
- Auto-failover across a definition's known base URLs, plus a base-URL picker in the UI.
- Per-indexer VIP/membership expiry tracking with threshold notifications.
- Fleet-wide status roll-up, status filter and operator-visible health detail.
- Scheduled RSS warm-cache poller so feeds are served hot.

### 🔎 Search & Torznab

- Aggregate feeds: an `all` feed with partial fan-out and origin-bound grabs,
  profile-scoped aggregate feeds, and a health-filtered `status:healthy` aggregate.
- Tracker-native internal/scene tags and release-name / filename evidence in results;
  the newznab `<limits>` element is modelled in caps.
- Web search runs on the server-merged aggregate window, groups identical releases
  across indexers, and filters instantly by substring or regex.
- Engine: degenerate-query gating with matching settings, and an opt-in
  punctuation-tolerant `andmatch` row filter.

### 🧩 Apps, sync & announce

- App identity: configure an app once and use it on every surface — reuse-first create
  dialogs and "Use as…" actions on App registry rows (ADR 0004).
- Sync profiles are indexer routing sets; per-indexer advanced options; split host/port
  fields with per-kind default ports.
- Announce targets can be edited and tested; a one-click qui target is seeded from the
  app connection; each target is bounded by its own push timeout.

### 🧭 Native drivers & definitions

- New native drivers: **AlphaRatio**, **Nebulance**, **XSpeeds**, **RetroFlix** and
  **BrokenStones** (Gazelle); a torznab driver family for preset-based sites.
- Six vendored Jackett definition refreshes, tracking upstream adds and removals.

### 🐇 Web UI

- Responsive shell with mobile card layouts for indexers and search results, and an
  installable PWA (manifest, icons, service worker).
- Cache page with entry ages, breaker-until countdowns and inline knob help; a global
  setting to hide adult categories; a Freeleech badge on the indexers page.
- Grab success rate and per-category indexer tallies on the dashboard.
- Backup export/import section on Settings; error/warn toasts are relayed into the
  daemon log; a Playwright e2e suite gates CI.

### 🔒 Security & operations

- OIDC / SSO login.
- Passphrase-encrypted config + DB export and import.
- `external_url` and reverse-proxy hardening; the proxy URL is split into structured
  fields.
- The shared app-facing HTTP client refuses authenticated cross-host redirects, so an
  API key can never follow an open redirect off the configured host.
- Seedbox installers ported from qui.

### 🛠️ Notable fixes

- Cache correctness: trustworthy hit-ratio stats, TTL clamping at read time, expiry when
  definition content changes, and collapsed RSS cache-key fragmentation.
- MyAnonamouse downloads use the cookie-authed form and identify the client; plain
  Gazelle sites download via `torrents.php`.
- Checkbox-shaped settings parse `"false"` correctly; unitless integer sizes parse
  exactly; single-digit-day RFC1123 dates are accepted.

<!-- release-header-end -->

## [0.1.0-alpha] - 2026-07-12

The first public cut of harbrr — the tracker and indexer fabric for the autobrr
ecosystem. harbrr is a single-binary, Cardigann-compatible **Torznab/Newznab**
provider that sits between your trackers and your automation: configure trackers
once, connect everything, and let harbrr aggregate feeds, deduplicate searches, and
be a better private-tracker citizen.

This is an **alpha**. The engine and daemon are proven, but expect rough edges and
breaking changes before `v1.0`. Back up your `/config` directory (the SQLite DB **and**
the encryption keyfile).

### 🐇 Web UI

A full embedded management interface ships in this release — no separate frontend to
run. Everything is served from the single binary:

- **Dashboard** — indexer health at a glance, tracker-hits-saved / cache hit ratio,
  app connections, and circuit-breaker status.
- **Indexers** — add, configure, test, enable/disable and delete indexers; protocol
  (Torrent/Usenet) and privacy at a glance; searchable filtering.
- **Search** — manual multi-indexer search with category, IMDb/TMDB/TVDB and
  season/episode scoping.
- **Applications** — connect Sonarr/Radarr/qui and sync indexers automatically.
- **Cache**, **Proxies & Solvers**, and **Settings** (API keys, notifications,
  logging, account) round it out — with light and dark themes.

### 🔌 Cardigann engine at parity

- A compiler-style engine (loader → mapper → template → filter → selector →
  dateparse → regexadapter → login → search → normalizer) that reproduces Jackett's
  Cardigann behavior on the same inputs, gated by an offline differential parity suite.
- Reuses the vendored Jackett/Prowlarr definition ecosystem byte-for-byte, with a
  `dropin/` layer for local overrides.
- **Native drivers** for the trackers Cardigann can't express (AvistaZ family,
  NZBIndex, and more).

### 🔁 Feeds, sync & cross-seed

- **Full Torznab + Newznab** serving — works with autobrr, qui, cross-seed and the
  whole \*arr family (Sonarr, Radarr, Lidarr, Readarr, Mylar, Whisparr).
- **App-sync** pushes your indexers into Sonarr/Radarr/qui, with configurable **sync
  profiles** (category narrowing, minimum seeders, per-capability search toggles) and
  freeleech **honor/bypass** per app.
- **Cross-seed aware** — announce targets for cross-seed tools plus per-indexer config
  snippets.
- Shared RSS + search-results cache, health tracking, and circuit breakers so many
  consumers make one upstream request.

### 🔒 Security

- Tracker credentials (passkeys, cookies, API keys) **encrypted at rest**
  (AES-256-GCM); the key is auto-generated on first run.
- Admin password and API keys are **hashed**, never stored recoverably.
- Secrets are **redacted** from logs, errors and traces; a passkey never appears in
  the served feed — download links resolve server-side.
- Session + API-key auth, session-bound CSRF on cookie surfaces, and trusted-proxy /
  IP-allowlist modes.
- **Encrypted backup** — passphrase-protected export/import of config + database.

### 📦 Packaging

- Single static, pure-Go binary (CGO-free; embedded SQLite) — fast startup, low
  footprint.
- Multi-arch Docker images (`linux/amd64`, `linux/arm64`) published to GHCR; runs
  non-root on port **7478** with a `/healthz` check.
- Prebuilt release archives for **Linux, macOS, Windows and FreeBSD** across
  amd64 / arm / arm64.

<!-- release-header-end -->

### Known limitations

- **Alpha quality** — interfaces and schemas may change before `v1.0`.
- **SQLite only.** Postgres is intentionally deferred; the database is behind a clean
  interface so it can be added later.
- **Send-to-download-client** is not implemented yet (harbrr resolves download links;
  handing releases to a client is planned — autobrr/harbrr#8).
- No stable `latest` image guarantees during alpha; pin a tag.

### Platforms

| OS | amd64 | arm | arm64 |
| --- | :---: | :---: | :---: |
| Linux | ✅ | ✅ | ✅ |
| macOS | ✅ | — | ✅ |
| Windows | ✅ | — | — |
| FreeBSD | ✅ | — | — |

Docker images: `linux/amd64`, `linux/arm64`.

[0.2.0-alpha]: https://github.com/autobrr/harbrr/releases/tag/v0.2.0-alpha
[0.1.0-alpha]: https://github.com/autobrr/harbrr/releases/tag/v0.1.0-alpha
