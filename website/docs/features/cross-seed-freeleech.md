# Cross-seed & freeleech-aware matching

harbrr makes one tracker serve **both** ratio-building (your \*arrs) **and** cross-seed off
a single configuration. It is the smart *source* and *messenger* for cross-seed — harbrr
never matches; the cross-seed tools (qui cross-seed, cross-seed v6) do that.

Two ideas work together:

- a **per-indexer freeleech toggle** so your \*arrs can be fed cheap-ratio (freeleech)
  releases, and
- a **freeleech-bypass feed** so cross-seed still sees the *full* catalog —

both configured in harbrr, with **no changes on the downstream apps**.

## Per-indexer freeleech toggle

On a tracker that exposes a **Filter freeleech only** setting, turn it on in the indexer's
settings. harbrr then serves the standard feed (`…/results/torznab`) as **freeleech-only**.

Under the hood harbrr always fetches the **full** catalog from the tracker once, caches it,
and applies the freeleech filter *on the way out* (using each release's
`downloadVolumeFactor`). So enabling freeleech never costs an extra tracker request, and
the full catalog is still available to the bypass feed below — from the same single fetch.

## Freeleech-bypass feed (`/full`)

Every indexer also exposes a second feed surface:

```text
…/api/indexers/<slug>/results/torznab        ← honors the freeleech setting (*arrs)
…/api/indexers/<slug>/results/torznab/full   ← full catalog (cross-seed)
```

The `/full` variant ignores the freeleech filter and returns everything — cross-seed must
see every release to find matches. Both URLs are answered from the **same cached fetch**,
so polling `/full` after an \*arr polled the honor feed adds **no** tracker request.

## Per-app routing (set once in harbrr)

App-sync connections carry a **freeleech mode**, defaulted by app kind:

| App | Default | Feed pushed |
|-----|---------|-------------|
| Sonarr / Radarr / Lidarr / Readarr / Whisparr | `honor` | `…/results/torznab` |
| qui | `bypass` | `…/results/torznab/full` |

When harbrr syncs a connection it pushes the matching feed URL automatically — the \*arr
gets the freeleech-honoring feed, qui gets the full catalog. **Nothing changes on the app
side**; it just stores whichever URL harbrr handed it. You can override the mode per
connection (the `freeleechMode` field on an app connection).

> qui uses a single shared Torznab pool for *both* cross-seed and manual search, so qui
> gets the bypass feed for both — there is no per-feature switch on qui's side.

## cross-seed v6 config snippet

cross-seed v6 has no indexer API (it reads a `config.js` file and restarts), so harbrr
emits a copy-paste snippet instead of pushing. For each indexer:

```text
GET /api/indexers/<slug>/crossseed-snippet
```

returns the bypass `/full` feed URL plus a ready `torznab:` entry for `config.js`. Replace
the `<YOUR_HARBRR_API_KEY>` placeholder with one of your minted harbrr API keys (harbrr
stores keys hashed and cannot print a usable one for you).

## Announce push (qui + cross-seed v6)

Instead of cross-seed *polling* harbrr, harbrr can **push** newly-seen releases to it.
Configure an **announce target** (`/api/announce-connections`):

- **qui** (two-step): harbrr calls qui's `webhook/check`; only on a `download`
  recommendation does it fetch the `.torrent` (via its own `/dl`, holding the tracker
  creds) and `apply` it. No match → one cheap request, no tracker contact.
- **cross-seed v6** (one-step): harbrr `POST`s `/api/announce` with a `/dl` link; cross-seed
  fetches the link itself if it injects.

The "what's new" stream is derived by tapping harbrr's **existing RSS cache fills** — it
announces only what a consumer already polls, so it adds **zero** tracker load. A `.torrent`
is fetched only on a confirmed match, which is strictly less load than a consumer
polling + grabbing.

!!! note "cross-seed v6 reachability"
    The link harbrr hands cross-seed v6 is built from the connection's **harbrr URL**, so
    set it to an address cross-seed can actually reach (e.g. harbrr's LAN/container host),
    not `127.0.0.1` if cross-seed runs elsewhere.
