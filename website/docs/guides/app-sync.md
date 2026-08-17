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

Syncing is **manual** — harbrr has no sync scheduler, so a sync happens when you trigger one.
The **Applications** page in the web UI drives everything below; the API calls are the
scriptable equivalent.

---

## Create a connection

```bash
curl -X POST http://<host>:7478/api/app-connections \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: <harbrr-api-key>' \
  -d '{
        "name": "Sonarr",
        "kind": "sonarr",
        "baseUrl": "http://sonarr:8989",
        "apiKey": "<sonarr-api-key>",
        "harbrrUrl": "http://harbrr:7478"
      }'
```

| Field           | Required    | Notes                                                                     |
| --------------- | ----------- | ------------------------------------------------------------------------- |
| `name`          | yes         | display name                                                              |
| `kind`          | yes         | `sonarr` \| `radarr` \| `lidarr` \| `readarr` \| `whisparr` \| `qui`      |
| `appId`         | conditional | reuse an existing **App** identity — then omit the three fields below      |
| `baseUrl`       | conditional | the app's base URL harbrr reaches it at                                   |
| `apiKey`        | conditional | the **app's** API key (sealed on the App)                                 |
| `harbrrUrl`     | conditional | harbrr's own base URL **as the app reaches it** (used to build feed URLs) |
| `syncLevel`     | no          | `full` (default) \| `add_update`                                          |
| `freeleechMode` | no          | `honor` \| `bypass` — defaults by kind (qui `bypass`, \*arrs `honor`)     |
| `syncProfileId` | no          | which indexers to route here (see below); omit for all of them            |

Only `name` and `kind` are always required. **Identity lives on an App**, not on the
connection (ADR 0004): pass `appId` to reuse an App you already configured, or pass the
inline trio (`baseUrl` + `apiKey` + `harbrrUrl`), which get-or-creates an App keyed by
`(kind, baseUrl)`. Either way the same app is only ever set up once.

- **`syncLevel: full`** also removes app indexers harbrr owns but no longer has (orphan
  cleanup, scoped to harbrr-owned rows only). **`add_update`** only adds/updates, never removes.
- **`freeleechMode: bypass`** pushes the `/full` feed variant (full catalog, for cross-seed);
  `honor` pushes the standard feed, which respects the indexer's freeleech setting.

Per-indexer sync behavior — `priority`, `minSeeders`, `syncCategories` — is configured on the
**indexer**, not the connection. See [Adding an indexer](add-indexer.md).

A successful create returns `201` with the connection (the app key is redacted in responses).

## Choose which indexers sync (sync profiles)

A **sync profile** is a named routing set: the indexers a connection pushes. Create one, then
point connections at it with `syncProfileId`.

```bash
curl -X POST http://<host>:7478/api/sync-profiles \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: <harbrr-api-key>' \
  -d '{ "name": "TV only", "indexerIds": [3, 7] }'
```

An empty (or omitted) `indexerIds` means **every compatible indexer** — the same as leaving
`syncProfileId` unset. Manage profiles with `GET`/`PATCH`/`DELETE /api/sync-profiles/{id}`;
deleting one is refused with `409` while any connection still references it.

---

## Test, sync, and check status

```bash
# Verify harbrr can reach and authenticate to the app
curl -X POST http://<host>:7478/api/app-connections/{id}/test \
  -H 'X-API-Key: <harbrr-api-key>'

# Reconcile the app's indexers to match harbrr (add / update / remove per syncLevel)
curl -X POST http://<host>:7478/api/app-connections/{id}/sync \
  -H 'X-API-Key: <harbrr-api-key>'

# Reconcile every connection in one call
curl -X POST http://<host>:7478/api/app-connections/sync \
  -H 'X-API-Key: <harbrr-api-key>'

# See the last sync outcome per indexer
curl http://<host>:7478/api/app-connections/{id}/status \
  -H 'X-API-Key: <harbrr-api-key>'
```

Every management endpoint needs auth — an `X-API-Key` header, or the session cookie the web
UI uses. Without one you get `401`.

Manage connections with the rest of the set: `GET`/`PATCH`/`DELETE /api/app-connections/{id}`
and `POST .../enable` · `.../disable`. The connection `PATCH` covers only `name`, `syncLevel`,
`freeleechMode`, and `syncProfileId` — **identity and credentials (base URL, app API key,
harbrr URL) are App-level and rotate via `PATCH /api/apps/{id}`**, which updates every
connection sharing that App at once.

:::note[qui and usenet]

qui takes torrent indexers; usenet (Newznab) indexers are skipped for qui and registered
as Newznab indexers in the Servarr apps (Sonarr / Radarr / Lidarr / Readarr / Whisparr). A
movie-only indexer is correctly accepted by Radarr and rejected by Sonarr (no `tv-search`) —
that's expected, not a sync failure.

:::

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

:::warning[Readarr is archived upstream]

Readarr was archived by its maintainers and is no longer actively developed. harbrr still
syncs to it (the v1 indexer API is unchanged) for users running an existing install, but no
new development should depend on it.

:::

**Mylar** (comics) is not yet a target — it's a separate spike, demand-gated. Pushing tracker
**credentials** into Upbrr is a separate, planned outbound sync.
