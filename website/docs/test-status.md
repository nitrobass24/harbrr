# Test status

How far harbrr is **proven**, not just implemented. Every tracker harbrr serves passes its
offline golden tests; this page tracks the stronger bar — **live validation** against the real
tracker and the real *arr stack.

For the per-tracker Built/Live-tested matrix, see **[Tracker coverage](coverage.md)**. This
page is the evidence behind the "Live-tested ✅" column and the auth/fetch patterns that back
it.

## What "live-tested" means

A tracker is marked live-tested when it has been driven end-to-end against the real service,
not a fixture:

1. **Add + Test** — the indexer is configured with real credentials (encrypted at rest) and
   its login/connectivity probe (the `/test` action) passes.
2. **Prowlarr differential** — harbrr searches the tracker and the results are diffed against
   the operator's Prowlarr for the identical query. The pass bar is page-1 count ratio ≥ 0.50
   **and** title [Jaccard similarity](https://en.wikipedia.org/wiki/Jaccard_index) ≥ 0.30; in
   practice the runs land far above it, at count ≈ 1.00 and Jaccard ≈ 1.00. Prowlarr is the
   oracle, and the full criteria — including the empty-set and page-cap cases — are in
   [`docs/smoke-setup.md`](https://github.com/autobrr/harbrr/blob/main/docs/smoke-setup.md).
3. **Grab** — the served download link resolves to a real bencoded `.torrent`
   (`application/x-bittorrent`), and — for the full pipeline — a real *arr grabs it into a
   download client.

The harness is manual, build-tagged, and env-credentialed — it reaches real trackers and
**never runs in CI**. Per-run evidence is secret-scrubbed and gitignored rather than committed,
so this page is the durable record of what those runs proved. To run it yourself, see
[Golden smoke test](guides/smoke-test.md).

## Auth & fetch patterns confirmed live

harbrr supports many tracker auth/fetch shapes. These are the ones **proven against a real
tracker** (the rest are offline-gated and tracked for a live pass when a qualifying tracker is
available):

| Pattern | Live result | Status |
|---|---|:--:|
| **apikey** (UNIT3D & friends, 14 trackers) | count parity 1.00, title Jaccard ≈ 1.00 vs Prowlarr | ✅ |
| **user / pass form login** | full login → search → logout → relogin, count parity 1.00 | ✅ |
| **Cloudflare via FlareSolverr** | real CF challenge cleared, then searched, count parity 1.00 | ✅ |
| **grab via `/dl` — session cookie auth** | bare link was a login/CF page; `/dl` resolves a real `.torrent` server-side | ✅ |
| **grab via `/dl` — request-header auth** (`X-API-KEY`) | bare link 401'd; `/dl` resolves a real `.torrent` with the search-header auth | ✅ |
| **cookie / manual-cookie (Cardigann defs)** | `ManualCookieSolver` offline-proven; no supported cookie tracker in the test stack yet | ⬜ tracked |
| **.NET-quirk** (`*()'!` / unicode / `regexp2`) | encoder + .NET-regex routing offline-proven; no non-Latin/quirk tracker in the stack yet | ⬜ tracked |
| **per-indexer proxy (HTTP / SOCKS5)** | doer construction offline-proven; no proxy in the test env yet | ⬜ tracked |

## Live differential runs

| Date | Trackers | Result |
|---|--:|---|
| 2026-06-14 | 5 | 5/5 count parity 1.00 vs Prowlarr; full Sonarr → harbrr → qBittorrent grab confirmed |
| 2026-06-16 | 14 | 13/14 pass (1 Prowlarr-side skip); apikey + form + Cloudflare all confirmed |
| 2026-06-18 | 16 | grab pass — `/dl` cookie-auth and header-auth grabs confirmed; IPTorrents/FileList native live |
| 2026-07-14 | 1 | MoreThanTV via the **torznab preset** — 14 = 14, Jaccard 1.00. The site shut down in 2026-08 and the preset was retired; the surviving presets and the generic entry are offline-only |
| 2026-07-17 | 1 | **BrokenStones** (Gazelle) — 696 = 696 across harbrr's Torznab pages, head titles identical in order |
| Usenet pass | 2 | generic Newznab via the **DOGnzb preset** (search + a real `.nzb` grab through `/dl`) and the **NZBIndex** native driver — 100 = 100 vs Prowlarr. Run against the deployed fix build; exact date not recorded |

Cumulatively, **17** Cardigann corpus trackers plus the native drivers listed below are
live-confirmed; the per-tracker breakdown is in [Tracker coverage](coverage.md).

Live runs have caught bugs the entire offline suite could not — a `nil`-`Transport` panic on
the no-proxy path, which only surfaces once a real per-indexer HTTP client is built, and a
FileList decode failure from flags the API sends as integers (`0`/`1`) where the driver had
typed them as booleans. Both are fixed and pinned by regression tests.

## Native drivers

Native (non-Cardigann) drivers are all implemented and offline-gated against the documented
autobrr/Prowlarr contracts; live validation needs an account on each tracker. Rather than
duplicate the list, the **per-driver live status lives in the coverage matrix** — see the
[Native drivers table in Tracker coverage](coverage.md#native-drivers). BroadcastTheNet,
IPTorrents, FileList, MyAnonamouse, PassThePopcorn, HDBits and BrokenStones are live-confirmed,
as are the Usenet drivers (generic Newznab and NZBIndex); the rest are built and offline-gated,
pending credentials. Every driver **degrades cleanly** — a parse or auth failure surfaces as a
health event, never a crash.

## Download clients

One download-client path is live-proven: the full **Sonarr → harbrr → qBittorrent** grab
pipeline, confirmed in the 2026-06-14 run. Everything else on that surface — the **in-UI
send-to-client** flow, and the clients beyond qBittorrent (Deluge, Transmission, rTorrent,
Flood, SABnzbd, NZBGet, Download Station, qui, blackhole) — is built and offline-gated, but
**not yet live-tested**.

## Help close the gaps

The ⬜ (built, not yet live-tested) rows in [Tracker coverage](coverage.md) are almost always
waiting on **an account on a qualifying tracker**, not on code. If you run one of these and can
help validate, that's one of the most useful contributions right now — see
[Contributing](https://github.com/autobrr/harbrr/blob/main/CONTRIBUTING.md).
