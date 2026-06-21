# Native indexer pattern — porting Prowlarr/Jackett C# indexers to harbrr

Some trackers are **not** Cardigann YAML in Jackett/Prowlarr — they're bespoke C#
indexers, so they never appear in the vendored `Definitions/` tree and harbrr
cannot serve them from the corpus. They need **native drivers** (the AvistaZ
pattern in `internal/indexer/native/`). This doc records the implementation
pattern those drivers follow, derived from the Prowlarr/Jackett source. It feeds
the native-driver work; per-tracker divergences live beside each
driver's fixtures, not here.

In the user's stack the missing natives are **IPTorrents**, **MyAnonamouse**, and
**FileList**. A full inventory of what else is native-only is the coverage-matrix
backlog item in [`plan.md`](plan.md).

## The shared shape (Prowlarr — follow this, not Jackett's monolith)

Every Prowlarr native indexer is a `TorrentIndexerBase<TSettings>` subclass that
returns a **request generator** + a **parser**, with a `TSettings` POCO and a
category map. harbrr's `native.Driver` (AvistaZ:
`AvistazRequestGenerator`/`AvistazParserBase`) already mirrors this split — reuse
it. Jackett collapses the same logic into one `IndexerBase` file; prefer
Prowlarr's split as the reference because it's cleaner to port and (on the points
below) more correct.

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

## Why autobrr isn't in this picture

autobrr covers a **different surface** of the same trackers — the IRC **announce**
firehose (push, latency-optimized), parsed by regex, download link rebuilt from a
passkey/cookie. It does **not** do on-demand search (even when it consumes a
Torznab endpoint, it polls it RSS-style, never `t=search`). harbrr/Prowlarr/Jackett
own the **search** surface; autobrr owns **announce**. They are complementary, not
substitutes — which is the framing for the coverage matrix in `plan.md`.
