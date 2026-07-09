# Harbrr

> The tracker and indexer fabric for the autobrr ecosystem.

Harbrr is a single-binary, Cardigann-compatible **Torznab/Newznab** provider — the
centralized intelligence layer between your trackers and your automation. Configure your
trackers once, connect everything, and let harbrr aggregate feeds, deduplicate searches, and
be a better private-tracker citizen.

Built for **autobrr, qui, and cross-seed** from day one, while staying fully compatible with
Sonarr, Radarr, Lidarr, Readarr, Mylar, Whisparr, and any Torznab client.

> [!NOTE]
> **Alpha — operated via the API.** The Cardigann engine is at parity with Jackett/Prowlarr
> (proven offline and live), native drivers cover the trackers Cardigann can't express, and
> harbrr syncs indexers into Sonarr/Radarr/qui. harbrr already works as a **Swagger-only
> Prowlarr replacement**; a web UI is the next phase. Until then, the interactive **Swagger
> UI at `/api/docs`** is the interface.

---

## Quick start

harbrr listens on port **7478** and stores its SQLite database + encryption keyfile in a
single data directory. Both quick starts land you at the Swagger UI — from there, follow
**[Getting started](website/docs/getting-started.md)** to create the admin, mint a Torznab
key, add an indexer, and point your apps at the feed.

### Docker

```yaml
# docker-compose.yml — a ready-to-edit docker-compose.example.yml ships in the repo
services:
  harbrr:
    image: ghcr.io/autobrr/harbrr:latest
    container_name: harbrr
    restart: unless-stopped
    ports:
      - "7478:7478"
    volumes:
      - harbrr-config:/config      # SQLite db + encryption keyfile — BACK THIS UP
    environment:
      - TZ=Etc/UTC                 # match your stack so localized tracker dates parse

volumes:
  harbrr-config:
```

```bash
docker compose up -d
# open http://<host>:7478/api/docs
```

The image runs non-root, exposes port 7478, ships a `/healthz` check, and already invokes
`harbrr serve --host 0.0.0.0 --data-dir /config`.

> [!NOTE]
> No stable image tag is published yet — `main` pushes don't publish images. Tag a release
> (`git tag v0.1.0-alpha && git push origin v0.1.0-alpha`) to publish
> `ghcr.io/autobrr/harbrr:0.1.0-alpha`, use a same-repo `pr-<n>` image, or build from source.

### Linux (binary / source)

Grab a prebuilt binary from [Releases](https://github.com/autobrr/harbrr/releases) once a
`v*` tag is cut, or build from a checkout (Go 1.26+):

```bash
git clone https://github.com/autobrr/harbrr && cd harbrr
make build                                   # -> bin/harbrr
./bin/harbrr serve --data-dir ./data         # open http://localhost:7478/api/docs
```

> [!NOTE]
> `make build` embeds whatever is in `web/dist`; a fresh checkout has none, so the binary
> serves "frontend not built" until you run `make web-build` (needs Node + pnpm). Building
> the image locally with `docker build` likewise needs the SPA built first — otherwise the
> build fails fast rather than silently shipping a UI-less image. Published images already
> bundle it.

---

## The API & Swagger UI

Everything harbrr does is an HTTP endpoint, and the **Swagger UI at `/api/docs`** is the full
interactive interface for the alpha — create the admin, mint API keys, add/test indexers,
search, grab, and configure app-sync, all from the browser. See
**[The API & Swagger UI](website/docs/api.md)** for the reference.

<!-- TODO(#12): embed Swagger UI screenshots once a release image is running. -->

---

## Why Harbrr?

Private trackers are a **shared resource**, but most automation stacks hammer them with
duplicate RSS polls and repeated searches from disconnected apps. Instead of every app
talking to every tracker, harbrr sits in the middle and aggregates, caches, and optimizes
that traffic.

- **Centralized tracker management** — one source of truth for auth, capabilities, categories,
  and search behavior across your whole stack. No duplicating tracker setup per app.
- **Shared RSS + search-results cache** — many consumers, one upstream request; far fewer
  tracker queries, lower latency, better tracker citizenship.
- **Cross-seed aware** — smarter release matching, freeleech-aware matching, and optional
  freeleech-bypass logic.
- **Cardigann compatibility** — reuses the mature Jackett/Prowlarr definition ecosystem with a
  modernized execution engine.
- **Full Torznab/Newznab** — works with autobrr, qui, cross-seed, and the entire \*arr family.
- **Modern Go** — single static binary, Docker-first, low footprint, fast startup.

More detail on each of these lives in the
**[feature docs](website/docs/features/)**.

---

## Security

harbrr treats tracker credentials as sensitive by default:

- Credentials (passkeys, cookies, API keys) are **encrypted at rest** (AES-256-GCM); the key
  is auto-generated on first run.
- The admin password and API keys are **hashed**, never stored recoverably.
- Secrets are **redacted** from logs, errors, and traces; a passkey never appears in the
  served Torznab feed — download links resolve server-side.

See [Configuration](website/docs/configuration.md) for key management and keyfile backup, and
**[SECURITY.md](SECURITY.md)** to report a vulnerability privately.

---

## Roadmap & status

The executable, up-to-date roadmap lives in **[`docs/plan.md`](docs/plan.md)** — built by risk
retirement (engine parity first, product surface after). In short: the engine and parity gate
are done, the daemon (SQLite, encrypted secrets, management API, Docker) is done, native
drivers and Newznab are in, and app-sync into \*arr/qui works. The **web UI** is the next
phase.

---

## Contributing

Contributions, testing, feedback, and ideas are welcome. Start with
**[CONTRIBUTING.md](CONTRIBUTING.md)** — how to build and test, commit conventions, and the
non-negotiable rules — and please review the **[Code of Conduct](CODE_OF_CONDUCT.md)**. Found a
security issue? See **[SECURITY.md](SECURITY.md)** and report it privately, not as a public
issue.

Especially helpful: Cardigann definitions, tracker testing, Torznab interoperability, and
autobrr/qui/cross-seed integration.

---

## License

harbrr is free software: you can redistribute it and/or modify it under the terms of the GNU
General Public License as published by the Free Software Foundation, **either version 2 of the
License, or (at your option) any later version** (GPL-2.0-or-later). The full text is in
[LICENSE](LICENSE).

---

## Keywords

autobrr, qui, cross-seed, Torznab, Newznab, Cardigann, Prowlarr alternative, Jackett
alternative, private trackers, RSS caching, search deduplication, indexer manager, indexer
proxy, Sonarr, Radarr, Lidarr, Readarr, Mylar, Whisparr, Go, Docker
