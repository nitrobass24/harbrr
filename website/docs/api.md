# The API & Swagger UI

harbrr is driven entirely over HTTP. For the alpha, the API **is** the interface — there is
no separate web UI yet (that's a later phase). Everything an operator does — add indexers,
search, grab, sync into your apps — happens through these endpoints.

## Interactive docs

- **Swagger UI** — `http://<host>:7474/api/docs`. A live, try-it-out reference for every
  endpoint. Log in once (it stores your session) and you can exercise the whole API from the
  browser.
- **Raw spec** — `http://<host>:7474/api/openapi.yaml`. The machine-readable OpenAPI
  document, if you'd rather generate a client or import it into another tool.

(If you set `server.base_url`, both live under that subpath.)

## Authentication

Two ways to authenticate, depending on the endpoint:

- **Session** — `POST /api/auth/login` establishes a cookie session (what the Swagger UI
  uses). Cookie-auth surfaces are CSRF-protected.
- **API key** — pass `X-API-Key: <key>` (or the `apikey` query param on the Torznab feed).
  Mint keys with `POST /api/apikeys` (shown once) and revoke with `DELETE /api/apikeys/{id}`.

In `auth.mode: disabled`, harbrr trusts an authenticating reverse proxy and serves a
synthetic admin to allowlisted IPs instead — see [Configuration](configuration.md#auth).

## What's there

The spec is organized by tag:

| Area                  | What it covers                                                        |
| --------------------- | -------------------------------------------------------------------- |
| **Authentication**    | first-run setup, login/logout, change password, current identity     |
| **API Keys**          | mint / list / revoke Torznab keys                                    |
| **Indexer Definitions** | list definitions, read a definition's settings schema + capabilities |
| **Indexers**          | add / configure / enable / disable / delete, **test**, status, JSON search, capabilities, cross-seed snippet |
| **App Connections**   | the Sonarr/Radarr/Lidarr/Readarr/Whisparr/qui [App Sync](guides/app-sync.md) lifecycle |
| **Announce Connections** | push new releases to qui / cross-seed v6 — see [Cross-seed & freeleech](features/cross-seed-freeleech.md) |
| **Cache**             | stats, flush, and runtime config (`GET`/`PUT /api/cache/config`)     |
| **System**            | `/healthz` liveness probe                                            |

The JSON search endpoint returns a paged envelope (`results` / `total` / `hasMore` /
`limit` / `offset`) — see [Pagination](features/pagination.md).

The **Torznab/Newznab feed** itself lives at
`/api/indexers/{slug}/results/torznab` (with `/dl` for proxied downloads) — that's the
URL your apps consume, separate from the JSON management API above.

!!! note "OIDC is stubbed"
    `/api/auth/oidc/*` returns `501` for now — OIDC is deferred to a later phase. Use session
    or API-key auth.

## Where to start

New here? Follow **[Getting started](getting-started.md)** — it runs through setup, minting a
key, adding an indexer, and pointing Sonarr/Radarr at the feed, all against these endpoints.
