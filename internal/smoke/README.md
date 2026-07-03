# Live smoke — evidence + coverage ledger

The harness in this package is **manual, build-tagged (`//go:build smoke`), and
env-var-credentialed** — it reaches real trackers and never runs in CI. See
[`docs/smoke-setup.md`](../../docs/smoke-setup.md) to run it. Raw per-tracker
evidence is written to `testdata/*.json` (gitignored, secret-scrubbed); this file
is the committed, secret-free summary.

## Extended harness (the live alpha gate)

The harness now drives **every auth/fetch pattern**, not just apikey trackers. Per
tracker it adds the indexer, runs the **Test action** (live login probe), searches
harbrr's Torznab feed, and diffs against Prowlarr. Env contract (full detail in
`smoke_test.go`'s doc comment and `docs/smoke-setup.md`):

- `SMOKE_TRACKERS = "slug|defId|prowlarrName[|pattern],…"` — the optional 4th field is
  a label (`apikey`/`form`/`cookie`/`netquirk`/`cloudflare`/`proxy`/`avistaz`) recorded
  in evidence.
- `SMOKE_SETTINGS_<SLUG>` — a JSON object of **any** harbrr settings (replaces the
  apikey-only model), e.g. `{"cookie":"…","solver_type":"manual_cookie"}`,
  `{"solver_type":"flaresolverr","flaresolverr_url":"http://flaresolverr:8191"}`,
  `{"proxy_type":"socks5","proxy_url":"socks5://host:1080"}`,
  `{"username":"…","password":"…","pid":"…"}` (AvistaZ). `SMOKE_KEY_<SLUG>` stays as an
  apikey shorthand.
- `SMOKE_GRAB=1` — also resolves the first release's link to a real `.torrent`/magnet
  (the qBittorrent push + seeding stays a **manual, no-hit-and-run** step).

Each evidence record gains `pattern`, `testOk`, and `grab`. Pull existing creds out of
Prowlarr (its API masks them) with `scripts/prowlarr-extract-creds.sh prowlarr.db`.

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

- harbrr deployed via `docker run -p 7478:7478` (Jackett-style Torznab URL
  `…/indexers/seedpool/results/torznab` + Sonarr `apiPath=/api`)
- Sonarr indexer **connectivity test passed** (HTTP 201) once configured with
  seedpool's actual advertised TV categories (`5000`+subcats); the default
  `5030/5040` correctly returns an empty feed — harbrr's result-category filter and
  Sonarr's own "no results in configured categories" check agree
- release: `The.Night.Agent.S03.1080p.NF.WEB-DL.DDP5.1.AV1-DBMS` (4.18 GB, seedpool,
  8 seeders) — a season pack of a monitored series
- Sonarr **history**: grabbed, `indexer = "harbrr seedpool (smoke)"`
- Sonarr **queue**: `status=downloading` on `client=qBittorrent2` (the live seedbox
  download client), season pack mapped across all 10 S03 episodes
- the feed's `guid`/download link was the **raw seedpool direct link** harbrr served
  via the inline `ResolveDownload` passthrough; Sonarr fetched the
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
validated only by offline deterministic tests. Recorded here so they are
accounted for rather than rediscovered:

| Pattern | Why unverified live | Re-test disposition |
|---|---|---|
| **FlareSolverr / Cloudflare** | seam built (`login.Solver`), no CF tracker in the 5, no FlareSolverr in the env | `[Resolved]` — solver offline-tested; **live CF clear confirmed** (torrentleech, FlareSolverr) in the run below |
| **user/pass form login** | lazy-login + form/post flows validated offline (replay Doer) only; all 5 trackers are apikey | `[Resolved]` — **confirmed live** (racingforme, 60=60 vs Prowlarr) in the run below |
| **.NET-quirk sites** | the `WebUtility` URL encoder + `regexp2` (.NET regex) routing are validated by offline KAT/differential, not a live `*()'!`/unicode/regexp2 site | `[Tracked]` — tracker available per intake; live-search inputs exercising those constructs |
| **cookie / manual-cookie sites** | cookie-auth + `ManualCookieSolver` exercised offline only | `[Tracked]` — tracker available per intake; live-test via `solver_type=manual_cookie` |
| **per-indexer proxy (HTTP/SOCKS5)** | no proxy in the test env; doer construction is offline-tested | `[Tracked]` — route a real search via `proxy_type`+`proxy_url` when a proxy is available (SOCKS4 unsupported — `x/net/proxy` has no socks4 dialer) |
| **Sonarr → harbrr (inbound)** | ~~the sandbox daemon was not LAN-reachable; grab used a direct qBittorrent push~~ | ✅ **resolved 2026-06-14** — harbrr deployed via Docker; Sonarr added/tested/searched/grabbed it through to qBittorrent2 (see Grab §A) |
| **download resolver / `/dl` proxy** | the 5 trackers are direct-link; resolver-needing defs aren't covered | `[Tracked]` |

### Native drivers — live-validation status

All native drivers are implemented and **offline-gated** (synthetic goldens from the documented
autobrr/Prowlarr contracts). **BroadcastTheNet is live-confirmed**; the **#63 batch has never been run
against the live tracker** — the operator holds no credentials for those. Per-tracker live validation
(configure a key/cookie → `/test` → Prowlarr differential + a `/dl` grab, via the container) is the
standing re-test for the `[Tracked]` rows. All degrade cleanly (parse/auth errors → health events).

| Driver | Auth / download | Live disposition |
|---|---|---|
| **BroadcastTheNet** (#62) | apikey in JSON-RPC body / `/dl` | ✅ live-confirmed 2026-06-24 |
| **Redacted** (#63) | `Authorization: <apikey>` (bare) header / `/dl` (header auth) | `[Tracked]` — `SMOKE_KEY_RED` |
| **Orpheus** (#63) | `Authorization: token <apikey>` header / `/dl` (header auth) | `[Tracked]` — `SMOKE_KEY_OPS` |
| **PassThePopcorn** (#63) | ApiUser+ApiKey headers / `/dl` (header auth) | `[Tracked]` |
| **GazelleGames** (#63) | X-API-Key header; passkey fetched via `quick_user` / `/dl` (passkey URL) | `[Tracked]` |
| **AnimeBytes** (#63) | username+passkey in query / `/dl` (passkey URL) | `[Tracked]` |
| **HDBits** (#63) | username+passkey in JSON POST body / `/dl` (passkey URL) | `[Tracked]` |
| **BeyondHD** (#63) | api_key in URL path + rsskey in body / `/dl` (rsskey URL) | `[Tracked]` |
| **TorrentDay** (#63) | session cookie header / `/dl` (cookie auth) | `[Tracked]` |

## Live run — 2026-06-16 (14 trackers, automated)

Driven fully from `prowlarr.db` via `scripts/phase9-smoke.sh` (extract → env →
harness): each indexer added (creds encrypted), login-probed (Test action),
searched, diffed against the live Prowlarr, and grabbed.

**Broad Prowlarr differential — 13/14 PASS** (1 skip: aura4k-api, a Prowlarr-side
HTTP 400, not harbrr). Every tracker hit **count parity 1.00** with Prowlarr; title
Jaccard ~1.00 on all but digitalcore (the known config-sorted-window case → count
parity).

| Pattern | Live result | Disposition |
|---|---|---|
| **apikey** (11 trackers) | count 1.00, Jaccard ~1.00 vs Prowlarr | `[Resolved]` |
| **user/pass form login** (racingforme) | 60=60, Jaccard 1.00, logout→relogin live | `[Resolved]` |
| **Cloudflare via FlareSolverr** (torrentleech) | 35=35, real CF clear + search | `[Resolved]` |
| **grab → `.torrent`** | 11/13 resolved a bencoded `.torrent` (incl. the form tracker) | `[Resolved]` (resolve half; the qBit push + seed stays the manual no-H&R step) |

**Critical bug found + fixed — the headline catch.** Every live search/login of a
non-proxied tracker panicked: `nil pointer dereference` in
`net/http.(*Transport).alternateRoundTripper`, surfaced as an empty HTTP 500. Root
cause: `newDoer` assigned a **typed-nil `*http.Transport`** (buildTransport returns
nil for the no-proxy case) to `http.Client.Transport`, making the interface non-nil
and bypassing `http.DefaultTransport`. Invisible to the entire offline suite (it
injects a replay `Doer` and never builds the real `*http.Client`) — only a live run
could hit it. Fixed in **PR #42** (`registry/client.go`) with a regression test that
builds the real no-proxy client.

## Live run — 2026-06-18 (grab pass, updated build)

Re-run with `SMOKE_GRAB=1` against the build carrying the grab-auth `/dl` fix (#44), the
three native drivers (#45), the FileList int-flags fix, and the MyAnonamouse write-back
seam (#46). Confirms the two remaining gaps live.

| Pattern / tracker | Live result | Disposition |
|---|---|---|
| **grab via `/dl` — session cookie** (torrentleech) | `grab: torrent` — a real bencoded `.torrent` (was a login/CF page) | `[Resolved]` |
| **grab via `/dl` — request header** (digitalcore, X-API-KEY) | `grab: torrent` — a real `.torrent` (was a 401) | `[Resolved]` |
| **IPTorrents** native (cookie+UA, HTML scrape) | 50=50 vs Prowlarr, Jaccard 1.00, `grab: torrent` | `[Resolved]` |
| **FileList** native (passkey/Basic, JSON) | search OK (the int-flags fix — was HTTP 500); Prowlarr differential auto-skipped (its native indexer isn't named `filelist`) | `[Resolved]` (parse fixed; live differential pending a Prowlarr-name match) |
| **MyAnonamouse** native (`mam_id` cookie) | driver correct — reported `mam_id expired or invalid`; the session is dead at source (fails in Prowlarr too, ASN-locked). Write-back seam ready to maintain a live one | `[Tracked]` — live search/parse pending a fresh MAM session |
| 13 other trackers (apikey/form/CF) | count parity 1.00 | `[Resolved]` |

Two non-harbrr failures this run: **seedpool** (tracker down for maintenance) and **MAM**
(dead session). No harbrr regressions.

**FileList int-flags bug — caught only live.** The API sends `freeleech`/`internal`/
`doubleup` as integers (`0/1`); the struct typed them `bool`, so `json.Unmarshal` failed
the whole decode → HTTP 500. The synthetic golden used `true`/`false`, so offline tests
passed. Fixed to `int64`; golden + filter test now use `0/1` (#46).

**Grab gap found — non-URL-authenticated downloads `[Resolved]`.** This is a
real functional gap, not a quirk. harbrr serves a **bare direct download
link** for any non-resolver tracker, assuming the URL **self-authenticates** (carries
a passkey/rsskey in the path/query). That holds for seedpool
(`…/torrent/download/{id}.{rsskey}`) and grabs fine. But it breaks for trackers that
authenticate the *download* out-of-band:

- **digitalcore** — link is `…/api/v1/torrents/download/2491129` with **no token**; auth
  is the `X-API-KEY` **header**. A bare GET (by *arr or the harness) → **401**.
- **torrentleech** — link is `…/download/241785226/….torrent` with **no token**; auth is
  the **session cookie** (CF-cleared). A bare GET → a login/CF page, not a `.torrent`.

Search works for both (count parity 1.00); only the grab fails. Jackett/Prowlarr never
hit this — their download always goes through the indexer's authenticated HTTP client
(cookies + headers from login). harbrr only routes a download through its `/dl` proxy
when the def has a `download:` block (`NeedsResolver()`); a plain login-auth tracker
falls through to the bare-link path. **Impact:** harbrr is effectively search-only (no
grab) for **session/cookie-auth and header-auth trackers** — a meaningful slice of
private trackers (essentially every cookie-login tracker, plus header-auth UNIT3D like
DigitalCore), not two oddballs. **Fix direction:** route a login-requiring tracker's
download through `/dl` (resolve server-side with harbrr's authenticated session), not
just `download:`-block defs — scoped fix PR, like the nil-`Transport` panic above.

**Fix — shipped, offline-proven.** The serializer now routes a link
through `/dl` when the def has a **login block** (`DownloadNeedsAuth()`), not only a
`download:` block (`NeedsResolver()`); the `/dl` grab then fetches the `.torrent`
server-side with the session cookie (torrentleech) and the search-header auth
(digitalcore's `X-API-KEY`, applied via the `renderDownloadHeaders` search-headers
fallback). Covered by offline tests (engine predicate, `search.Grab` with header- and
cookie-auth + no download block, and Torznab/JSON routing+redaction). **Live-confirmed
2026-06-18:** torrentleech (cookie) and digitalcore (X-API-KEY) both resolve a real
bencoded `.torrent` through `/dl` — see the run above. `[Resolved]`.

**Coverage gap found — native (non-Cardigann) trackers `[Resolved]`** (IPTorrents/
FileList live-confirmed; MAM driver built, live pending a session). harbrr shipped the Cardigann corpus
+ the AvistaZ native driver only; **IPTorrents, MyAnonamouse, FileList** (one-off C# native
indexers in Jackett/Prowlarr, ≈3 of ~18 trackers here) had no def. Per-tracker
native drivers were added on the AvistaZ pattern (#45): **IPTorrents** (count parity 1.00 + grab,
live-confirmed 2026-06-18); **FileList** (search live-confirmed after the int-flags fix #46;
Prowlarr differential pending a name match); **MyAnonamouse** (driver + `mam_id`
write-back seam #46 — correct, but live search/parse pending a fresh dedicated session). This corrected
the earlier "AvistaZ is the only native gap" measurement (now superseded — see
`docs/native-indexer-pattern.md` and the `native-driver` issues).

**Still `[Tracked]` — no qualifying tracker in this stack:**

- **cookie / 2FA** — the cookie trackers present (IPTorrents, MyAnonamouse) are native
  (above); none of the Cardigann-supported trackers here use cookie login. harbrr's
  `ManualCookieSolver` is offline-proven; needs a supported cookie tracker + session
  cookie to confirm live.
- **.NET-quirk** (`*()'!` / unicode / `regexp2`) — all configured defs are Latin-script
  and queries are plain, so the .NET URL encoder / regexp2 routing wasn't stressed.
- **per-indexer proxy (HTTP/SOCKS5)** — only a FlareSolverr proxy is configured in
  Prowlarr; no HTTP/SOCKS proxy to route a real search through.
