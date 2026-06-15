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

Two independent grabs prove the served download link resolves to a real bencoded
`.torrent` (`application/x-bittorrent`) and that a release flows from harbrr into a
live download client.

### A. Sonarr-orchestrated grab (full pipeline, 2026-06-14)

harbrr ran as a LAN-reachable Docker container; a real **Sonarr 4.0.17** added it as
a Torznab indexer, tested it, searched it, and grabbed a release — the complete
*arr → harbrr → download-client chain, end to end:

- harbrr deployed via `docker run -p 7474:7474` (Jackett-style Torznab URL
  `…/indexers/seedpool/results/torznab` + Sonarr `apiPath=/api`)
- Sonarr indexer **connectivity test passed** (HTTP 201) once configured with
  seedpool's actual advertised TV categories (`5000`+subcats); the default
  `5030/5040` correctly returns an empty feed — harbrr's result-category filter and
  Sonarr's own "no results in configured categories" check agree (see item 6)
- release: `The.Night.Agent.S03.1080p.NF.WEB-DL.DDP5.1.AV1-DBMS` (4.18 GB, seedpool,
  8 seeders) — a season pack of a monitored series
- Sonarr **history**: grabbed, `indexer = "harbrr seedpool (smoke)"`
- Sonarr **queue**: `status=downloading` on `client=qBittorrent2` (the live seedbox
  download client), season pack mapped across all 10 S03 episodes
- the feed's `guid`/download link was the **raw seedpool direct link** harbrr served
  via the inline `ResolveDownload` passthrough (item 7); Sonarr fetched the
  `.torrent` from it directly
- **left downloading/seeding — not removed** (no hit-and-run)

### B. Direct harbrr → qBittorrent push (earlier, sandbox env)

Before the Docker deployment, the grab was verified by a direct push (fetch the
feed's download link → add the `.torrent` to qBittorrent), because the daemon then
ran in a sandbox not reachable from the Sonarr container:

- release: `Daniel_VR_-_Only_In_My_Mind-(RSR0020)-SINGLE-WEB-2026-ZzZz` (6.1 MiB, seedpool)
- qBittorrent: **100% downloaded, seeding** (`stalledUP`), seedpool tracker
  announce **working** (status 2), category `harbrr-smoke`
- **left seeding — not removed** (no hit-and-run)

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
| **FlareSolverr / Cloudflare** | seam built (`login.Solver`), no CF tracker in the 5, no FlareSolverr in the env | `[Resolved: Phase 6]` — solver **implemented + offline-tested** (stub `/v1`, typed model, replay header contract); live CF clear `[Tracked: Phase 9 — live validation]` (no FlareSolverr/CF tracker on the day) |
| **user/pass form login** | lazy-login + form/post flows validated offline (replay Doer) only; all 5 trackers are apikey | `[Tracked: Phase 9 — live validation]` — tracker available per intake; run the build-tagged harness via the daemon + Prowlarr differential (confirm logout→relogin live) |
| **.NET-quirk sites** | the `WebUtility` URL encoder + `regexp2` (.NET regex) routing are validated by offline KAT/differential, not a live `*()'!`/unicode/regexp2 site | `[Tracked: Phase 9 — live validation]` — tracker available per intake; live-search inputs exercising those constructs |
| **cookie / manual-cookie sites** | cookie-auth + `ManualCookieSolver` exercised offline only | `[Tracked: Phase 9 — live validation]` — tracker available per intake; live-test via `solver_type=manual_cookie` |
| **per-indexer proxy (HTTP/SOCKS5)** | no proxy in the test env; doer construction is offline-tested | `[Tracked: Phase 9 — live validation]` — route a real search via `proxy_type`+`proxy_url` when a proxy is available (SOCKS4 unsupported — `x/net/proxy` has no socks4 dialer) |
| **Sonarr → harbrr (inbound)** | ~~the sandbox daemon was not LAN-reachable; grab used a direct qBittorrent push~~ | ✅ **resolved 2026-06-14** — harbrr deployed via Docker; Sonarr added/tested/searched/grabbed it through to qBittorrent2 (see Grab §A) |
| **download resolver / `/dl` proxy** | the 5 trackers are direct-link; resolver-needing defs aren't covered | `[Tracked: Phase 7]` |
