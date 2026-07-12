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
   the operator's Prowlarr for the identical query. The bar: page-1 count ratio and title
   [Jaccard similarity](https://en.wikipedia.org/wiki/Jaccard_index) within tolerance
   (typically count ≈ 1.00, Jaccard ≈ 1.00). Prowlarr is the oracle.
3. **Grab** — the served download link resolves to a real bencoded `.torrent`
   (`application/x-bittorrent`), and — for the full pipeline — a real *arr grabs it into a
   download client.

The harness is manual, build-tagged, and env-credentialed — it reaches real trackers and
**never runs in CI**. Raw evidence is secret-scrubbed and gitignored; the committed,
secret-free ledger is [`internal/smoke/README.md`](https://github.com/autobrr/harbrr/blob/main/internal/smoke/README.md).

## Auth & fetch patterns confirmed live

harbrr supports many tracker auth/fetch shapes. These are the ones **proven against a real
tracker** (the rest are offline-gated and tracked for a live pass when a qualifying tracker is
available):

| Pattern | Live result | Status |
|---|---|:--:|
| **apikey** (UNIT3D & friends, 11+ trackers) | count parity 1.00, title Jaccard ≈ 1.00 vs Prowlarr | ✅ |
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

Live runs have caught bugs the entire offline suite could not — a `nil`-`Transport` panic that
only appears when the real HTTP client is built, and a FileList decode failure from
integer-typed API flags. Both are fixed with regression tests. Detail in the
[smoke ledger](https://github.com/autobrr/harbrr/blob/main/internal/smoke/README.md).

## Native drivers

Native (non-Cardigann) drivers are all implemented and offline-gated against the documented
autobrr/Prowlarr contracts; live validation needs an account on each tracker. Rather than
duplicate the list, the **per-driver live status lives in the coverage matrix** — see the
[Native drivers table in Tracker coverage](coverage.md#native-drivers). BroadcastTheNet,
IPTorrents, FileList and MyAnonamouse are live-confirmed, as are the Usenet drivers (generic
Newznab and NZBIndex); the rest are built and offline-gated, pending credentials. Every driver
**degrades cleanly** — a parse or auth failure surfaces as a health event, never a crash.

## Help close the gaps

The ⬜ (built, not yet live-tested) rows in [Tracker coverage](coverage.md) are almost always
waiting on **an account on a qualifying tracker**, not on code. If you run one of these and can
help validate, that's one of the most useful contributions right now — see
[Contributing](https://github.com/autobrr/harbrr/blob/main/CONTRIBUTING.md).
