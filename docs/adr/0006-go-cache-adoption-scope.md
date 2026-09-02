# go-cache is adopted for one call site only: memoizing regex compilation

`github.com/autobrr/go-cache` (#563) ships three packages — `ttlcache`, `timecache`, and
`regexcache`. harbrr adopts **`ttlcache`, in exactly one place**: memoizing
`regexadapter.Compile`. The other two packages, and every other caching site in harbrr, were
evaluated and rejected. This ADR records the evaluation so the question does not get reopened
each time the library gains a release.

## The one place it earns its keep

`search/fields.go` applies a field's filters inside the per-row loop, so
`regexadapter.Compile` runs once per **row** per **field** — a definition with N regex filters
recompiles the same N patterns on every row of a result page. Across the vendored corpus, 335
definitions use `re_replace`/`regexp`, a mean of 5.6 regex filters each and a maximum of 57
(`mazepa.yml`). Compilation measured at ~6.1µs / 6.2KB / 67 allocs (RE2) and ~6.8µs / 6.3KB /
103 allocs (regexp2), so an ordinary hundred-row search burns several MB of pure churn
recompiling def-authored constants. Memoized:

| | before | after |
|---|---|---|
| RE2 | 6137 ns, 6196 B, 67 allocs | 213 ns, 0 B, 0 allocs |
| regexp2 | 6758 ns, 6312 B, 103 allocs | 61 ns, 0 B, 0 allocs |

This is **allocation pressure, not latency**: a few ms against a tracker round-trip of hundreds.
It is worth a dependency because the dependency is nearly free (see below), not because the
search path was slow.

The memo is safe because a compiled pattern is a pure function of (normalized pattern, engine
choice): `regexp.Regexp` and `regexp2.Regexp` both document their compiled form as safe for
concurrent use, and nothing mutates a `*Regexp` after `compileRegexp2` sets `MatchTimeout` and
returns. Entries are therefore shared across concurrent searches as-is.

## Why `regexcache` itself is not the thing adopted

`regexcache` is a drop-in for `regexp.Compile`, and harbrr does not call `regexp.Compile`
directly — it calls `regexadapter.Compile`, which routes between RE2 and regexp2 per
`RouteOptions` and returns harbrr's own `*Regexp` wrapper. Using `regexcache` would return the
wrong type and bypass the routing that exists for Jackett parity. The cache has to live *inside*
`regexadapter`, keyed on the routing decision, which means `ttlcache` directly.

Keying on the resolved `wantRegexp2` boolean rather than on `RouteOptions` is deliberate: the
decision is all the routing inputs contribute to the result, so every Latin-script language
collapses onto one entry instead of one per language code.

## Why a TTL and not an unbounded map

A filter's pattern argument is a template (`renderFilterArgs`), so a definition *may*
interpolate row- or query-derived text into the pattern itself, making the key space unbounded.
No vendored definition does today — 0 of 1890 filter arg-blocks template `args[0]`; the 167 that
template anything all template the *replacement*, `args[1]`. But a dropin or a future vendor
refresh can, and paying for eviction we already have costs nothing. The 15-minute sliding window
matches `regexcache`'s own numbers rather than inventing new ones: a pattern in active use never
expires, a definition that stops being searched lets its patterns go.

Compile *failures* are left uncached. A pattern both engines reject is a definition bug that
recurs per row, but it is also about to be loud, and caching it would mean keying on an error.

## `ttlcache` is rejected for harbrr's existing caches

Nothing else in harbrr is shaped like `ttlcache`:

- The **search-results cache** (`internal/database/searchcache.go`) is SQLite-backed by design —
  durability across restart, hit counters, stale-while-revalidate, and the persisted stats the
  management endpoint reads. An in-memory TTL map is not a smaller version of it, it is a
  different feature.
- **`newznab/caps.go`**, **`login/session.go`**, and **`cardigann/engine.go`** hold single
  mutex-guarded values, not keyed maps. A generic cache would be strictly more machinery.
- **`announceWindow`** (`searchcache_announce.go`) is the only genuinely map-shaped candidate,
  and it is the clearest rejection: it needs a **hard size cap** (`announceDedupMax`, with oldest-
  first eviction) and an **injected `now`** for deterministic tests. `ttlcache` has neither — it
  has no size bound at all, and reads its own clock. Swapping it in would trade a bounded,
  testable structure for an unbounded, time-dependent one.

## `timecache` is rejected outright

`timecache` serves a coarse cached clock to hot paths that read the time constantly. harbrr has
no such path, and it deliberately goes the other way: `now time.Time` parameters and injected
`clock: time.Now` seams exist throughout precisely so tests are deterministic. A cached clock is
the opposite of the property the codebase is built around.

## On the dependency itself

`go-cache` is same-org (go.mod already carries `go-deluge`, `go-qbittorrent`, `go-rtorrent`),
MIT, Go 1.27, ~700 non-test lines, and has **zero transitive dependencies** — the module graph
does not grow. It is pinned at `v1.0.0-rc1`, the only published version; a pre-1.0 pin is
acceptable here because the surface used is four calls (`New`, `Get`, `Set`, `DefaultTTL`) behind
harbrr's own `Compile`, so a breaking change is contained to one file.

## Consequences

- The dependency is load-bearing for exactly one file
  (`cardigann/internal/regexadapter/cache.go`). If `go-cache` ever becomes unattractive, the
  replacement is a `sync.Map` in that file and the loss is only the eviction policy.
- One package-level cache means one permanent background expiration goroutine, never `Close`d.
  It idles with its timer stopped while the cache is empty, harbrr runs no goroutine-leak
  detector, and the cache's lifetime is the process's — so there is nothing to wire into
  `app/lifecycle.go`.
- Parity is unaffected: the memo changes no compiled result, and the offline parity gate covers
  it as it covered the uncached path.
- Future "should we use go-cache for X?" questions have an answer here. The bar this ADR sets is
  the one the regex site cleared: a keyed, repeated computation on a hot path, whose result is
  pure, and which does not need a size cap or an injectable clock.
