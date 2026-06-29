# App Sync (\*arr / qui)

App Sync makes harbrr a drop-in Prowlarr for the core stack: instead of adding each indexer
by hand in every app, you configure it **once** in harbrr and harbrr **pushes** its indexer
feed into Sonarr, Radarr, Lidarr, Readarr, Whisparr, and qui through their APIs — adding,
updating, and (optionally) removing the corresponding indexer entries for you.

It's the one Prowlarr headline feature that makes harbrr the single source of truth for the
whole stack.

---

## How it works

You create an **app connection** per target app. harbrr then:

- **mints a dedicated harbrr API key** for that connection (so you can revoke one app's
  access without touching the others),
- builds the per-app **harbrr feed URL** each indexer should point at,
- and reconciles the app's indexers to match harbrr's — idempotently (re-syncing makes no
  spurious changes), with partial-failure isolation (one bad indexer doesn't sink the batch).

Sonarr, Radarr, and Whisparr share the Servarr **v3** indexer dialect; Lidarr and Readarr are
the same Servarr-shaped dialect on the **v1** indexer API; qui uses its native snake-case
backend. harbrr handles the differences per driver — you don't configure any of it.

---

## Create a connection

```bash
curl -X POST http://<host>:7474/api/app-connections \
  -H 'Content-Type: application/json' \
  -d '{
        "name": "Sonarr",
        "kind": "sonarr",
        "baseUrl": "http://sonarr:8989",
        "apiKey": "<sonarr-api-key>",
        "harbrrUrl": "http://harbrr:7474"
      }'
```

| Field        | Required | Notes                                                                    |
| ------------ | -------- | ------------------------------------------------------------------------ |
| `name`       | yes      | display name                                                             |
| `kind`       | yes      | `sonarr` \| `radarr` \| `lidarr` \| `readarr` \| `whisparr` \| `qui`     |
| `baseUrl`    | yes      | the app's base URL harbrr reaches it at                                  |
| `apiKey`     | yes      | the **app's** API key (stored encrypted)                                 |
| `harbrrUrl`  | yes      | harbrr's own base URL **as the app reaches it** (used to build feed URLs)|
| `syncLevel`  | no       | `full` (default) \| `add_update`                                         |
| `indexScope` | no       | `all` (default) \| `selected`                                            |
| `priority`   | no       | indexer priority pushed to the app (default `25`)                        |

- **`syncLevel: full`** also removes app indexers harbrr owns but no longer has (orphan
  cleanup, scoped to harbrr-owned rows only). **`add_update`** only adds/updates, never removes.
- **`indexScope: selected`** syncs only a chosen subset — set it with
  `PUT /api/app-connections/{id}/indexers`. `all` (default) syncs every configured indexer.

A successful create returns `201` with the connection (the app key is redacted in responses).

---

## Test, sync, and check status

```bash
# Verify harbrr can reach and authenticate to the app
curl -X POST http://<host>:7474/api/app-connections/{id}/test

# Reconcile the app's indexers to match harbrr (add / update / remove per syncLevel)
curl -X POST http://<host>:7474/api/app-connections/{id}/sync

# See the last sync outcome per indexer
curl http://<host>:7474/api/app-connections/{id}/status
```

Manage connections with the rest of the set: `GET`/`PATCH`/`DELETE /api/app-connections/{id}`
and `POST .../enable` · `.../disable`.

!!! note "qui and usenet"
    qui takes torrent indexers; usenet (Newznab) indexers are skipped for qui and registered
    as Newznab indexers in the Servarr apps (Sonarr / Radarr / Lidarr / Readarr / Whisparr). A
    movie-only indexer is correctly accepted by Radarr and rejected by Sonarr (no `tv-search`) —
    that's expected, not a sync failure.

---

## Supported targets

App Sync targets **Sonarr, Radarr, Lidarr, Readarr, Whisparr, and qui**. Each app is one
connection: point it at that app's `baseUrl` and give it that **app's own API key** (the
`POST /api/app-connections` flow above), then `test` and `sync`. The Servarr-shaped forks all
take both torrent (Torznab) and usenet (Newznab) indexers; qui takes torrent only.

| `kind`     | App      | Indexer dialect    | Notes                                          |
| ---------- | -------- | ------------------ | ---------------------------------------------- |
| `sonarr`   | Sonarr   | Servarr v3         | also gets `animeCategories`                    |
| `radarr`   | Radarr   | Servarr v3         | movie-only                                     |
| `whisparr` | Whisparr | Servarr v3         | adult-content Sonarr/Radarr sibling            |
| `lidarr`   | Lidarr   | Servarr **v1**     | music                                          |
| `readarr`  | Readarr  | Servarr **v1**     | books — see the caveat below                   |
| `qui`      | qui      | native snake-case  | torrent only (usenet indexers are skipped)     |

!!! warning "Readarr is archived upstream"
    Readarr was archived by its maintainers and is no longer actively developed. harbrr still
    syncs to it (the v1 indexer API is unchanged) for users running an existing install, but no
    new development should depend on it.

**Mylar** (comics) is not yet a target — it's a separate spike, demand-gated. Pushing tracker
**credentials** into Upbrr is a separate, planned outbound sync.
