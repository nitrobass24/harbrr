# Adding an indexer

This guide walks through adding and configuring a tracker over the API: discover a
definition's settings, configure an instance, and test it before you rely on it. All of it
is doable interactively in the **Swagger UI at `/api/docs`**.

harbrr ships the full Cardigann/Jackett definition corpus plus native drivers, so most
trackers are already supported — you just supply your credentials.

---

## 1. Find a definition

List the available tracker definitions:

```bash
curl http://<host>:7478/api/definitions
```

Each entry has an `id` (for example `torrentleech`, `filelist`). That `id` is what you
configure against.

## 2. Read its settings schema

A definition declares which settings it needs (username/password, passkey, cookie, …) and
which of them are secret:

```bash
curl http://<host>:7478/api/definitions/torrentleech
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
  -d '{
        "definitionId": "torrentleech",
        "settings": { "username": "you", "password": "your-password" }
      }'
```

Fields you can send:

| Field          | Required | Notes                                                            |
| -------------- | -------- | ---------------------------------------------------------------- |
| `definitionId` | yes      | the definition `id` from step 1                                  |
| `slug`         | no       | URL identifier; defaults to `definitionId`                       |
| `name`         | no       | display name                                                     |
| `baseUrl`      | no       | override the tracker base URL (multi-domain trackers)            |
| `settings`     | no       | definition field → value; secrets stored encrypted              |

The `settings` map also accepts a few **reserved engine keys** when a tracker needs them:

- `proxy_type` / `proxy_url` — route this indexer through an HTTP or SOCKS5 proxy.
- `timeout` — per-indexer request timeout.
- `solver_type` / `flaresolverr_url` — anti-bot solver (e.g. FlareSolverr for Cloudflare),
  or `solver_type=manual_cookie` with an encrypted `cookie` setting for manual-cookie / 2FA.

A successful add returns `201` with the created instance (its `slug` and redacted settings).

## 4. Test it

Validate the credentials and connectivity against the live tracker — this uses a fresh,
uncached engine so it never disturbs a running session:

```bash
curl -X POST http://<host>:7478/api/indexers/torrentleech/test
```

`200 {"ok":true}` means the login/probe succeeded. `{"ok":false,"error":"..."}` returns a
**secret-scrubbed** reason you can act on.

---

## Updating and removing

- **Update** — `PATCH /api/indexers/{slug}`. Settings are **merged**; send the sentinel
  `<redacted>` for a secret field to keep the stored value unchanged.
- **Enable / disable** — `POST /api/indexers/{slug}/enable` · `.../disable`.
- **Status** — `GET /api/indexers/{slug}/status` (auth/rate-limit/parse health).
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
