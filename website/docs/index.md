# harbrr

harbrr is a single-binary Torznab/Newznab search provider for the autobrr family — a
self-hosted, Cardigann-compatible alternative to Prowlarr/Jackett. You point Sonarr,
Radarr, and friends at harbrr, and it searches your trackers and hands back results — while
a shared cache spares your trackers from the duplicate requests every app would otherwise
make on its own.

For the alpha, harbrr is operated entirely over its HTTP API; the interactive
**[Swagger UI at `/api/docs`](api.md)** is the interface.

## Start here

- **[Getting started](getting-started.md)** — run harbrr, create the admin, mint a key, add
  an indexer, and point Sonarr/Radarr at the feed.
- **[Adding an indexer](guides/add-indexer.md)** — discover a definition, configure it, and
  test it.
- **[App Sync](guides/app-sync.md)** — push indexer config straight into Sonarr/Radarr/Lidarr/Readarr/Whisparr/qui.
- **[Configuration](configuration.md)** — every config key and environment variable.
- **[The API & Swagger UI](api.md)** — the complete HTTP reference.

## Features

- **[Search-results cache](features/search-results-cache.md)** — how harbrr spares your
  trackers from repeated, identical searches, and the knobs you can use to tune it.
- **[Failing-tracker circuit breaker](features/circuit-breaker.md)** — how harbrr backs off
  a tracker that's erroring instead of hammering it.
- **[Usenet (Newznab) indexers](features/usenet-newznab.md)** — usenet support alongside
  torrents.
- **[Cross-seed & freeleech](features/cross-seed-freeleech.md)** — one tracker serves both
  your \*arrs and cross-seed, with a per-indexer freeleech toggle and announce push.
- **[Pagination](features/pagination.md)** — honest counts and stable, non-duplicating pages
  (the bug Prowlarr/Jackett have, that harbrr doesn't).

For the internal design notes and build plan, see the `docs/` folder in the repository.
