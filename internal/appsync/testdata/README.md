# App Sync targets — golden bodies & per-app decisions

This is the **App Sync** layer record indexed by [`docs/divergences.md`](../../../docs/divergences.md).
It pins the exact on-the-wire indexer-create bodies harbrr pushes into each app, and records the
per-app decisions for the Servarr-shaped forks. The disposition vocabulary (`[Deliberate]` /
`[Accepted]` / `[Tracked]`) is defined in `docs/divergences.md`.

The goldens here are **doc-derived** — built from each app's documented indexer contract and the
live `GET /indexer/schema` field set confirmed during Phase-10 live validation (see
`docs/plan.md` Phase 10), never captured from a live save. The live Prowlarr differential and a
real sync are the live-validation gate.

## Fixtures

One torrent + one usenet golden per Servarr-shaped target, freezing the `buildIndexer` body:

- `sonarr_create.golden.json` / `sonarr_create_usenet.golden.json`
- `radarr_create.golden.json` / `radarr_create_usenet.golden.json`
- `lidarr_create.golden.json` / `lidarr_create_usenet.golden.json`
- `readarr_create.golden.json` / `readarr_create_usenet.golden.json`
- `whisparr_create.golden.json` / `whisparr_create_usenet.golden.json`
- `qui_create.golden.json` — the snake-case `native`-backend body (qui is a separate driver).

The torrent body is `implementation: "Torznab"` / `configContract: "TorznabSettings"` /
`protocol: "torrent"`; the usenet body flips those to `Newznab` / `NewznabSettings` / `usenet`.
Everything else (the `fields[]` shape, the feed `baseUrl`, the empty `apiPath`, the categories)
is identical between the two — the protocol flip is the only difference.

## Per-app decisions (Lidarr / Readarr / Whisparr)

All three reuse the shared `servarrDriver`; they are thin constructors over the same builder.

- **Indexer API version is parameterized, not the body** — `[Deliberate]`. Sonarr, Radarr, and
  Whisparr expose the indexer collection at `/api/v3/indexer`; Lidarr and Readarr expose the same
  Servarr-shaped collection at `/api/v1/indexer`. The version is threaded through the driver's
  lifecycle paths (`servarrIndexerPathV3` / `servarrIndexerPathV1`) and affects **only the request
  URL**, never the JSON body. This is why every `*_create.golden.json` is structurally identical
  across the five forks and why the existing Sonarr/Radarr goldens stayed **byte-identical** when
  the version was parameterized. `TestLidarrLifecycleV1` is the standing check that the v1 path is
  actually exercised end-to-end.
- **No `animeCategories` for any of the three** — `[Deliberate]`. Only Sonarr advertises an
  `animeCategories` field (its driver is constructed with `anime=true`). Lidarr, Readarr, and
  Whisparr are all `anime=false`, so the field is omitted from their bodies entirely — the per-app
  `HasNoAnimeField` guards pin this. Whisparr is a Servarr v3 sibling but is not anime-aware.
- **Readarr is archived upstream** — `[Accepted]`. Readarr was archived by its maintainers; harbrr
  keeps the target because its v1 indexer API is unchanged and existing installs still benefit. No
  new behavior is gated on it. (User-facing note: `website/docs/guides/app-sync.md`.)
- **Mylar is not a target** — `[Tracked]`. Comics (Mylar) is a separate spike, demand-gated in
  `docs/plan.md` Phase 11; it is **not** a Servarr v3 fork and does not reuse this driver.
