# Adding an indexer

The **Indexers** page in the web UI is the interactive way to add a tracker: it renders the
add form from the definition's own settings schema and tests the result in place. This guide
covers the same flow over the API, for scripting it — discover a definition's settings,
configure an instance, and test it before you rely on it. Every call is also clickable in the
**Swagger UI at `/api/docs`**, which fills in the auth header for you.

All four endpoints below require auth: an `X-API-Key` header (shown in the examples) or the
session cookie the web UI uses. Without one they answer `401`.

harbrr ships the full Cardigann/Jackett definition corpus plus native drivers, so most
trackers are already supported — you just supply your credentials.

---

## 1. Find a definition

List the available tracker definitions:

```bash
curl http://<host>:7478/api/definitions \
  -H 'X-API-Key: <harbrr-api-key>'
```

Each entry has an `id` (for example `torrentleech`, `filelist`). That `id` is what you
configure against.

## 2. Read its settings schema

A definition declares which settings it needs (username/password, passkey, cookie, …) and
which of them are secret:

```bash
curl http://<host>:7478/api/definitions/torrentleech \
  -H 'X-API-Key: <harbrr-api-key>'
```

The response gives the definition's **settings fields** (with a `secret` flag on credential
fields) and its **capabilities** (search modes and categories) — everything a client needs to
render an add form. Use the field names here as the keys in the next step.

## 3. Add the indexer

Create a configured instance. Pass the definition `id` and a `settings` map of field name →
value. Secret values are stored **encrypted at rest**.

```bash
curl -X POST http://<host>:7478/api/indexers \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: <harbrr-api-key>' \
  -d '{
        "definitionId": "torrentleech",
        "settings": { "username": "you", "password": "your-password" }
      }'
```

Fields you can send:

| Field                     | Required | Notes                                                                    |
| ------------------------- | -------- | ------------------------------------------------------------------------ |
| `definitionId`            | yes      | the definition `id` from step 1                                          |
| `slug`                    | no       | URL identifier; defaults to `definitionId`                               |
| `name`                    | no       | display name                                                             |
| `baseUrl`                 | no       | override the tracker base URL (multi-domain trackers)                    |
| `settings`                | no       | definition field → value; secrets stored encrypted                       |
| `proxyId`                 | no       | reference a proxy resource (see below); omit for none                    |
| `solverId`                | no       | reference a solver resource (see below); omit for none                   |
| `priority`                | no       | indexer priority pushed to apps, 1–50 (1 = highest); defaults to `25`    |
| `minSeeders`              | no       | minimum-seeders floor pushed to apps; `0` = unset, not pushed            |
| `syncCategories`          | no       | Newznab category ids narrowing what this indexer pushes, within the app's own content type; empty = no narrowing |
| `enableRss`               | no       | let the app use this indexer for RSS (default `true`)                    |
| `enableAutomaticSearch`   | no       | let the app use it for automatic searches (default `true`)               |
| `enableInteractiveSearch` | no       | let the app use it for interactive searches (default `true`)             |
| `expiresAt`               | no       | VIP/membership expiry date, `YYYY-MM-DD`; omitted = untracked            |
| `expiryKind`              | no       | `perk` (VIP lapses) \| `account` (access ends)                           |
| `expiryLifetime`          | no       | never expires — clears `expiresAt` and never notifies                    |

`priority`, `minSeeders`, `syncCategories`, and the three `enable*` flags are per-indexer
**sync** behavior — what [App Sync](app-sync.md) registers for this indexer in
Sonarr/Radarr/qui. The `enable*` flags are ANDed with the indexer's own enabled state, so
disabling an indexer forces all three off. The `expiry*` fields are unrelated to sync: they
track when a VIP perk or the account itself lapses, so harbrr can warn you first.

### Proxies and solvers

Proxies and anti-bot solvers are **shared resources**: create one, reference it from as many
indexers as you like. Manage them at `/api/proxies` and `/api/solvers` (or on the **Proxies &
Solvers** page in the web UI), then set `proxyId` / `solverId` on the indexer.

The `settings` map also accepts **reserved engine keys** for the per-indexer knobs:

- `timeout` — per-indexer request timeout (a Go duration, e.g. `30s`).
- `rate_interval` — minimum spacing between requests to this tracker (e.g. `5s`); it overrides
  the global rate-limit default, but never goes below the definition's own `requestDelay`.
- `solver_type=manual_cookie` with an encrypted `cookie` setting — manual-cookie / 2FA login.
  This one stays inline by design: it's per-tracker, not a shared resource.

:::note[Legacy inline proxy/solver settings]

The older inline keys — `proxy_type` / `proxy_url` and `solver_type=flaresolverr` /
`flaresolverr_url` — still work. Existing indexers using them are folded into proxy and solver
resources automatically at startup, so prefer `proxyId` / `solverId` for anything new.

:::

A successful add returns `201` with the created instance (its `slug` and redacted settings).

## 4. Test it

Validate the credentials and connectivity against the live tracker — this uses a fresh,
uncached engine so it never disturbs a running session:

```bash
curl -X POST http://<host>:7478/api/indexers/torrentleech/test \
  -H 'X-API-Key: <harbrr-api-key>'
```

`200 {"ok":true}` means the login/probe succeeded. `{"ok":false,"error":"..."}` returns a
**secret-scrubbed** reason you can act on.

---

## Updating and removing

- **Update** — `PATCH /api/indexers/{slug}`. Settings are **merged**; send the sentinel
  `<redacted>` for a secret field to keep the stored value unchanged.
- **Enable / disable** — `POST /api/indexers/{slug}/enable` · `.../disable`.
- **Status** — `GET /api/indexers/{slug}/status`. Returns a tri-state `healthy` (something
  succeeded and nothing has failed since) · `failing` (the newest failure still stands) ·
  `unknown` (nothing observed yet), plus recent health events — `auth_failure`,
  `rate_limited`, `parse_error`, `anti_bot`, `transport`, and `base_url_promoted` (not a
  failure: the base-URL failover moved the indexer to another host the definition lists).
  `failingSince` marks when the current streak began; `disabledTill` is present only while the
  circuit breaker is excluding the indexer from searches. `GET /api/indexers/status` gives the
  same picture fleet-wide, with healthy/failing/unknown counts.
- **Delete** — `DELETE /api/indexers/{slug}`.

## Searching and serving

Once an indexer is configured you can:

- **Search over JSON** — `GET /api/indexers/{slug}/search?q=...` (the same results the feed
  serves, as JSON, with download links sealed behind the `/dl` proxy).
- **Serve the Torznab feed** — point an app at
  `http://<host>:7478/api/indexers/<slug>/results/torznab?apikey=<key>`
  (see [Getting started](../getting-started.md#5-point-sonarrradarr-at-the-feed)).

To push this indexer into Sonarr/Radarr/qui automatically instead of configuring it in each
app, see **[App Sync](app-sync.md)**.
