# The API & Swagger UI

harbrr is driven entirely over HTTP. The web UI at `http://<host>:7478/` is a client of these
same endpoints — everything it does (add indexers, search, grab, sync into your apps) is
scriptable here, so you can automate anything you can click.

## Interactive docs

- **Swagger UI** — `http://<host>:7478/api/docs`. A live, try-it-out reference for every
  endpoint. Log in once (it stores your session) and you can exercise the whole API from the
  browser.
- **Raw spec** — `http://<host>:7478/api/openapi.yaml`. The machine-readable OpenAPI
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
| **Apps**              | an external service's identity + credential stored **once** and shared by the app-sync, announce, and download surfaces |
| **Announce**          | push new releases to qui / cross-seed v6 — see [Cross-seed & freeleech](features/cross-seed-freeleech.md) |
| **Download Clients**  | add / test / enable / disable a client, and **grab** a result into it (`POST /api/download-clients/{id}/grab`) |
| **Notifications**     | Discord / webhook targets for indexer failure, recovery, and VIP expiry |
| **Proxies**           | global, reusable proxy resources an indexer references by id         |
| **Solvers**           | global, reusable FlareSolverr resources an indexer references by id  |
| **Sync profiles**     | named, reusable app-sync overrides a connection references by id     |
| **Backup**            | export config + database as a passphrase-encrypted bundle (`/api/export`) and restore it (`/api/import`) |
| **Cache**             | stats, flush, and runtime config (`GET`/`PUT /api/cache/config`)     |
| **System**            | `/healthz` liveness, server info, and the runtime-config knobs — log level, rate-limit default, adult categories, expiry thresholds, stats retention |

The JSON search endpoint returns a paged envelope (`results` / `total` / `hasMore` /
`limit` / `offset`) — see [Pagination](features/pagination.md).

The **Torznab/Newznab feed** itself lives at
`/api/indexers/{slug}/results/torznab` (with `/dl` for proxied downloads) — that's the
URL your apps consume, separate from the JSON management API above. The same feed also
answers at `/results/torznab/api`, `/results/torznab/full`, and `/results/torznab/full/api`,
for apps that expect one of those spellings.

`{slug}` isn't limited to a single indexer. Three aggregate selectors sit in the same
position: `all` (every enabled indexer), `profile:<name>` (a [sync profile](guides/app-sync.md)'s
members), and `status:healthy` (`all` minus the indexers harbrr currently believes are broken)
— so one Sonarr/Radarr entry can cover your whole set.

:::note[OIDC / SSO]

OIDC login is supported: `GET /api/auth/oidc/config` reports whether it's on and where to
start, and `/api/auth/oidc/callback` completes the flow. Configure it under `[auth.oidc]` —
see [Configuration](configuration.md).

:::

## Where to start

New here? Follow **[Getting started](getting-started.md)** — it runs through setup, minting a
key, adding an indexer, and pointing Sonarr/Radarr at the feed, all against these endpoints.
