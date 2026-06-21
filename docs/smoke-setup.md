# Live smoke-test setup

The live smoke (`make smoke-test`) drives a **running harbrr daemon** like a real
*arr: it adds each tracker to harbrr (credentials encrypted by the daemon),
searches harbrr's Torznab feed, searches **Prowlarr** for the same tracker+query,
and asserts the two agree within a tolerance. It is **manual only** — it reaches
real trackers and is build-tagged (`//go:build smoke`) so it never runs in CI.

> **Credentials live in env vars only** — never committed, never logged. The
> harness POSTs each tracker key to the daemon's management API, which encrypts it
> at rest (AES-256-GCM). Evidence files under `internal/smoke/testdata/` are
> gitignored and secret-scrubbed before writing.

## Prerequisites

- A running harbrr daemon (`bin/harbrr serve`) with first-run setup done and an
  API key minted (`POST /api/apikeys`).
- Prowlarr reachable, with the same trackers configured (the differential oracle).
- For the grab half: a Sonarr with harbrr added as a Torznab indexer and a
  download client (qBittorrent) wired.

## Environment variables

| Var | Meaning |
|---|---|
| `SMOKE_HARBRR_URL` | harbrr base URL, e.g. `http://127.0.0.1:7474` |
| `SMOKE_HARBRR_APIKEY` | a harbrr API key (used for `X-API-Key` + the Torznab `?apikey=`) |
| `SMOKE_PROWLARR_URL` | Prowlarr base URL |
| `SMOKE_PROWLARR_APIKEY` | Prowlarr API key |
| `SMOKE_TRACKERS` | comma-separated `slug|definitionId|prowlarrDefinitionName` (no secrets) |
| `SMOKE_KEY_<SLUG>` | each tracker's API key; `<SLUG>` is the slug upper-cased with `-`/`.`→`_` |
| `SMOKE_QUERY` | optional, default `test` |
| `SMOKE_QUERY_FALLBACK` | optional, default `2024` (used when `test` returns 0 on both) |

Example (`SMOKE_KEY_*` values are the real tracker keys, set in your shell — never
checked in):

```sh
export SMOKE_HARBRR_URL=http://127.0.0.1:7474
export SMOKE_HARBRR_APIKEY=...
export SMOKE_PROWLARR_URL=http://192.168.10.220:9696
export SMOKE_PROWLARR_APIKEY=...
export SMOKE_TRACKERS="seedpool|seedpool-api|seedpool-api,onlyencodes|onlyencodes-api|onlyencodes-api,digitalcore|digitalcore-api|digitalcore-api,darkpeers|darkpeers|darkpeers,luminarr|luminarr-api|luminarr-api"
export SMOKE_KEY_SEEDPOOL=... SMOKE_KEY_ONLYENCODES=... SMOKE_KEY_DIGITALCORE=... SMOKE_KEY_DARKPEERS=... SMOKE_KEY_LUMINARR=...
make smoke-test
```

## Differential pass criteria

Per tracker, page-1 only:

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

1. Add harbrr to Sonarr as a Torznab indexer (URL `…/api/v2.0/indexers/{slug}/`,
   the harbrr API key as the apikey).
2. Trigger a search and grab one **healthy / well-seeded** release.
3. **Leave the torrent seeding in qBittorrent — never auto-remove or delete it.**
   Private trackers penalize grab-then-remove (hit-and-run); leaving it seeding is
   the safeguard.
4. Confirm the grab in Sonarr's history and that the torrent reached qBittorrent.
