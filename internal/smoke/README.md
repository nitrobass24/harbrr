# Phase 5 live smoke — evidence + coverage ledger

The harness in this package is **manual, build-tagged (`//go:build smoke`), and
env-var-credentialed** — it reaches real trackers and never runs in CI. See
[`docs/phase5-setup.md`](../../docs/phase5-setup.md) to run it. Raw per-tracker
evidence is written to `testdata/*.json` (gitignored, secret-scrubbed); this file
is the committed, secret-free summary.

## Run recorded 2026-06-14 (5 trackers, query "test"/"2026")

A real Sonarr would parse the same caps + Torznab feed harbrr served here; each
tracker was searched through the running daemon and diffed against the user's
Prowlarr for the identical query (the differential oracle).

| Tracker (def) | harbrr | Prowlarr | differential | result |
|---|---|---|---|---|
| seedpool (`seedpool-api`) | 100 | 100 | count 1.00, title Jaccard **1.00** | ✅ pass |
| OnlyEncodes+ (`onlyencodes-api`) | 71 | 71 | count 1.00, title Jaccard **1.00** | ✅ pass |
| Darkpeers (`darkpeers`) | 98 | 98 | count 1.00, title Jaccard **1.00** | ✅ pass |
| Luminarr (`luminarr-api`) | 76 | 76 | count 1.00, title Jaccard **1.00** | ✅ pass |
| DigitalCore (`digitalcore-api`) | 100 | 100 | count **1.00**; titles incomparable¹ | ✅ pass (count parity) |

¹ DigitalCore's result order is **config-driven** (`sort`/`order` inputs) and the
response is capped at the 100-result page limit, so harbrr (def-default sort) and
the user's Prowlarr instance fetch different top-100 *windows* of a larger set —
title Jaccard (0.04) is not a valid comparison. harbrr's results were verified to
be valid DigitalCore releases for the query, with download links present. See
`diffPass` in `smoke_test.go`.

**Tolerances** (live data is non-deterministic; harbrr also category-filters):
page-1 count ratio ≥ 0.50 **and** title Jaccard ≥ 0.30; or, for a full-page
config-sorted window, count parity ≥ 0.90 with a caveat. Both-empty passes;
Prowlarr > 0 while harbrr = 0 fails.

## Grab (search → grab end-to-end)

harbrr's served download link resolved to a real bencoded `.torrent`
(`application/x-bittorrent`), and the release was grabbed into the live
qBittorrent client:

- release: `Daniel_VR_-_Only_In_My_Mind-(RSR0020)-SINGLE-WEB-2026-ZzZz` (6.1 MiB, seedpool)
- qBittorrent: **100% downloaded, seeding** (`stalledUP`), seedpool tracker
  announce **working** (status 2), category `harbrr-smoke`
- **left seeding — not removed** (no hit-and-run)

> **Method note (environment, not harbrr):** the grab was a **direct
> harbrr → qBittorrent push** (fetch the feed's download link → add the `.torrent`
> to qBittorrent), because the daemon under test ran in a sandbox not reachable
> from the Sonarr container, and Sonarr could not connect to it. The
> Sonarr-orchestrated half (parse caps → search → select) is proven by proxy: the
> harbrr feed matched Prowlarr **exactly** on 4/5 trackers, and the same Sonarr
> already consumes Prowlarr. Re-verifying a Sonarr-driven grab on a LAN-reachable
> harbrr deployment is the one remaining live step.

## Engine gaps the live smoke found — and fixed

| Finding | Root cause | Fix |
|---|---|---|
| All UNIT3D-API searches 500'd on date parsing | Go `encoding/json` keeps a JSON ISO string verbatim, but Jackett's Newtonsoft (`DateParseHandling.DateTime`) auto-converts it to a `DateTime` rendered `MM/dd/yyyy HH:mm:ss`; UNIT3D defs `dateparse` that form | `selector` reproduces Newtonsoft for ISO-`T` strings (`selector/jsonpath.go`) |
| DigitalCore search failed with `401` | The apikey is an `X-API-KEY` **header** sent on search, not on the `get` login probe; harbrr failed login on the 401 | `get`/`cookie` logins no longer fail on a 401 status (Jackett relies on error selectors); only form/post do (`login/methods.go`) |

## Coverage ledger — auth/fetch patterns NOT exercised live (re-test later)

The five smoke trackers are all `apikey`/`method: get`, so several patterns are
validated only by offline deterministic tests. Recorded here so later phases
account for them rather than rediscover them:

| Pattern | Why unverified live | Re-test disposition |
|---|---|---|
| **FlareSolverr / Cloudflare** | seam built (`login.Solver`), impl deferred; no CF tracker in the 5, no FlareSolverr in the env | `[Tracked: Phase 6]` — implement the FlareSolverr solver + live-test a CF tracker |
| **user/pass form login** | lazy-login + form/post flows validated offline (replay Doer) only; all 5 trackers are apikey | live-test a clean form-login tracker; confirm logout→relogin live |
| **.NET-quirk sites** | the `WebUtility` URL encoder + `regexp2` (.NET regex) routing are validated by offline KAT/differential, not a live `*()'!`/unicode/regexp2 site | add a corpus/live case with those inputs |
| **cookie / manual-cookie sites** | cookie-auth + `ManualCookieSolver` exercised offline only | live-test a cookie/2FA tracker via `solver_type=manual_cookie` |
| **Sonarr → harbrr (inbound)** | the sandbox daemon was not LAN-reachable; grab used a direct qBittorrent push | re-verify a Sonarr-orchestrated grab on a reachable deployment |
| **download resolver / `/dl` proxy** | the 5 trackers are direct-link; resolver-needing defs aren't covered | `[Tracked: Phase 7]` |
