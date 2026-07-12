# Live smoke harness

This package is harbrr's **live smoke harness** — a manual differential test that drives a
running harbrr daemon like a real *arr would: it discovers the indexers you already have
configured and **enabled**, searches each one through harbrr, and diffs the results against a
Prowlarr instance (the oracle) for the same query.

It is **manual, build-tagged (`//go:build smoke`), and never runs in CI** — it reaches real
trackers. This README covers **how to run it** and **how to report what it finds**. The full
setup (prerequisites, every environment variable, and the exact pass/fail criteria) lives in
**[`docs/smoke-setup.md`](../../docs/smoke-setup.md)**.

## Run it

```sh
export SMOKE_HARBRR_URL=http://<host>:7478     # the running daemon
export SMOKE_HARBRR_APIKEY=…                    # a harbrr API key
export SMOKE_PROWLARR_URL=http://<host>:9696    # the differential oracle
export SMOKE_PROWLARR_APIKEY=…
make smoke-test                                 # go test -tags smoke ./internal/smoke/
```

- **No per-tracker credentials are needed** — the daemon already holds them (encrypted at rest).
- Every **enabled** indexer is searched and matched to Prowlarr by name/slug. An indexer that
  Prowlarr doesn't have is **skipped** (not comparable), never failed.
- **Queries are category-aware by default.** When `SMOKE_QUERY` is unset, each tracker gets a
  bounded, content-appropriate query derived from its advertised categories — a specific film for
  movie trackers, a **single TV episode** (not a whole series) for TV, an album for music, a title
  for books — so both sides return a small, overlapping set instead of slamming the 100-result cap.
  Set `SMOKE_QUERY` (and optionally `SMOKE_QUERY_FALLBACK`) to force one query for every tracker.
- Optional knobs: `SMOKE_GRAB=1` (also resolve the first release's download link),
  `SMOKE_STRICT_FIELDS=1` (also fail on volatile field divergences — see below).

Per-tracker evidence is written to `testdata/smoke-<slug>.json` — **gitignored and
secret-scrubbed** (counts and a few titles, never a passkey/apikey/cookie). It is scratch
output for the current run, not a committed ledger — don't add run results to this repo.

## What counts as a failure

Per tracker, page 1 only (the full criteria are in
[`docs/smoke-setup.md`](../../docs/smoke-setup.md)):

- **Prowlarr has results but harbrr returns 0 (or far fewer)** → **fail** — a real bug to report.
- A `429`/`503` (rate-limit / anti-bot) is a **skip**, not a fail — re-run later.
- Count ratio ≥ 0.50 **and** title Jaccard ≥ 0.30 → **pass**. (Exception: when **both** sides hit
  the 100-result page cap **and** the count ratio is ≥ 0.90, low title Jaccard still passes — a
  full page is a config-sorted window, so titles aren't comparable there.)

### Field-level differential

On top of the result-set check, the harness compares **normalized fields** on the titles present
in **both** harbrr and Prowlarr (matched by normalized title, and only when a title is unique on
both sides — so two different releases sharing a title are never mispaired). A field either side
leaves unpopulated is **not-comparable** (skipped), never a fail, so this stays non-flaky on live
data.

- **Always compared (stable):** `size` (must differ by more than *both* 2% and 64 MiB, so
  legitimate 1-decimal-GB display rounding doesn't flap; a GiB-vs-GB unit bug still trips on
  releases roughly ≥ 1 GB — the 64 MiB floor makes the check lenient on smaller, e.g. music/book, releases),
  `category` (the major Torznab bucket must overlap — a movie tagged as TV fails; sub-category
  granularity is ignored), and the harbrr **download-link shape** (must be a sealed `/dl` URL or a
  magnet — a raw tracker `passkey`/`torrent_pass`/… link fails, as both a parity defect and a leak).
- Field comparison is **skipped** when both sides hit the 100-result page cap (the titles are
  config-sorted windows, not a stable set) — the bounded default queries keep runs under the cap.
- **Only under `SMOKE_STRICT_FIELDS=1` (volatile):** `seeders` (presence only — harbrr must report a
  positive count when Prowlarr reports a healthy swarm) and `publishDate` (within 48h). These move
  between the two fetches, so they are opt-in to keep routine runs green.

A field divergence surfaces as its own `field-parity` finding in the report; the detail names the
offending field and title but **never** echoes a URL or secret value.

## Report a finding back

When a tracker fails the differential, that's something for the maintainers to fix — **not**
something to record in this repo. To report it:

1. **Confirm it reproduces** — re-run just that tracker (a one-off `429`/`503` is a transient
   skip, not a bug).
2. **Open an issue** at [autobrr/harbrr](https://github.com/autobrr/harbrr/issues/new) with:
   - the tracker **slug** and its definition/driver **id**,
   - the **harbrr vs Prowlarr counts** and the **query** used,
   - the **`testdata/smoke-<slug>.json`** evidence file — it's already secret-free, so attach it
     as-is.
3. **Never** paste raw request URLs, `.torrent`/`.nzb` bytes, cookies, or API keys — those embed
   passkeys. The scrubbed evidence JSON is the safe thing to share.

Fixes land in the **engine** (or a native driver), never in a vendored definition — a definition
is consumed byte-for-byte from Jackett.
