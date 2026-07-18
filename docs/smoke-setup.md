# Live smoke-test setup

> **Operators:** for the built-in, no-toolchain golden smoke test — `harbrr smoke` (interactive
> first-run, runs natively or `docker exec … harbrr smoke`, writes a shareable secret-scrubbed
> `smoke-report.md`) — see the user guide: `website/docs/guides/smoke-test.md`. The rest of this
> doc is the **developer** differential harness (`make smoke-test`), which discovers already-enabled
> indexers in a running daemon and shares the same parity engine (`internal/smoke`).

The live smoke (`make smoke-test`) drives a **running harbrr daemon** like a real
*arr: it discovers the indexers already configured and enabled in the daemon,
matches each against Prowlarr, searches both, and asserts the two agree within a
tolerance. It is **manual only** — it reaches real trackers and is build-tagged
(`//go:build smoke`) so it never runs in CI.

No per-tracker credentials are needed — the daemon already holds them encrypted at
rest. Evidence files under `internal/smoke/testdata/` are gitignored and
secret-scrubbed before writing.

## Prerequisites

- A running harbrr daemon with indexers already configured and enabled.
- Prowlarr reachable, with the same trackers configured (the differential oracle).
- For the grab half: a Sonarr with harbrr added as a Torznab indexer and a
  download client (qBittorrent) wired.

## Environment variables

| Var | Meaning |
|---|---|
| `SMOKE_HARBRR_URL` | harbrr base URL, e.g. `http://192.168.10.220:7478` |
| `SMOKE_HARBRR_APIKEY` | a harbrr API key (used for `X-API-Key` + the Torznab `?apikey=`) |
| `SMOKE_PROWLARR_URL` | Prowlarr base URL |
| `SMOKE_PROWLARR_APIKEY` | Prowlarr API key |
| `SMOKE_QUERY` | optional, default `test` |
| `SMOKE_QUERY_FALLBACK` | optional, default `2024` (used when `test` returns 0 on both) |
| `SMOKE_GRAB=1` | optional — also resolve the first release's download link |

Example:

```sh
export SMOKE_HARBRR_URL=http://192.168.10.220:7478
export SMOKE_HARBRR_APIKEY=...
export SMOKE_PROWLARR_URL=http://192.168.10.220:9696
export SMOKE_PROWLARR_APIKEY=...
make smoke-test
```

The harness discovers every enabled harbrr indexer automatically and matches each
against Prowlarr by name/slug. Indexers absent from Prowlarr are skipped
(not-comparable), not failures.

## Differential pass criteria

Every differential search against harbrr is issued with `nocache=1` (harbrr's exact search-cache
bypass — see `internal/web/torznabhttp/cachebypass.go`), so it compares harbrr's live
engine/driver output against Prowlarr's always-live output rather than a possibly-frozen harbrr
cache window (see [#164](https://github.com/autobrr/harbrr/issues/164)). Cache coverage instead
comes from a single dedicated cached-path check, once per suite on one designated tracker: see
[`internal/smoke/README.md`](../internal/smoke/README.md#the-differential-bypasses-harbrrs-search-cache)
for the detail.

Per tracker, page-1 only:

- Prowlarr above the 100-result page cap while harbrr serves a full page → the
  oracle response is **unpaged** (Prowlarr's search API has no page cap, so a
  driver with no upstream paging — e.g. a Gazelle browse — hands it the entire
  result set in one response, while harbrr correctly serves a 100-result Torznab
  page). Prowlarr is clamped to harbrr's page-1 window before the ratio/Jaccard
  below — full-set-vs-one-page is a paging artifact, not a count mismatch
  (BrokenStones: Prowlarr 696 vs harbrr's page of 100 with identical heads).
- both empty → **pass** (the tracker had nothing for the query)
- Prowlarr > 0, harbrr = 0 → **fail**
- harbrr > 0, Prowlarr = 0 → **pass** (likely a Prowlarr cache miss)
- count ratio ≥ 0.50 **and** title Jaccard ≥ 0.30 → **pass**
- both sides at the 100-result page cap with count ratio ≥ 0.90 but low Jaccard →
  **pass with a caveat**: a full page is a *sort-dependent window* of a larger
  result set, and a config-driven sort (e.g. DigitalCore's `sort`/`order`) differs
  between harbrr and the user's Prowlarr instance, so the two windows don't
  overlap. Titles can't be compared there; count parity + a non-empty,
  download-bearing harbrr feed confirm the search works. (Real failures — empty,
  garbage, or low-count — still fail.)
- otherwise → **fail**

Tolerances are intentionally loose: live data is non-deterministic and harbrr
applies category filtering, so its count can be legitimately lower than Prowlarr's.

## The grab half (no hit-and-run)

The MVP gate also requires a real **search → grab end-to-end**. This is performed
manually through Sonarr (not by the smoke harness):

1. Add harbrr to Sonarr as a Torznab indexer (URL
   `…/api/indexers/{slug}/results/torznab`, the harbrr API key as the apikey).
2. Trigger a search and grab one **healthy / well-seeded** release.
3. **Leave the torrent seeding in qBittorrent — never auto-remove or delete it.**
   Private trackers penalize grab-then-remove (hit-and-run); leaving it seeding is
   the safeguard.
4. Confirm the grab in Sonarr's history and that the torrent reached qBittorrent.

## Why it's worth running

The offline golden suite injects a replay `Doer` and synthetic fixtures, so a whole
class of defect is invisible to it and only a live run surfaces:

- **Real server response shapes** — a real API sends fields in forms a synthetic
  golden guessed wrong (e.g. integer flags typed as `bool`, or a `<guid>` whose id
  looks credential-shaped and gets over-redacted, collapsing dedup).
- **Real transport** — the offline suite never builds the real `*http.Client`, so a
  transport-construction bug (e.g. a typed-nil `Transport`) can't show up there.
- **Real auth/fetch** — login, cookie rotation, Cloudflare clearance, and the `/dl`
  grab of a login/header-authenticated download only exercise against a live tracker.
- **Wrong indexer modeling** — a tracker Prowlarr serves with a bespoke implementation
  won't work as a generic driver; the differential (0 vs N results) exposes it.

That's the value of the manual live pass: it catches what a fixture can't predict.
When it catches one, follow the report loop in
[`internal/smoke/README.md`](../internal/smoke/README.md).
