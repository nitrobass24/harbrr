# Failing-tracker circuit breaker

When a tracker goes down or tells you to slow down, the worst thing a fleet of apps can
do is keep hammering it. harbrr's circuit breaker makes sure that doesn't happen.

If a search to a tracker **fails**, harbrr remembers that failure for a short window. For
that window, the *next* search to the same tracker is answered with the failure
immediately — **without sending anything to the tracker**. One app's bad luck shields the
tracker from everybody else's retries.

This is the other half of being [kind to trackers](search-results-cache.md): the cache
spares trackers from duplicate *successful* polls; the breaker spares them from a pile-on
when they're already struggling.

The breaker is **on by default** (a 1-minute window). Most people never need to touch it.

---

## Why you want this

A private tracker having a rough moment — a maintenance window, a traffic spike, an
anti-abuse rate limit — is exactly when a wall of automated retries does the most damage.
And a typical autobrr-family setup is a wall of retries waiting to happen: several apps,
sometimes several *instances* of each, all pointed at the same tracker, all polling on
their own timers.

Without a breaker, every one of those apps independently discovers the tracker is down,
and every one of them keeps trying. With the breaker, the **first** failure trips it, and
every other app's request for the next minute is answered from memory instead of piling
onto a tracker that's already having a bad day.

When the tracker explicitly asks you to back off (an HTTP **429 / 503** with a
`Retry-After`), harbrr honors it: the breaker stays open for **at least** as long as the
tracker asked — across *all* your apps, not just the one that got the 429.

---

## How it works (in plain English)

- **A failure opens the breaker** for that one tracker, for a short window
  (`negative_ttl`, default **1 minute**). A rate-limit response (`429`/`503`) keeps it open
  for at least its `Retry-After`.
- **While open, new searches short-circuit.** A search to that tracker returns the recorded
  error right away. The tracker is not contacted.
- **Cached answers still flow.** The breaker only affects searches that *would have gone to
  the tracker*. If harbrr already has a fresh cached answer for what you're asking, you get
  it — an open breaker never blanks out good cached results.
- **It heals itself.** The first search after the window expires goes out live. If the
  tracker has recovered, everything resumes; if not, that one failure re-opens the breaker.
  There's nothing to reset.
- **You can force a live retry** any time with `nocache=1` on a search (the same
  [bypass](search-results-cache.md#per-request-bypass-nocache1) the cache honors), or by
  hitting the indexer's **Test** action — neither is blocked by an open breaker.

:::note[Per tracker, not per query]

The breaker opens for a whole **tracker** (indexer instance), because a down tracker is
down for every search, not just the one that failed. A caller that *cancels* its own
request never trips the breaker — that's your app giving up, not the tracker failing.

:::

---

## Seeing it work

The breaker shares the cache's stats endpoint,
[`GET /api/cache/stats`](search-results-cache.md#get-apicachestats):

- **`breakerSuppressed`** — how many searches the breaker short-circuited (extra requests
  your trackers were spared), process-wide.
- **`byIndexer[].breakerOpenUntil`** — for each tracker, the Unix time the breaker will
  reopen it to live traffic, or `null` when it's healthy. A non-null value is harbrr telling
  you "this tracker is failing right now, and I'm holding off."

---

## Tuning

The breaker has one knob, `negative_ttl`, alongside the other
[cache settings](search-results-cache.md#the-tuning-knobs). It's a Go duration string and
is **runtime-tunable** through the management API — no restart.

```yaml
cache:
  negative_ttl: "1m"   # default; how long a failed tracker is left alone. "0s" disables.
```

Change it live:

```http
PUT /api/cache/config
Content-Type: application/json

{ "negativeTtl": "2m" }
```

- **Longer** (e.g. `5m`) — gentler on a flaky tracker, but you'll wait longer to notice it
  has recovered.
- **Shorter** (e.g. `30s`) — probes for recovery sooner, at the cost of more retries against
  a tracker that's still down.
- **`"0s"`** — disables the breaker entirely (every search re-drives the tracker, even when
  it's failing). Disabling takes effect immediately, even on an already-open window.

---

## FAQ

**Does the breaker ever hide a tracker that's actually fine?**
No. It only opens *after* a real failure, for a short self-healing window, and a fresh
cached answer is always served regardless. The first request after the window probes the
tracker live.

**A tracker I just fixed is still erroring for a minute — why?**
The breaker is riding out its window from the last failure. Either wait it out (≤ the
`negative_ttl`, default 1 minute), hit the indexer's **Test** action, or send one search
with `nocache=1` to probe immediately.

**Is the backoff fleet-wide, or per app?**
Fleet-wide. Because harbrr is the search *server* for your whole fleet, it holds one
tracker-wide backoff that protects every app behind it, where a per-app tool only sees (and
backs off) its own traffic.
