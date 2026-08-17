# Getting started

This page takes you from nothing to a running harbrr that Sonarr/Radarr can search
through. harbrr is a single-binary Torznab/Newznab provider — you point your apps at it,
and it searches your trackers and hands back results.

harbrr's web UI at **`http://<host>:7478/`** covers every step below — create the admin, mint
a key, add an indexer, wire up your apps. The `curl` calls here are the scriptable equivalent,
and the interactive **Swagger UI at `/api/docs`** lets you run them from the browser. See
**[The API & Swagger UI](api.md)** for the full reference.

:::note[Image tags]

Pushes to `main` publish `ghcr.io/autobrr/harbrr:develop`; `v*` tags publish the matching
version tags plus `:latest`. Same-repo PRs publish a `pr-<n>` image.

:::

---

## 1. Run harbrr

The fastest path is Docker. A ready-to-edit [`docker-compose.example.yml`](https://github.com/autobrr/harbrr/blob/main/docker-compose.example.yml)
ships in the repo; the essentials:

```yaml
services:
  harbrr:
    image: ghcr.io/autobrr/harbrr:latest   # or pin a version — see below
    container_name: harbrr
    restart: unless-stopped
    ports:
      - "7478:7478"                # drop this if only same-network apps reach harbrr
    volumes:
      - harbrr-config:/config      # SQLite db + the encryption keyfile (BACK THIS UP)
    environment:
      - TZ=${TZ:-Etc/UTC}          # match your stack so localized tracker dates parse
      - HARBRR_LOG_LEVEL=info

volumes:
  harbrr-config:
```

The image already runs `harbrr serve --host 0.0.0.0 --data-dir /config`, is non-root
(uid 1000), exposes port **7478**, and ships a `/healthz` healthcheck.

**Which image**: `ghcr.io/autobrr/harbrr:latest` is the normal path — it follows the most
recent release. Alternatives:

- **Pin a version** — `ghcr.io/autobrr/harbrr:0.1.0-alpha`, so an upgrade is a deliberate act.
- **Track `main`** — `ghcr.io/autobrr/harbrr:develop`, rebuilt on every push to `main`.
- **PR image** (private) — `ghcr.io/autobrr/harbrr:pr-<n>`; `docker login ghcr.io` first.
- **Build from source** — replace `image:` with `build: .` and run from a checkout.

Then bring it up:

```bash
docker compose -f docker-compose.example.yml up -d harbrr
```

:::warning[Back up the keyfile]

The `/config` volume holds the SQLite database **and** the auto-generated encryption
keyfile (`.keys/harbrr.key`). Tracker credentials are encrypted with that key — back it
up **separately** from the database. Losing it means re-entering every tracker credential.

:::

---

## 2. Create the admin (first run)

Open the web UI at **`http://<host>:7478/`** and it walks you through this, or use `curl`
(the Swagger UI at `/api/docs` runs the same calls from the browser).

harbrr starts with no users. Create the single admin account:

```bash
curl -X POST http://<host>:7478/api/auth/setup \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"a-long-passphrase"}'
```

`GET /api/auth/setup` reports whether setup is still pending. Everything after this needs a
session, so log in and keep the cookies — the browser UIs do this for you:

```bash
curl -X POST http://<host>:7478/api/auth/login \
  -H 'Content-Type: application/json' \
  -c cookies.txt \
  -d '{"username":"admin","password":"a-long-passphrase"}'

CSRF=$(awk '$6 == "harbrr_csrf" { print $7 }' cookies.txt)
```

Login sets the session cookie **and** a non-HttpOnly `harbrr_csrf` cookie. Every
cookie-authenticated `POST`/`PUT`/`PATCH`/`DELETE` must echo that value in an
`X-CSRF-Token` header or harbrr answers `403`. (Callers using `X-API-Key` instead of a
session are exempt.)

:::tip[Auth modes]

The default `auth.mode: required` means a login. If harbrr sits behind an
authenticating reverse proxy, you can run `auth.mode: disabled` with an `ip_allowlist`
instead — see **[Configuration](configuration.md#auth)**.

:::

---

## 3. Mint a Torznab API key

Sonarr/Radarr authenticate to the feed with an API key. Mint one — the **plaintext key is
shown only once**, so copy it now:

```bash
curl -X POST http://<host>:7478/api/apikeys \
  -H 'Content-Type: application/json' \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -d '{"name":"sonarr"}'
```

Mint a separate key per consumer (one for Sonarr, one for Radarr, …) so you can revoke them
independently with `DELETE /api/apikeys/{id}`.

---

## 4. Add an indexer

Add and configure a tracker (credentials are encrypted at rest). The short version:

```bash
curl -X POST http://<host>:7478/api/indexers \
  -H 'Content-Type: application/json' \
  -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -d '{"definitionId":"yourtracker","settings":{"username":"...","password":"..."}}'
```

Every tracker has its own settings fields. **[Adding an indexer](guides/add-indexer.md)**
walks through discovering a definition's schema, configuring it, and testing connectivity
before you rely on it.

---

## 5. Point Sonarr/Radarr at the feed

In Sonarr/Radarr, add a **Generic Torznab** indexer with:

- **URL** — `http://harbrr:7478/api/indexers/<slug>/results/torznab`
  (use the container/host name your app can reach; `<slug>` is the indexer you added)
- **API Key** — the key you minted in step 3

That's it — Sonarr/Radarr now search your tracker through harbrr, and every consumer shares
harbrr's [search-results cache](features/search-results-cache.md) so the tracker sees far
fewer requests.

:::tip[One entry for every indexer]

Instead of adding one Torznab entry per tracker, put `all` in the `<slug>` position —
`http://harbrr:7478/api/indexers/all/results/torznab` searches **every enabled indexer**
through a single Sonarr/Radarr entry. Two narrower selectors sit in the same position:
`profile:<name>` for the members of a [sync profile](guides/app-sync.md), and
`status:healthy` for `all` minus the indexers harbrr currently considers broken.

:::

---

## Next steps

- **[Adding an indexer](guides/add-indexer.md)** — the full discover → configure → test flow.
- **[App Sync](guides/app-sync.md)** — let harbrr push indexer config straight into
  Sonarr/Radarr/Lidarr/Readarr/Whisparr/qui so you don't add it by hand in each.
- **[Configuration](configuration.md)** — every config key and environment variable.
- **[The API & Swagger UI](api.md)** — the complete HTTP reference.
