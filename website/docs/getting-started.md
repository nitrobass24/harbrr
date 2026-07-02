# Getting started

This page takes you from nothing to a running harbrr that Sonarr/Radarr can search
through. harbrr is a single-binary Torznab/Newznab provider — you point your apps at it,
and it searches your trackers and hands back results.

Everything here is done over harbrr's HTTP API. The interactive **Swagger UI at `/api/docs`**
is the whole interface for the alpha — there is no separate web UI yet, so the steps below
are the operator path. See **[The API & Swagger UI](api.md)** for the full reference.

!!! note "Alpha status"
    No stable image tag is published yet (`main` pushes do **not** publish images; only
    `v*` tags and same-repo PRs do). Pick a way to get a runnable image in step 1.

---

## 1. Run harbrr

The fastest path is Docker. A ready-to-edit [`docker-compose.example.yml`](https://github.com/autobrr/harbrr/blob/main/docker-compose.example.yml)
ships in the repo; the essentials:

```yaml
services:
  harbrr:
    image: ghcr.io/autobrr/harbrr:latest   # see the note below on getting an image
    container_name: harbrr
    restart: unless-stopped
    ports:
      - "7474:7474"                # drop this if only same-network apps reach harbrr
    volumes:
      - harbrr-config:/config      # SQLite db + the encryption keyfile (BACK THIS UP)
    environment:
      - TZ=${TZ:-Etc/UTC}          # match your stack so localized tracker dates parse
      - HARBRR_LOG_LEVEL=info

volumes:
  harbrr-config:
```

The image already runs `harbrr serve --host 0.0.0.0 --data-dir /config`, is non-root
(uid 1000), exposes port **7474**, and ships a `/healthz` healthcheck.

**Getting an image** (no stable tag yet — pick one):

- **Tag a release** — `git tag v0.1.0-alpha && git push origin v0.1.0-alpha` publishes
  `ghcr.io/autobrr/harbrr:0.1.0-alpha` + `:latest`.
- **PR image** (private) — `ghcr.io/autobrr/harbrr:pr-<n>`; `docker login ghcr.io` first.
- **Build from source** — replace `image:` with `build: .` and run from a checkout.

Then bring it up:

```bash
docker compose -f docker-compose.example.yml up -d harbrr
```

!!! warning "Back up the keyfile"
    The `/config` volume holds the SQLite database **and** the auto-generated encryption
    keyfile (`.keys/harbrr.key`). Tracker credentials are encrypted with that key — back it
    up **separately** from the database. Losing it means re-entering every tracker credential.

---

## 2. Create the admin (first run)

Open the Swagger UI at **`http://<host>:7474/api/docs`** and run these, or use `curl`.

harbrr starts with no users. Create the single admin account:

```bash
curl -X POST http://<host>:7474/api/auth/setup \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"a-long-passphrase"}'
```

`GET /api/auth/setup` reports whether setup is still pending. After this, log in with
`POST /api/auth/login` to get a session cookie (the Swagger UI does this for you).

!!! tip "Auth modes"
    The default `auth.mode: required` means a login. If harbrr sits behind an
    authenticating reverse proxy, you can run `auth.mode: disabled` with an `ip_allowlist`
    instead — see **[Configuration](configuration.md#auth)**.

---

## 3. Mint a Torznab API key

Sonarr/Radarr authenticate to the feed with an API key. Mint one — the **plaintext key is
shown only once**, so copy it now:

```bash
curl -X POST http://<host>:7474/api/apikeys \
  -H 'Content-Type: application/json' \
  -d '{"name":"sonarr"}'
```

Mint a separate key per consumer (one for Sonarr, one for Radarr, …) so you can revoke them
independently with `DELETE /api/apikeys/{id}`.

---

## 4. Add an indexer

Add and configure a tracker (credentials are encrypted at rest). The short version:

```bash
curl -X POST http://<host>:7474/api/indexers \
  -H 'Content-Type: application/json' \
  -d '{"definitionId":"yourtracker","settings":{"username":"...","password":"..."}}'
```

Every tracker has its own settings fields. **[Adding an indexer](guides/add-indexer.md)**
walks through discovering a definition's schema, configuring it, and testing connectivity
before you rely on it.

---

## 5. Point Sonarr/Radarr at the feed

In Sonarr/Radarr, add a **Generic Torznab** indexer with:

- **URL** — `http://harbrr:7474/api/indexers/<slug>/results/torznab`
  (use the container/host name your app can reach; `<slug>` is the indexer you added)
- **API Key** — the key you minted in step 3

That's it — Sonarr/Radarr now search your tracker through harbrr, and every consumer shares
harbrr's [search-results cache](features/search-results-cache.md) so the tracker sees far
fewer requests.

---

## Next steps

- **[Adding an indexer](guides/add-indexer.md)** — the full discover → configure → test flow.
- **[App Sync](guides/app-sync.md)** — let harbrr push indexer config straight into
  Sonarr/Radarr/Lidarr/Readarr/Whisparr/qui so you don't add it by hand in each.
- **[Configuration](configuration.md)** — every config key and environment variable.
- **[The API & Swagger UI](api.md)** — the complete HTTP reference.
