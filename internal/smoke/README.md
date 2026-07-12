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
- Count ratio ≥ 0.50 **and** title Jaccard ≥ 0.30 → **pass**. (A full, config-sorted page can pass
  on count parity alone — the two instances fetch different sort windows, so titles aren't
  comparable there.)

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
