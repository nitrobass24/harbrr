# Divergences from Jackett / the Torznab·Newznab spec

harbrr's prime directive is **behavioral parity with Jackett's Cardigann engine
on the same input**. Where harbrr deliberately — or knowingly — differs, that
difference is **recorded once, with an explicit disposition, next to the fixtures
that exercise it**. This file is the *index* of where those records live and the
single rule they all follow; it is **not** a second copy of them, so there is no
parallel ledger to drift.

## Disposition vocabulary

Every divergence entry carries exactly one disposition, so the record is a
complete decision log rather than a half-tracked backlog:

- **`[Tracked: Phase N]`** — a real gap with a `docs/plan.md` follow-up item.
- **`[Partial: Phase N]`** — partly implemented in Phase N; the remainder is a
  `[Tracked]` gap for a later phase (the entry cites both).
- **`[Resolved: Phase N]`** — a once-tracked gap closed in Phase N, kept in the
  log so the decision history stays auditable (no longer a gap).
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
| **Engine** (Phases 1–2) | how a saved tracker response becomes a normalized release: extraction, login/session, request building, date/regex/selector parsing, the XML backend | [`internal/indexer/cardigann/parity/testdata/README.md`](../internal/indexer/cardigann/parity/testdata/README.md) |
| **Torznab output** (Phase 3) | how a normalized release becomes the served feed: the *arr-facing capabilities + results + error XML and the HTTP handler | [`internal/torznab/testdata/README.md`](../internal/torznab/testdata/README.md) |
| **Daemon foundation** (Phase 4) | how the daemon stores + serves: the §9 secrets model, persistence, auth/session/CSRF, and where these differ from autobrr/qui | [`internal/secrets/testdata/README.md`](../internal/secrets/testdata/README.md) |
| **Live smoke** (Phase 5) | what the live 5-tracker smoke + Prowlarr differential + Sonarr-orchestrated grab verified, and the auth/fetch patterns it could NOT exercise live (FlareSolverr, form login, .NET-quirk sites, cookie sites) with re-test dispositions | [`internal/smoke/README.md`](../internal/smoke/README.md) |
| **Operational safety** (Phase 6) | the anti-blacklist + observability layer: per-request timeouts, retry backoff, per-host rate limits, per-indexer proxies, and indexer health/status (the `ClientParams` seam, the rate/parse error taxonomy, SOCKS4 deferral) | [`internal/indexer/registry/testdata/README.md`](../internal/indexer/registry/testdata/README.md) |

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
- **Download links** — two halves of the download path, both completed in
  **Phase 7**: the engine's `ResolveDownload`/`Grab` reproduce Jackett's full
  download algorithm (before/infohash/selectors/testlinktorrent + the final fetch),
  and the output layer routes a resolver-needing indexer's links through the
  grab-time **`/dl` proxy** so the passkey never reaches the feed (direct-link
  trackers are still served inline). (engine: "Download resolver scope"
  `[Resolved: Phase 7]`; torznab: "Resolver-needing links routed through the /dl
  proxy" `[Resolved: Phase 7]`.)

## Open gaps

The actionable divergences are exactly the `[Tracked: Phase N]` entries across the
per-layer READMEs (the remainder of a `[Partial]` entry is one too). To list them:

```sh
grep -rn '\[Tracked' \
  internal/indexer/cardigann/parity/testdata/README.md \
  internal/torznab/testdata/README.md \
  internal/secrets/testdata/README.md \
  internal/indexer/registry/testdata/README.md \
  internal/smoke/README.md
```

When a tracked gap ships, update its entry in its own README — it is recorded in
exactly one place, so there is nothing else here to change.
