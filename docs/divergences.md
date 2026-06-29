# Divergences from Jackett / the Torznab·Newznab spec

harbrr's prime directive is **behavioral parity with Jackett's Cardigann engine
on the same input**. Where harbrr deliberately — or knowingly — differs, that
difference is **recorded once, with an explicit disposition, next to the fixtures
that exercise it**. This file is the *index* of where those records live and the
single rule they all follow; it is **not** a second copy of them, so there is no
parallel ledger to drift.

**Scope:** this ledger is only for *behavioral* differences from Jackett on the same
input. **Unbuilt product features** (OIDC, backup/restore, stats/event-log,
user-configurable rate, fleet-status, …) are not divergences — they live in
`docs/plan.md` → "Beyond the alpha" (the demand-gated backlog), never as `[Tracked]`
entries here.

## Disposition vocabulary

Every divergence entry carries exactly one disposition, so the record is a
complete decision log rather than a half-tracked backlog:

- **`[Tracked]`** — a real gap with a `docs/plan.md` follow-up item.
- **`[Partial]`** — partly implemented; the remainder is a `[Tracked]` gap (the
  entry cites both).
- **`[Resolved]`** — a once-tracked gap now closed, kept in the log so the
  decision history stays auditable (no longer a gap).
- **`[Deliberate]`** — an intentional design choice; not a gap.
- **`[Accepted]`** — a difference harbrr chooses to keep (harbrr-additive or
  clean-degradation); no work planned. Revisit only if a vendored def needs it.

## Where each layer's divergences live

A divergence is documented in the README colocated with the test fixtures that
pin it, so the record sits beside the test that would catch a regression. The
layers' entries are **disjoint** — each divergence belongs to exactly one layer —
so there is nothing to keep in sync between them.

| Layer | What it covers | Record |
|-------|----------------|--------|
| **Engine** | how a saved tracker response becomes a normalized release: extraction, login/session, request building, date/regex/selector parsing, the XML backend | [`internal/indexer/cardigann/parity/testdata/README.md`](../internal/indexer/cardigann/parity/testdata/README.md) |
| **Torznab output** | how a normalized release becomes the served feed: the *arr-facing capabilities + results + error XML and the HTTP handler | [`internal/torznab/testdata/README.md`](../internal/torznab/testdata/README.md) |
| **Daemon foundation** | how the daemon stores + serves: the §9 secrets model, persistence, auth/session/CSRF, and where these differ from autobrr/qui | [`internal/secrets/testdata/README.md`](../internal/secrets/testdata/README.md) |
| **Live smoke** | what the live 5-tracker smoke + Prowlarr differential + Sonarr-orchestrated grab verified, and the auth/fetch patterns it could NOT exercise live (FlareSolverr, form login, .NET-quirk sites, cookie sites) with re-test dispositions | [`internal/smoke/README.md`](../internal/smoke/README.md) |
| **Operational safety** | the anti-blacklist + observability layer: per-request timeouts, retry backoff, per-host rate limits, per-indexer proxies, and indexer health/status (the `ClientParams` seam, the rate/parse error taxonomy, SOCKS4 deferral) | [`internal/indexer/registry/testdata/README.md`](../internal/indexer/registry/testdata/README.md) |
| **Native indexers** | the per-tracker native drivers for trackers Prowlarr/Jackett ship as bespoke C# (no Cardigann YAML): AvistaZ-family and the standalone ports — FileList (passkey/Basic JSON), MyAnonamouse (`mam_id` cookie + in-memory rotation), IPTorrents (cookie+UA HTML scrape) — and where each knowingly differs from its Prowlarr indexer | [`avistaz`](../internal/indexer/native/avistaz/testdata/README.md) · [`filelist`](../internal/indexer/native/filelist/testdata/README.md) · [`myanonamouse`](../internal/indexer/native/myanonamouse/testdata/README.md) · [`iptorrents`](../internal/indexer/native/iptorrents/testdata/README.md) |
| **App Sync** | the indexer-create bodies harbrr pushes into the \*arr / qui apps and the per-app decisions for the Servarr-shaped forks (Sonarr/Radarr/Whisparr on `/api/v3`, Lidarr/Readarr on `/api/v1`, qui's native backend), including the version-not-body parameterization, the anime-field omission, and the Readarr-archived / Mylar-deferred dispositions | [`internal/appsync/testdata/README.md`](../internal/appsync/testdata/README.md) |

The per-layer READMEs cross-link for navigation; this file is linked from
`docs/architecture.md` (invariant #2).

## Cross-layer relationships

A few engine-layer choices surface — or are resolved — at the output layer. The
records stay in their own layer; these are the durable relationships between
them (see each README for the live disposition, which is the single source):

- **Dates** — the engine stores the normalized `publishDate` as RFC3339; the
  Torznab serializer converts it to RFC1123Z on the wire, so the *served*
  `pubDate` matches Jackett. (engine: "Date canonical form"; torznab: "`pubDate`
  timezone".)
- **`leechers`** — the engine's normalized release carries `leechers`; the
  Torznab feed deliberately emits only `seeders` + `peers`, matching Jackett's
  `ReleaseInfo`. (engine: "`leechers` field".)
- **Category ordering** — the engine sorts a release's categories ascending; the
  caps category tree extends that determinism choice. (engine: "Category
  ordering"; torznab: "Custom-category top-level ordering".)
- **Download links** — two halves of the download path: the engine's
  `ResolveDownload`/`Grab` reproduce Jackett's full download algorithm
  (before/infohash/selectors/testlinktorrent + the final fetch), and the output
  layer routes a resolver-needing indexer's links through the grab-time
  **`/dl` proxy** so the passkey never reaches the feed (direct-link trackers are
  still served inline). (engine: "Download resolver scope" `[Resolved]`; torznab:
  "Resolver-needing links routed through the /dl proxy" `[Resolved]`.)

## Open gaps

The actionable divergences are exactly the `[Tracked]` entries across the
per-layer READMEs (the remainder of a `[Partial]` entry is one too). To list them:

```sh
# Every divergence record is a per-layer testdata README, plus the live-smoke
# README — globbed so a newly added layer (e.g. another native tracker) is
# covered automatically, with no path list here to drift.
grep -rn '\[Tracked' \
  internal/smoke/README.md \
  $(find internal -path '*/testdata/README.md' | sort)
```

When a tracked gap ships, update its entry in its own README — it is recorded in
exactly one place, so there is nothing else here to change.
