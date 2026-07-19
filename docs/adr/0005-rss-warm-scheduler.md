# Scheduled RSS warm-cache poller

harbrr's search-results cache (#60) makes every RSS poll from an *arr/qui a cache read once
something has warmed the entry — but the first poll after a TTL expiry, or after a restart on a
quiet indexer, still pays a live tracker fetch. #252 adds a background poller, per opted-in
indexer at a configurable interval, that proactively refreshes the canonical RSS cache entry so
downstream RSS polls stay a pure cache read even when no client happens to poll right after
expiry. This is harbrr's first scheduler beyond the existing reap-style maintenance goroutines
(session/cache cleanup, stat flushes).

## Served path, not a parallel fetch path

The warmer drives the exact same path a served request takes:
`adapter.Search(core.WithCacheBypass(ctx), search.Query{})`. `WithCacheBypass` forces a live
fetch + store instead of a cache read; an empty `search.Query{}` classifies as `isEmptyQuery` and
`Search` canonicalizes its categories to the definition's `DefaultCategories` (#249) — the exact
canonicalization that makes every RSS consumer's poll collapse onto ONE cache key. Warming with
this same empty query therefore produces and stores under the identical key #257's downstream RSS
polls read. Considered and rejected: a second, warmer-only fetch path — it would need to
duplicate cache-key canonicalization, budget reservation, and circuit-breaker gating, or risk
drifting from the served path's behavior over time. Reusing `adapter.Search` means the warmer
gets all of that for free and can never diverge from what a real RSS poll would have done.

## Budget and breaker are inherited, not reimplemented

"Never exceed the configured request budget" and "back off a broken indexer" are both properties
of the served path already (`budgetedLiveSearch` / `liveSearch`, #251 and #253), so the warmer
does nothing special for either:

- An exhausted budget surfaces as `errBudgetExhausted`. With `CacheBypass` set, `Search`'s
  stale-serve fallback is skipped (guarded by `!core.CacheBypass(ctx)`), so the error reaches the
  warmer directly. The circuit breaker explicitly never trips on a budget refusal
  (`tripBreaker` excludes it) — a budget cap is an operator choice, not tracker unhealthiness.
- A tripped circuit returns `errCircuitOpen` from `checkCircuit` before the tracker is ever hit.

Either error — and any other `Search` failure — is a **logged skip**: the warmer does not retry
within the tick, does not treat it as fatal, and simply waits for the target's next scheduled
warm. There is no retry loop to reason about separately from the normal per-tick schedule.

## Tick granularity vs. interval bounds

The scheduler ticks at a fixed one-minute granularity (`WarmTickInterval`) regardless of how
long any individual target's interval is. Configured intervals are clamped to [10m, 120m], so the
nominal ticker-granularity jitter is ≤1 minute against a ≥10-minute interval. Actual delay can
exceed that: a tick warms its due targets serially, so a slow upstream `Search` (or several
targets due on the same tick) pushes later warms past the minute — acceptable for a cache
freshener, and it keeps the loop itself dead simple (reusing `app/lifecycle.go`'s existing
`reap` skeleton) rather than needing a per-target timer.

## Defer-and-stagger the first warm

The cache is SQLite-persisted across a restart, so warming at boot (t=0) would often just
re-refresh entries that are still fresh — wasted tracker traffic on every restart. A cold cache
entry is already covered by ordinary pull-through on the first real poll. So the warmer defers
each target's first warm to its first scheduled due time rather than firing immediately at boot.

Naively, "first due at boot + interval" still has a problem: every opted-in indexer configured
with the same interval reaches its first due time at the *same* instant, and — because `nextDue`
advances by a fixed `+interval` each cycle — reconverges at *every* interval boundary
thereafter. For an operator with several indexers behind one shared proxy or FlareSolverr
instance, that's a recurring burst of N concurrent engine builds and fetches at a predictable
cadence — exactly what a single-fetch-per-TTL cache is supposed to prevent.

The fix is a stable **per-instance phase offset** in `[0, interval)`:
`warmPhase(instanceID, interval)` — a deterministic, minute-granular function of the instance ID
(`instanceID % intervalMinutes` minutes) — seeds each target's first `nextDue` at
`now + warmPhase(...)` instead of uniformly at `now + interval`. Because `nextDue` always advances
by `+interval` from its own previous *scheduled* value (not from "now"), the phase is preserved
forever: no drift, and the herd is killed at every future boundary too, not just the first one.
When advancing past a due tick, `nextDue` skips ahead by whole intervals until it lands strictly
after "now" (not just one `+interval` step) — otherwise a process suspended (laptop/VM sleep) past
several intervals would replay one warm per missed interval instead of the single refresh the
cache actually needs, while still landing on the original phase.

`ponytail:` the modulo can collide two instance IDs into the same one-minute slot — still far
better than every target firing at once. Upgrade to an `i·interval/n` stable-ordering assignment
only if even distribution across a slot ever actually matters at the scale harbrr runs at.

## Cache-off is a correctness gate, not just thrift

The whole tick is gated on the search cache being configured **and** runtime-enabled
(`searchCache != nil && tuning.enabled`). This isn't only about avoiding a wasted fetch: with
caching off, `adapter.Search` runs `liveSearch` directly and never stores anything, so a
cache-bypass poll would be a live tracker hit whose result is thrown away outright. Gating on the
cache being on is therefore required for correctness, not an optimization.

## Configuration: reserved instance setting, backend-only

The interval is `rss_warm_interval`, a Go duration string (e.g. `"15m"`) stored via the existing
generic per-instance settings mechanism — the same mechanism `rate_interval`, `cache_ttl`, and
`query_limit` already use. Absent, unparseable, or non-positive means disabled: opt-in,
default-off. A valid value is clamped to [10m, 120m]. There is no new API surface — no OpenAPI
schema change, no web UI — the setting rides the existing `Manager.Update` instance-settings path
like every other reserved setting.

## Re-read targets every tick

Rather than wiring cache-invalidation notifications into the scheduler, each tick simply re-reads
the enabled instance list and per-instance settings fresh (`Resolver.warmTargets`). At
single-user scale, one `List` plus one `Settings` query per instance per minute is trivial, and it
picks up an interval change, a newly-opted-in indexer, or a disable immediately with zero
invalidation plumbing. `ponytail:` this is an N+1 query pattern; upgrade to a single indexed query
if the instance count ever grows enough for it to matter.

## Shutdown

The warmer reuses `app/lifecycle.go`'s `reap` skeleton verbatim — the same ticker-loop-plus-
`sync.WaitGroup` shape as session cleanup, search-cache cleanup, stat flushes, and health-event
retention. No new ticker or shutdown code was written for this feature; `App.Run`'s existing
join-before-close ordering covers the warmer goroutine automatically.

## Consequences

- The warmer can never drift from what a real RSS poll does, because it *is* a real RSS poll —
  it inherits budget enforcement, circuit-breaker gating, and cache-key canonicalization for
  free, with zero duplicated logic.
- A budget-exhausted or circuit-open target is silently skipped for the tick, not retried — a
  slow/degraded indexer or an exhausted budget self-heals on its own schedule rather than the
  warmer adding pressure.
- The stagger means two indexers on the same interval do not, in general, warm in the same
  tick — an intentional trade against perfectly synchronized (and therefore burst-prone)
  scheduling.
- No OpenAPI or web changes ship with this feature; enabling per-indexer RSS warming today
  requires setting `rss_warm_interval` through the existing generic settings write path.
