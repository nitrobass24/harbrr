# Changelog

All notable changes to **harbrr** are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and harbrr aims to follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) once it leaves alpha.

For each release, the section between its heading and the `release-header-end`
marker is published verbatim as the GitHub Release's highlights; goreleaser appends
the grouped feature/fix commit list beneath it.

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

[0.1.0-alpha]: https://github.com/autobrr/harbrr/releases/tag/v0.1.0-alpha
