# App Sync targets — golden bodies & per-app decisions

This is the **App Sync** layer record indexed by [`docs/divergences.md`](../../../docs/divergences.md).
It pins the exact on-the-wire indexer-create bodies harbrr pushes into each app, and records the
per-app decisions for the Servarr-shaped forks. The disposition vocabulary (`[Deliberate]` /
`[Accepted]` / `[Tracked]`) is defined in `docs/divergences.md`.

The goldens here are **doc-derived** — built from each app's documented indexer contract and the
live `GET /indexer/schema` field set confirmed during Phase-10 live validation, never captured from a live save. The live Prowlarr differential and a
real sync are the live-validation gate.

## Fixtures

One torrent + one usenet golden per Servarr-shaped target, freezing the `buildIndexer` body:

- `sonarr_create.golden.json` / `sonarr_create_usenet.golden.json`
- `radarr_create.golden.json` / `radarr_create_usenet.golden.json`
- `lidarr_create.golden.json` / `lidarr_create_usenet.golden.json`
- `readarr_create.golden.json` / `readarr_create_usenet.golden.json`
- `whisparr_create.golden.json` / `whisparr_create_usenet.golden.json`
- `qui_create.golden.json` — the snake-case `native`-backend body (qui is a separate driver).
- `sonarr_create_profile.golden.json` — a **sync-profile** body: mixed search-mode toggles
  (`enableRss: false`, `enableAutomaticSearch: true`, `enableInteractiveSearch: false`) and a
  `minimumSeeders` floor on a torrent indexer.

The torrent body is `implementation: "Torznab"` / `configContract: "TorznabSettings"` /
`protocol: "torrent"`; the usenet body flips those to `Newznab` / `NewznabSettings` / `usenet`.
Everything else (the `fields[]` shape, the feed `baseUrl`, the empty `apiPath`, the categories)
is identical between the two — the protocol flip is the only difference.

## Sync profiles (min seeders + search-mode toggles)

A connection's optional sync profile overrides three things in the pushed body, all captured
by `sonarr_create_profile.golden.json`:

- **`minimumSeeders`** is a `TorznabSettings`-only field, so `buildIndexer` appends it **only on
  the torrent branch** and **only when the profile set it** (`MinSeeders > 0`). A Newznab/usenet
  indexer never carries it (`NewznabSettings` has no seeders notion), and an unset floor (0) falls
  back to the app's own default — exactly the pre-sync-profile behavior. `[Deliberate]`
  (`internal/appsync/servarr.go` `buildIndexer`; `TestServarrMinSeedersTorrentOnly`).
- **`enableRss` / `enableAutomaticSearch` / `enableInteractiveSearch`** come from the profile's
  toggles ANDed with the instance's enabled state (a disabled instance forces all three false).
  With no profile, all three equal the instance's `Enabled` flag — which is why every non-profile
  golden above stayed **byte-identical** when the toggles were threaded through. `[Deliberate]`
  (`internal/appsync/sync.go` `resolveToggles`; the `hash()` divergence rule keeps a profile-less
  connection's `PayloadHash` unchanged on upgrade).

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
- **Mylar is not a target** — `[Tracked]`. Comics (Mylar) is a separate spike, demand-gated; it is **not** a Servarr v3 fork and does not reuse this driver.

## Per-app freeleech routing + the kind CHECK (#85)

- **`freeleech_mode` is defaulted by app kind, not asked of the user.** A new connection
  defaults `bypass` for qui (its single Torznab pool feeds cross-seed) and `honor` for
  every \*arr; app-sync appends `/results/torznab/full` to the pushed feed URL for a
  `bypass` connection. Operator-overridable per connection. `[Deliberate]`
  (`internal/appsync/sync.go` `feedURL`, `validate.go` `withDefaults`).
- **The `app_connections.kind` CHECK was dropped (#85 fix).** The 0003 CHECK allowed only
  `('sonarr','radarr','qui')` and was never widened when lidarr/readarr/whisparr shipped,
  so those three failed at INSERT even though `validateKind` accepts them. Migration 0008
  rebuilds the table without the kind CHECK — `internal/appsync/validate.go` `validateKind`
  is the single source of truth, so a future kind needs no migration. `[Resolved]`
  (migration `0008_app_connections_freeleech.sql`; standing test: `TestAppConnectionAllKindsRoundTrip`).
