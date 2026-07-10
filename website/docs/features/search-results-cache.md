# Search-results cache

harbrr remembers the answers to recent searches. When Sonarr, Radarr, or Prowlarr asks
your tracker the **same question** it asked a moment ago, harbrr replies from memory
instead of bothering the tracker again.

The reason this helps the tracker, and not just you, is simple: **harbrr is the search
*server***. A cache hit in harbrr means the request **never reaches the tracker at all** —
so you're sparing the tracker's infrastructure, not just your own. (A cache that lives inside
a client-side app still forwards the request on to the tracker; harbrr sits where it can stop
it.)

The cache is **on by default** with conservative settings, so most people never need to
touch it. The rest of this page explains what it does and how to tune it if you want to.

---

## Why you want this

Two everyday patterns hammer trackers with duplicate work. The cache is built to absorb
both.

### 1. The same poll, over and over

Sonarr/Radarr/Prowlarr "RSS sync" re-asks your trackers *"what's new?"* every few minutes,
forever. That request is **identical** every time. If you run more than one app pointed at
the same tracker, each one issues its own copy of it.

It gets worse with **multiple instances of the same app** — a very common setup where you
run one Sonarr for 1080p and another for 4K. Both poll the same tracker for the same
shows. Because the resolution you want is decided *inside the app* (by your quality
profile), the request that actually leaves for the tracker is **byte-for-byte the same**
from both instances.

harbrr collapses all of these into **one** request per tracker. The 1080p instance and the
4K instance share a single cached answer and each filters it down to what it wants. No
configuration needed — this works the moment the cache is on.

### 2. Staggered releases

On TV especially, a release shows up in stages: 720p first, then 1080p, then 4K a few
minutes later. Apps poll impatiently during that window hoping to catch the next quality,
which is exactly when the tracker gets pounded.

harbrr handles this with a shorter memory for "thin" results (see
[the thin-result rule](#the-thin-result-rule-staggered-releases) below): when a search
comes back with only a few results, harbrr re-checks sooner, so the 1080p and 4K versions
are picked up quickly — while still collapsing the duplicate polling in between.

---

## How it works (in plain English)

- **One answer, shared by everyone.** A cached result is stored once and served to every
  app that asks the same question. Your download links are still sealed per-request on the
  way out, so sharing the cache never leaks your passkey.
- **Identical requests collapse.** If three apps ask the same thing at the same instant,
  harbrr sends **one** request to the tracker and gives all three the same answer.
- **Answers expire.** Every cached answer has a time-to-live (TTL). After it expires, the
  next search goes back out to the tracker and refreshes the memory.
- **Different questions are remembered separately.** A search for *Dune* and a search for
  *Halt and Catch Fire* are different entries. So is an RSS poll vs. a real keyword search.
- **Only good answers are remembered.** Successful searches are cached — including a
  legitimate "0 results". Errors and timeouts are **never** cached, so a tracker hiccup
  never gets "stuck".
- **Refreshed before it goes stale.** When a popular entry is near the end of its life,
  harbrr serves the cached copy *instantly* and refreshes it in the background. Your apps
  never wait, and the tracker still only sees about one refresh per TTL no matter how many
  apps are polling.

---

## The tuning knobs

There are three places to control caching: **global settings**, a **per-indexer override**,
and a **per-request bypass**.

### Global settings (config file)

These live under `[cache]` in your `config.toml`. The values below are the defaults — you
only need to add the keys you want to change.

!!! tip "All of these are tunable at runtime — no restart"
    Every knob below is also editable live via `PUT /api/cache/config` and takes effect
    immediately (a `cleanup_interval` change applies on the next cleanup cycle). The config
    file is just the seed; a value set through the API overrides it and persists across
    restarts. `GET /api/cache/config` shows the live configuration.

```yaml
cache:
  enabled: true          # master switch; set false to turn caching off entirely
  rss_ttl: 5m            # how long an RSS / "what's new?" poll is remembered
  keyword_ttl: 30m       # how long a real keyword/ID search is remembered
  thin_ttl: 2m           # shorter memory when a search returns only a few results
  thin_threshold: 5      # "a few" = this many results or fewer
  refresh_ahead_pct: 80  # refresh-in-background once this % of the TTL has elapsed
  cleanup_interval: 1h   # how often expired entries are tidied up
  negative_ttl: 1m       # how long a FAILED tracker is left alone (the circuit breaker); "0s" disables
```

What each one is for:

| Setting | What it controls | When to change it |
|---|---|---|
| `enabled` | Turns the whole cache on or off. Off behaves exactly like older harbrr. | Disable only for debugging or if you specifically don't want caching. |
| `rss_ttl` | Freshness of the constant "what's new?" polling. | Lower it if you rely on RSS to catch releases fast; raise it to spare trackers more. |
| `keyword_ttl` | Freshness of specific searches (by name or by ID). | Usually fine. Lower if manual searches feel stale; raise to cache harder. |
| `thin_ttl` | A shorter memory for searches that came back nearly empty — the staggered-release catcher. | Lower it (e.g. `1m`) if you want even faster pickup of late-arriving qualities. |
| `thin_threshold` | What counts as "nearly empty". | Raise it if your trackers return small result sets normally. |
| `refresh_ahead_pct` | How early the background refresh kicks in. `80` = refresh during the last 20% of an entry's life. | Rarely changed. |
| `cleanup_interval` | Housekeeping cadence for reaping expired rows (the ticker re-reads it live). | Rarely changed. |
| `negative_ttl` | How long a tracker that just **failed** is left alone — the [circuit breaker](circuit-breaker.md). | Raise to be gentler on flaky trackers; `0s` disables. |

#### The two TTL tiers, explained

harbrr picks a TTL based on the *kind* of search:

- An **RSS poll** ("what's new?", no search terms) uses **`rss_ttl`** (default 5 minutes).
  These are the highest-volume, most-duplicated requests, and the collapse-and-share
  behavior already removes most of the load — so the TTL is kept short to stay fresh.
- A **real search** (a show name, or an IMDb/TVDB/TMDb ID) uses **`keyword_ttl`** (default
  30 minutes). The results for a specific title barely change hour to hour, so harbrr can
  remember them longer.

#### The thin-result rule (staggered releases)

On top of the tier above, if a search returns **`thin_threshold` results or fewer**,
harbrr uses the shorter **`thin_ttl`** instead. This is the staggered-release antidote:
when only the 720p exists, the result is "thin", so harbrr re-checks within `thin_ttl`
(2 minutes) and catches the 1080p/4K as they drop.

!!! note "The thin rule can only *shorten*, never lengthen"
    If you set a long TTL (globally or per-indexer), a thin result is still capped at
    `thin_ttl`. You can't accidentally configure harbrr to sit on a half-empty result for
    an hour and miss the later qualities.

### Per-indexer override

Different trackers warrant different policies, and the natural unit is the tracker itself.
Add a **`cache_ttl`** setting to an individual indexer to override its base TTL (both the
RSS and keyword tiers) with a single value — without changing anything globally.

`cache_ttl` is a duration string like `10m`, `1h`, or `45s`. An invalid, zero, or negative
value is ignored and the global defaults apply. The thin-result rule still applies on top,
so it can only ever shorten this for nearly-empty searches.

Two typical reasons to use it:

- **A tracker you reach only via RSS** (no autobrr/announce coverage): keep its TTL tight
  (e.g. `cache_ttl: 2m`) so new releases surface quickly.
- **A fragile tracker that times out under load**: cache it harder (e.g. `cache_ttl: 1h`)
  to protect it — more caching here is purely protective.

### Per-request bypass: `nocache=1`

Add **`nocache=1`** to a search URL to skip the cache for that one request: harbrr fetches
live from the tracker and refreshes the stored answer. This is the "I *know* something just
dropped, check right now" override for a manual search. Everyday app traffic doesn't send
it, so it never interferes with normal caching.

A client that speaks HTTP caching can do the same with a **`Cache-Control: no-cache`** (or
`Pragma: no-cache`) request header — the header equivalent of `nocache=1`. Both force a live
fetch and skip the conditional-GET shortcut below.

### Conditional requests: `ETag` / `If-None-Match`

harbrr speaks standard HTTP cache validation on the feed. A cache-backed results feed comes
back with two headers:

```http
ETag: "9f8c…"                       # a fingerprint of the result set
Cache-Control: private, max-age=240 # still fresh for 240s
```

A client that keeps the `ETag` can send it back on the next poll as `If-None-Match`. If the
results haven't changed, harbrr answers **`304 Not Modified`** with **no body at all** — even
cheaper than serving the cached copy, and the tracker is never touched:

```http
GET /api/indexers/<slug>/results/torznab?... HTTP/1.1
If-None-Match: "9f8c…"

HTTP/1.1 304 Not Modified
ETag: "9f8c…"
```

The `ETag` is a fingerprint of the **results**, so it only changes when the results actually
change — a refresh that returns the same releases keeps the same `ETag`. This is opt-in on the
client side: tools like autobrr can adopt it to poll harbrr almost for free, while clients that
don't send `If-None-Match` simply get the normal cached feed.

---

## Seeing the cache work

harbrr exposes the cache through its management API.

### `GET /api/cache/stats`

```json
{
  "enabled": true,
  "entries": 1423,
  "totalHits": 50211,
  "trackerHitsSaved": 50211,
  "breakerSuppressed": 37,
  "hitRatio": 0.86,
  "approxSizeBytes": 9123840,
  "oldestCachedAt": 1750680000,
  "newestCachedAt": 1750683600,
  "lastUsedAt": 1750683715,
  "byIndexer": [
    {
      "instanceId": 4,
      "slug": "redacted-tracker",
      "name": "My Tracker",
      "entries": 612,
      "hitsSaved": 21984,
      "hits": 1840,
      "misses": 210,
      "hitRatio": 0.9,
      "approxSizeBytes": 4011200,
      "breakerSuppressed": 12,
      "breakerOpenUntil": null
    }
  ]
}
```

| Field | Meaning |
|---|---|
| `enabled` | Whether caching is on. If `false`, the rest is omitted. |
| `entries` | How many distinct cached answers are currently stored. |
| `totalHits` | How many searches have been served from cache. **This is your tracker-load saved.** |
| `trackerHitsSaved` | The same number, named for the value story: tracker requests harbrr answered from cache instead of going out. Survives restarts. |
| `breakerSuppressed` | How many searches were short-circuited by the [failing-tracker circuit breaker](circuit-breaker.md) — extra requests a struggling tracker was spared. |
| `hitRatio` | Fraction of searches served from cache since harbrr last started. The headline number — "86% of searches never touched a tracker." |
| `approxSizeBytes` | Roughly how much space the cache occupies. |
| `oldestCachedAt` / `newestCachedAt` / `lastUsedAt` | Unix timestamps (seconds) for the oldest/newest stored entry and the most recent hit. |
| `byIndexer` | The same figures broken down **per indexer**, plus `breakerOpenUntil` (the Unix time the breaker reopens that tracker, or `null` when it's healthy). |

!!! info "`hitRatio` resets on restart"
    The ratio (and the hits/misses behind it) is counted in memory for the life of the
    process, so it starts fresh each time harbrr restarts. The stored entries themselves
    survive a restart — harbrr won't re-poll every tracker just because it was bounced.

### `POST /api/cache/flush`

Empties the cache and tells you how many entries it removed:

```json
{ "flushed": 1423 }
```

Useful if you've changed something and want a clean slate. You rarely need it — entries
expire on their own, and harbrr already clears a tracker's cached answers automatically
when you edit, disable, or delete that indexer.

---

## When the cache clears itself

You don't have to manage this; harbrr handles it:

- **Expiry** — every entry clears itself after its TTL.
- **Indexer changed** — editing or disabling an indexer drops its cached answers (its
  settings may change what gets searched).
- **Indexer deleted** — its cached answers go with it.
- **Housekeeping** — expired rows are tidied up on the `cleanup_interval`.

---

## Tuning recipes

=== "I rely on RSS to catch releases"

    You don't run autobrr/announce for some trackers, so RSS *is* how you grab things.
    Keep RSS fresh:

    ```yaml
    cache:
      rss_ttl: 2m
      thin_ttl: 1m
    ```

    Or, if it's only certain trackers, leave the globals alone and set `cache_ttl: 2m`
    on just those indexers.

=== "autobrr does the fast grabbing"

    Announce handles timeliness; RSS is just a backstop. You can cache harder to spare
    trackers:

    ```yaml
    cache:
      rss_ttl: 15m
      keyword_ttl: 1h
    ```

=== "I run 1080p + 4K instances"

    Nothing to do — this is handled automatically. Both instances issue identical
    requests, so they share one cached answer and one tracker request. Check
    `GET /api/cache/stats` and watch `hitRatio` climb.

=== "A tracker keeps timing out"

    Protect it by caching it harder, without affecting your other trackers — set
    `cache_ttl: 1h` (or higher) on that one indexer.

---

## FAQ

**Will the cache make me miss releases?**
For RSS, the worst case is a delay of up to `rss_ttl` (default 5 minutes) before a new
release shows up — and the thin-result rule shortens that to `thin_ttl` while a release is
mid-staggered-drop. If you grab via autobrr/announce, that path is instant and unaffected.
If you rely on RSS and want it tighter, lower `rss_ttl` (see the recipes above).

**Could two apps get each other's results?**
No. Apps only share a cached answer when they asked the **same** question. Your download
links are sealed per-request on the way out, so a shared cache never exposes your passkey
to anyone.

**Is my passkey stored in the cache?**
A cached result can contain a download link with your passkey in it, the same as a live
result would. It's kept in harbrr's local database, which is created with locked-down,
owner-only file permissions, and the cache is never written to logs. (Cached results
aren't separately encrypted the way your stored tracker *logins* are — they rely on those
database file permissions, the same posture harbrr uses for session cookies.)

**Does a restart re-poll all my trackers?**
No. Cached answers are stored on disk and survive restarts, so harbrr doesn't stampede
your trackers when it comes back up. (Only the in-memory `hitRatio` counter resets.)

**How do I turn it off?**
Set `enabled = false` under `[cache]` in `config.toml`. harbrr then behaves exactly as it did before
the cache existed.
