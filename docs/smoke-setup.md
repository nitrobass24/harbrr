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
| `SMOKE_QUERY` | optional — force one query for every tracker; unset = **category-aware default** (see below) |
| `SMOKE_QUERY_FALLBACK` | optional — secondary query when the primary returns 0 (only used alongside an explicit `SMOKE_QUERY`) |
| `SMOKE_GRAB=1` | optional — also resolve the first release's download link |
| `SMOKE_STRICT_FIELDS=1` | optional — also fail the run on volatile field divergences (`seeders`, `publishDate`); the stable field checks always run |

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

### Category-aware default queries

A broad query (`test`, `2024`) makes both sides return a full 100-result page, which is a
sort-dependent *window* of a much larger set — the titles then don't overlap and can't be
compared (the count-parity caveat below, and the field diff skips too). To avoid that, when
`SMOKE_QUERY` is **unset** each tracker is searched with a **bounded, content-appropriate**
query chosen from its advertised categories:

| Tracker content (major category) | Default query |
|---|---|
| Movies (2000) | a specific film — e.g. `Oppenheimer 2023` |
| TV (5000) | a **single episode**, not a series — e.g. `The Last of Us S01E01` |
| Audio (3000) | one album — e.g. `Radiohead In Rainbows` |
| Books (7000) | one title — e.g. `Project Hail Mary` |
| PC/apps (4000), games (1000) | a specific title |
| none recognized / caps unavailable | a generic film title |

A general tracker that serves several types uses the first match in that order. Setting
`SMOKE_QUERY` overrides this and forces the same query for every tracker (with
`SMOKE_QUERY_FALLBACK` as the secondary).

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

### Field-level differential

Beyond the result-set counts/titles above, the harness also compares **normalized
fields** on the titles present in **both** sets (matched by normalized title, only
when the title is unique on both sides). A field either side leaves unpopulated is
**not-comparable** (skipped), never a fail — so this is non-flaky by construction and
surfaces as a separate `field-parity` finding per tracker:

- **Always compared (stable):**
  - `size` — must differ by more than **both** 2% and 64 MiB. The absolute floor absorbs
    1-decimal "X.Y GB" display rounding (one side scrapes a rounded string, the other reports exact
    API bytes); the relative floor still catches a proportional GiB-vs-GB unit bug (~7.4%) on
    releases roughly ≥ 1 GB. On smaller releases (e.g. music/ebooks) the 64 MiB floor makes the size
    check lenient — a unit bug there is caught by category/count checks, not size.
  - `category` — the *major* Torznab bucket (2040 → 2000) must overlap; a movie mis-mapped to TV
    fails. Sub-category granularity and indexer-custom categories (≥ 100000) are ignored.
  - **download-link shape** — harbrr's `<link>`/`<enclosure>` must be a sealed `/dl` URL or a
    magnet; a raw tracker `passkey`/`torrent_pass`/`authkey`/`rsskey` link fails (parity defect
    **and** a secret leak).
- **Only under `SMOKE_STRICT_FIELDS=1` (volatile):**
  - `seeders` — presence only: harbrr must report a positive count when Prowlarr reports a healthy
    swarm (≥ 5). Magnitudes move constantly, so they aren't compared.
  - `publishDate` — within 48h (some indexers report coarse dates).

Finding details name the offending field and title but **never** echo a URL or a secret value.

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
