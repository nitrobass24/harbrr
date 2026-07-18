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
- Optional knobs: `SMOKE_QUERY` (default `test`), `SMOKE_QUERY_FALLBACK` (default `2024`, used
  when `test` returns nothing), `SMOKE_GRAB=1` (also resolve the first release's download link).

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
- A Prowlarr response **above** the 100-result page cap is clamped to harbrr's page-1 window
  before comparing — Prowlarr's search API is unpaged while harbrr correctly serves Torznab
  pages, so full-set-vs-one-page is a paging artifact, not a count mismatch.

## The differential bypasses harbrr's search cache

Every differential search (harbrr's half of each tracker's comparison) is issued with
`nocache=1`, harbrr's exact search-cache bypass trigger (see
`internal/web/torznabhttp/cachebypass.go`). Prowlarr, the oracle, is always queried live, so
without the bypass a repeat run inside the keyword TTL compares Prowlarr's live page-1 against a
**frozen harbrr cache window** — on a high-churn tracker that is a guaranteed false failure (see
[#164](https://github.com/autobrr/harbrr/issues/164): nzbindex failed title Jaccard 0.00 this way
while the driver itself was fine). Bypassing makes the comparison what the pass criteria already
assume: harbrr's live engine/driver output vs. Prowlarr's live output.

That means the differential no longer exercises harbrr's cache-aside read path. Cache coverage
moved to a **dedicated, single cached-path check** (`CheckCache` in the report, `cache` subtest in
`make smoke-test`): it runs once per suite, against one designated tracker (the first enabled
one), issuing two identical searches **without** the bypass and asserting the cache-hit counter
(`trackerHitsSaved` from `/api/cache/stats`) incremented. That is a direct signal that the second
request was actually served from cache — stronger than inferring a cache hit from the two
responses' result counts matching, which a coincidental re-fetch could also satisfy — and it's
cheap: one tracker, not every tracker. It does not require that tracker's differential to have
passed; the cache stores whatever harbrr returned regardless of whether it agreed with Prowlarr.

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
