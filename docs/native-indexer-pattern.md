# Native indexer pattern — porting Prowlarr/Jackett C# indexers to harbrr

Some trackers are **not** Cardigann YAML in Jackett/Prowlarr — they're bespoke C#
indexers, so they never appear in the vendored `Definitions/` tree and harbrr
cannot serve them from the corpus. They need **native drivers** (the AvistaZ
pattern in `internal/indexer/native/`). This doc records the implementation
pattern those drivers follow, derived from the Prowlarr/Jackett source. It feeds
the native-driver work; per-tracker divergences live beside each
driver's fixtures, not here.

**IPTorrents**, **MyAnonamouse**, and **FileList** — the natives missing from the user's
stack when this pattern was first written — are now **shipped**, and are the worked examples
below. The remaining native-only trackers are tracked as demand-gated issues under the
[`native-driver`](https://github.com/autobrr/harbrr/labels/native-driver) label.

## Hard rule: newznab is usenet-only — a torrent tracker never gets a newznab preset

`internal/indexer/native/newznab` and `internal/indexer/native/torznab` speak the
**same wire format** (Newznab/Torznab RSS+XML, `torznab:attr`/`newznab:attr` are
interchangeable on the wire) but codify **different protocol semantics**, and that
difference is why they are two packages, not one with a flag:

- **Acquisition protocol.** newznab is `Protocol=usenet` (no seeders/leechers/peers, no
  DVF/UVF, a bare `.nzb` grab); torznab is `Protocol=torrent` (seeders/peers/DVF/UVF are
  real, meaningful fields on `normalizer.Release`, mapped the way the gazelle/gazellegames
  torrent drivers do it, not left zero).
- **Download-link shape and grab posture.** A Newznab `.nzb` link carries only the
  apikey; harbrr fetches it server-side and proxies the bytes (`NeedsResolver()=false`,
  `DownloadNeedsAuth()=true`). A Torznab tracker's download link is Gazelle-style
  URL-credentialed (`authkey`+`torrent_pass` riding the query, per the real MoreThanTV
  capture) — a fundamentally different secret shape requiring `NeedsResolver()=true`
  like FileList/GazelleGames, not the newznab pattern.
- **A torrent tracker that happens to expose Torznab is NEVER served by the newznab
  driver**, even though the newznab driver's XML parser would technically decode the
  feed without error. Doing so would silently drop every seeders/DVF/UVF field (the
  usenet driver zeroes them by design) and would proxy the download as if it were an
  apikey-only link, when the real secret is the URL-credentialed torrent link — a
  correctness AND a redaction bug, not a cosmetic one. Route it to the torznab family
  instead, adding a preset there (or the torznab generic entry, once one exists).

## Deciding where a new tracker lands (the rubric)

Given a new tracker to support, in order:

1. **A Cardigann definition exists** (vendored or drop-in) → it belongs to the **engine**
   (`internal/indexer/cardigann/`), not a native driver. This is always the first check —
   the engine is the standing parity gate and covers the overwhelming majority of
   trackers for free.
2. **No definition, but the tracker exposes a native Newznab or Torznab API** → a preset
   on the **matching-protocol family** (`native/newznab` for `Protocol=usenet`,
   `native/torznab` for `Protocol=torrent` — per the hard rule above, protocol decides
   the family, never the wire format alone).
3. **No definition, and a proprietary (non-Newznab/Torznab) API** → a **bespoke native
   driver** (the AvistaZ/Gazelle/FileList/GazelleGames/IPTorrents/MyAnonamouse shape
   below), or join an existing family if another tracker already proved the same API
   *shape* (see "Leverage & effort").

## The shared shape (Prowlarr — follow this, not Jackett's monolith)

Every Prowlarr native indexer is a `TorrentIndexerBase<TSettings>` subclass that
returns a **request generator** + a **parser**, with a `TSettings` POCO and a
category map. harbrr's `native.Driver` (AvistaZ:
`AvistazRequestGenerator`/`AvistazParserBase`) already mirrors this split — reuse
it. Jackett collapses the same logic into one `IndexerBase` file; prefer
Prowlarr's split as the reference because it's cleaner to port and (on the points
below) more correct.

### The driver base (`native.Base`) — embed it, don't re-scaffold

harbrr's side of that split is the **driver base**: every driver embeds
`native.Base` (`internal/indexer/native/base.go`) and gets the instance wiring
(`NewBase`: nil-def guard, capabilities build, base-URL normalisation, clock),
the transport (`Do`/`DoDownload`: paced doer, host-only transport-error
redaction, capped body reads — `DoDownload` errors with
`native.ErrDownloadTooLarge` instead of truncating a torrent), and status
classification. A new driver writes only its request generator (which injects
its own auth — header, cookie, or body-embedded) and its response parser.

Classification is a **required per-call parameter** — the endpoint's
**classification dialect** — so it can never be forgotten: `ClassifyAuth403`
(the majority: 401/403 = auth failure), `ClassifyRateLimit403` (HDBits/newznab:
403 = spent rate budget), `ClassifyAuthOnly403` (MAM: 403 = expired session, no
401), extended per endpoint with `AlsoAuth`/`AlsoRateLimited`/`WithAuthReason`
(AvistaZ: 412 on search, 422 on login). `Do` returns the header shell alongside
a classified-status error, so a rotation riding a 403/429 (MAM's `mam_id`) is
never lost. `TestViaSearch` is the default credential probe. hdbits (body
creds), gazelle (header auth), and myanonamouse (cookie + rotation) are the
reference adopters.

**Universal across these trackers:** none returns an infohash (the download is
always an authenticated/tokenized `.torrent` URL); freeleech is a download-volume
flag; tracker categories map to Torznab/Newznab ids. Build the download URL
**explicitly** (Prowlarr's approach) rather than trusting an API-returned link
(Jackett's) — deterministic and immune to a redacted field.

## Two auth shapes cover all three

The axis that matters for a Go driver is **how the download authenticates**,
because that's the same axis as the grab-auth gap (`/dl`). Build the
authenticated-`/dl` grab path first; these drivers reuse it.

### Shape A — session cookie
| Tracker | Credential | Sent as | Search surface | Download |
|---|---|---|---|---|
| **IPTorrents** | full `Cookie` string + User-Agent | `Cookie` + `User-Agent` headers | `GET /t` — `q=+(term)`, repeated `cat=`, `free=on`, `p=` page; **HTML scrape** (`table#torrents tr`, columns resolved by header text, relative "time ago" dates, `a[href^="/download.php/"]`) | scrape the href, fetch over the same cookie session |
| **MyAnonamouse** | `mam_id` session value | `Cookie: mam_id=…` | `GET tor/js/loadSearchJSONbasic.php`, `Accept: application/json`, `tor[text]`, `tor[srchIn][…]`, `tor[cat][n]`, `tor[perpage]=100`, `tor[startNumber]` offset; **JSON** | `tor/download.php/{dl}?tid={id}` over the cookie |

**MAM gotcha — cookie rotation.** MAM rotates `mam_id` on *every* response. A
correct driver must capture `Set-Cookie` and persist the new `mam_id` (Prowlarr:
30-day expiry; `403` ⇒ "mam_id expired"). Jackett does **not** do this and is the
weaker reference. MAM data quirks: `Size` is a human string (parse to bytes);
`author_info` is a stringified (sometimes malformed) JSON dict — parse
defensively.

### Shape B — passkey / HTTP Basic
| Tracker | Credential | Sent as | Search surface | Download |
|---|---|---|---|---|
| **FileList** | `username` + `passkey` | `Authorization: Basic base64(user:passkey)` | `GET /api.php?action=search-torrents&type=imdb\|name&query=…&category=…&freeleech=1`; **JSON array**, no pagination | rebuild `/download.php?id={id}&passkey={passkey}` (Prowlarr) — don't trust the API `download_link` |

## Build & validation

- **Offline-gated like AvistaZ**: stub auth/search server + synthetic goldens
  derived from the documented contract (never a live capture).
- **Live gate**: the Prowlarr differential — the stack runs all three live, so
  the live Prowlarr feed is the oracle (same query → Prowlarr vs harbrr → diff),
  exactly as the live differential does for the Cardigann corpus.
- **Redaction (non-negotiable)**: `mam_id`, `passkey`, `Cookie`, `Authorization`
  redacted in every log/trace.

## Leverage & effort (how the backlog is sized)

A native driver covers **one API *shape*, not one site.** Trackers running the same software
behind different hostnames share a single driver (AvistaZ is one driver serving
AvistaZ/CinemaZ/PrivateHD/ExoticaZ; the Gazelle base serves Redacted + Orpheus). So the backlog
splits into **families** (one build, many trackers) and **one-offs** (one build, one tracker) —
and each new driver reuses the shared seams (`native.Driver` + registry, paced client, encrypted
secrets, normalized release, category mapper, the authenticated `/dl` path), so it's only three
pieces: a settings struct, a request generator, and a response parser.

**Effort** (measured on the first four shipped drivers): each is ~1.5–2.1k source LOC + ~0.8–1.1k
test LOC — offline-gated (stub API server + synthetic goldens from the documented Prowlarr/autobrr
contract, never a live capture), then live-validated against a Prowlarr differential + a real
grab. A family base lands at the top of that range but amortizes over every site it covers.

Build order is **demand-gated**: the remaining native-only trackers are per-tracker issues under
the [`native-driver`](https://github.com/autobrr/harbrr/labels/native-driver) label, prioritized
by votes.

## Why autobrr isn't in this picture

autobrr covers a **different surface** of the same trackers — the IRC **announce**
firehose (push, latency-optimized), parsed by regex, download link rebuilt from a
passkey/cookie. It does **not** do on-demand search (even when it consumes a
Torznab endpoint, it polls it RSS-style, never `t=search`). harbrr/Prowlarr/Jackett
own the **search** surface; autobrr owns **announce**. They are complementary, not
substitutes — which is the framing for the coverage matrix in `../website/docs/coverage.md`.
