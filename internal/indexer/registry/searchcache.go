package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/singleflight"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
)

// swrRefreshTimeout bounds a stale-while-revalidate background refresh so a slow
// tracker can never leak a goroutine for the full request lifetime.
const swrRefreshTimeout = 30 * time.Second

// SearchCache is the registry-wide cache-aside layer for indexer searches. It
// holds the SQLite store, a singleflight group (so concurrent identical misses
// drive the tracker exactly once), the resolved TTL tiers, the refresh-ahead
// threshold, a clock, and a logger, plus cumulative hit/miss counters (persisted
// across restarts via counterStore).
//
// SECRETS-AT-REST: the store's results_json holds the FULL pre-/dl-rewrite release
// slice, whose Link/Magnet embed passkeys for some trackers. This layer NEVER logs
// the payload, a release, or a link — only the cache_key (a SHA-256 hash) and a
// redacted error. The SWR goroutine is the most dangerous spot; it follows the same
// rule.
type SearchCache struct {
	store database.SearchCacheStore
	// counterStore persists the per-instance hit/miss/suppressed counters so the
	// stats survive a restart (rehydrated at boot, flushed on the cleanup tick and
	// at shutdown). Stateless zero value, like store.
	counterStore database.CacheCountersStore
	// db is a Querier (not just an Execer) so SetConfig can wrap its multi-row
	// persist in a transaction; every read/write path still uses it as an Execer.
	db dbinterface.Querier
	sf singleflight.Group
	// tuning is the live, atomically-swappable config (TTL tiers, thin threshold,
	// refresh-ahead, enabled). Read per request (lock-free) so the global knobs are
	// runtime-tunable; seeded from the config file, overlaid by LoadOverrides.
	tuning atomic.Pointer[cacheTuning]
	// cfgMu serializes the read-merge-validate-persist-swap of UpdateConfig (and the
	// boot LoadOverrides) so concurrent updates can't lose each other's fields; the
	// per-request read path stays lock-free on tuning.
	cfgMu sync.Mutex
	clock func() time.Time
	log   zerolog.Logger

	// hits/misses are the global counters for the hit-ratio metric the stats endpoint
	// exposes. breakerSuppressed counts MISSes short-circuited by an open negative
	// breaker (tracker requests the breaker spared) — a separate category, not folded
	// into hits or misses. All three are the sum of the per-instance instCounters and
	// survive a restart: rehydrated from counterStore at boot, flushed back on the
	// cleanup tick and at shutdown (see searchcache_counters.go). A hard crash between
	// flushes loses the increments since the last cleanup tick — at most one
	// cleanup_interval — which is acceptable for observability-only counters.
	hits              atomic.Int64
	misses            atomic.Int64
	breakerSuppressed atomic.Int64

	// countersRehydrated gates FlushCounters: it is set once RehydrateCounters has
	// loaded the persisted counts at boot, so a failed/early flush can never overwrite
	// the stored totals with zeroes.
	countersRehydrated atomic.Bool

	// breaker is the per-instance negative-result circuit breaker (see searchcache_
	// breaker.go). Always present; inert unless the negative window (ttl.negative) is
	// positive.
	breaker *negativeBreaker

	// announceSink, when set, receives newly-observed releases on an RSS/empty-query
	// cache write-back (the cross-seed announce source — see searchcache_announce.go).
	// nil means no announce targets are configured (a no-op tap). announced is the
	// dedup window guarding re-announce across cache expiry; always present.
	announceSink AnnounceSink
	announced    *announceWindow

	// instCounters holds per-instance hit/miss/suppressed counters keyed by instanceID
	// for the per-indexer stats surface. Persisted via counterStore (see the hits/misses
	// note above), so they survive a restart.
	instCounters sync.Map // map[int64]*instanceCounters

	// touchMu guards touchPending, the in-memory coalescing buffer for per-entry
	// hit_count/last_used_at bumps. A cache hit records here (cheap, in-process)
	// instead of issuing a SQLite write per hit; the buffer is drained by
	// FlushTouches (on the cleanup tick, on Stats, and at shutdown), so N hits on
	// one key collapse to a single UPDATE. hit_count/last_used_at are observability
	// only (TTL drives expiry), so losing an unflushed interval on a hard crash is
	// acceptable.
	touchMu      sync.Mutex
	touchPending map[string]pendingTouch

	// epochMu guards instanceEpochs, the per-instance invalidation generation. A
	// config-mutation purge (InvalidateByInstance) bumps the instance's epoch under this
	// lock; buildAdapter snapshots the current value into the adapter's builtEpoch
	// before it reads the instance's settings, and storeBestEffort drops any write-back
	// whose captured epoch is stale — re-checked once more right after a successful
	// Store to catch a bump that lands inside the check-then-store window itself. This
	// closes the window where a store from an OLD engine/config (a detached SWR refresh
	// or an in-flight miss holding an old adapter) lands AFTER the purge and resurrects
	// a stale-config entry served until TTL (U8R-F4).
	epochMu        sync.Mutex
	instanceEpochs map[int64]uint64
}

// pendingTouch is one key's buffered hit delta and most-recent use time.
type pendingTouch struct {
	hits     int64
	lastUsed time.Time
}

// instanceCounters holds one instance's hit/miss/suppressed counts (persisted across
// restarts via counterStore).
type instanceCounters struct {
	hits       atomic.Int64
	misses     atomic.Int64
	suppressed atomic.Int64
}

// counters returns (creating on first use) the counter set for instanceID.
func (c *SearchCache) counters(instanceID int64) *instanceCounters {
	v, _ := c.instCounters.LoadOrStore(instanceID, &instanceCounters{})
	ic, _ := v.(*instanceCounters)
	return ic
}

// instanceEpoch reads instanceID's current invalidation generation (0 for an instance
// never invalidated). Snapshotted at engine-build time and re-read before every store
// to reject write-backs from a superseded config generation (U8R-F4).
func (c *SearchCache) instanceEpoch(instanceID int64) uint64 {
	c.epochMu.Lock()
	defer c.epochMu.Unlock()
	return c.instanceEpochs[instanceID]
}

// bumpInstanceEpoch advances instanceID's invalidation generation so any store from an
// adapter built before this call is rejected by storeBestEffort's epoch gate. Called
// from InvalidateByInstance under the config-mutation purge.
func (c *SearchCache) bumpInstanceEpoch(instanceID int64) {
	c.epochMu.Lock()
	c.instanceEpochs[instanceID]++
	c.epochMu.Unlock()
}

// newSearchCache builds the cache layer. db is the shared store handle, t the
// initial (config-seeded) tuning, clock the reference clock, and log a logger that
// only ever sees cache keys and redacted errors. The tuning is held atomically so
// SetConfig can swap it at runtime. Unexported: t is the unexported cacheTuning, so
// this is unconstructable outside the package anyway; NewSearchCacheFromConfig is
// the exported entry point.
func newSearchCache(db dbinterface.Querier, t cacheTuning, clock func() time.Time, log zerolog.Logger) *SearchCache {
	if clock == nil {
		clock = time.Now
	}
	c := &SearchCache{
		db:             db,
		clock:          clock,
		log:            log,
		touchPending:   make(map[string]pendingTouch),
		breaker:        newNegativeBreaker(),
		announced:      newAnnounceWindow(),
		instanceEpochs: make(map[int64]uint64),
	}
	c.tuning.Store(&t)
	return c
}

// SetAnnounceSink installs the cross-seed announce source tap. It is a setter (not a
// constructor arg) because the announce service is built after the cache in cmd/harbrr;
// a nil sink leaves the tap a no-op. Called once at wiring time, before serving.
func (c *SearchCache) SetAnnounceSink(sink AnnounceSink) { c.announceSink = sink }

// NewSearchCacheFromConfig builds a SearchCache from a CacheConfigView. It is
// the exported entry point for cmd/harbrr; newSearchCache stays internal so the
// ttlConfig tier struct does not leak across the package boundary.
func NewSearchCacheFromConfig(db dbinterface.Querier, v CacheConfigView, clock func() time.Time, log zerolog.Logger) *SearchCache {
	return newSearchCache(db, v.tuning(), clock, log)
}

// liveSearchFn is the live-fetch seam the cache drives on a miss or a refresh: the
// adapter's liveSearch (driver call + stats + health + id-wrap), returning the FULL
// catalog. The cache holds only this narrow seam, never the whole core.Indexer — it
// only ever needed to fetch — so the paging signal is passed in as a bool taken off the
// concrete adapter (SupportsOffsetPaging) rather than re-discovered by an interface
// type-assert, and the enabled gate + freeleech view stay in the adapter.
type liveSearchFn func(ctx context.Context, q search.Query) ([]*normalizer.Release, error)

// search is the cache-aside read path. nocache bypasses the cache entirely (live
// search + success-only write-back). Otherwise a live, unexpired hit is served
// immediately (Touch + optional refresh-ahead async); a miss runs the live search
// under singleflight and stores the result best-effort. A Fetch error degrades open
// (falls through to a live search) and never fails the user's search.
func (c *SearchCache) search(ctx context.Context, instanceID int64, cfg map[string]string, builtEpoch uint64, live liveSearchFn, paging bool, q search.Query) ([]*normalizer.Release, error) {
	// A paging-capable driver forwards offset/limit upstream, so each page is a distinct
	// outbound request and gets its own cache entry; a non-paging driver keys page-free
	// (one fetch serves every locally-sliced page), preserving the pre-paging key. The
	// signal comes straight off the adapter (SupportsOffsetPaging) so the cache and the
	// handler can never disagree about how a driver pages.
	key := buildSearchCacheKey(instanceID, q, paging)

	if core.CacheBypass(ctx) {
		return c.liveAndStoreRecording(ctx, instanceID, cfg, builtEpoch, live, q, key)
	}

	entry, found, err := c.store.Fetch(ctx, c.db, key, c.clock())
	if err != nil {
		// Degrade open: a read failure must never fail the user's search. Route through
		// missPath (not fetchLive directly) so concurrent identical requests coalesce on
		// the flight and the breaker fast-path applies — a degraded request now
		// (accurately) counts as a miss. missPath's own store.Fetch double-check inside
		// the flight will likely fail again too; that is harmless, it just falls through
		// to a live search.
		c.log.Warn().Str("cache_key", key).Str("error", apphttp.RedactError(err)).
			Msg("registry: search cache fetch failed; serving live")
		return c.missPath(ctx, instanceID, cfg, builtEpoch, live, q, key)
	}
	if found {
		return c.serveHit(ctx, instanceID, cfg, builtEpoch, live, q, key, entry)
	}
	return c.missPath(ctx, instanceID, cfg, builtEpoch, live, q, key)
}

// fetchStale reads the cache entry for (instanceID, q) REGARDLESS of expiry (via
// store.FetchAny), for the request-budget exhaustion path (autobrr/harbrr#251):
// when the query budget has no capacity left for an outbound fetch, Search prefers
// serving whatever was last cached — even expired — over refusing the request
// outright. found=false when there is no entry at all (nothing to serve stale), in
// which case the caller surfaces the budget-exhausted error instead. This never
// re-stores the entry (unlike a live fetch), so serving a stale hit does not reset
// its expiry or mark it freshly cached.
func (c *SearchCache) fetchStale(ctx context.Context, instanceID int64, paging bool, q search.Query) ([]*normalizer.Release, bool, error) {
	key := buildSearchCacheKey(instanceID, q, paging)
	entry, found, err := c.store.FetchAny(ctx, c.db, key)
	if err != nil {
		return nil, false, fmt.Errorf("registry: fetch stale search cache %q: %w", key, err)
	}
	if !found {
		return nil, false, nil
	}
	releases, derr := decodeReleases(entry.ResultsJSON, key)
	if derr != nil {
		return nil, false, derr //nolint:wrapcheck // decodeReleases already wraps with the key only.
	}
	c.recordCacheInfo(ctx, core.CacheInfo{Cached: true, ExpiresAt: entry.ExpiresAt})
	return releases, true, nil
}

// missPath is the fast-path breaker consult for a cache miss: if the breaker is open
// for the instance, it short-circuits to the recorded error without touching the
// tracker (counting one suppression), so a suppressed request never counts as a miss.
// Otherwise it runs the live miss. Tripping the breaker on a live failure now happens
// inside fetchLive (the single outbound funnel, driven from deep within serveMiss) —
// missPath only ever gates here, it never trips. It is the single funnel for every
// miss (the read-path miss, serveHit's corrupt-payload fallback, and the degrade-open
// path) so all three honor the breaker.
func (c *SearchCache) missPath(ctx context.Context, instanceID int64, cfg map[string]string, builtEpoch uint64, live liveSearchFn, q search.Query, key string) ([]*normalizer.Release, error) {
	// Consult the breaker only while it is armed (negative window > 0); reading the
	// live config means a runtime disable (negative_ttl -> 0) stops suppression at
	// once, without waiting for already-open windows to lapse. fetchLive self-gates
	// the same way, so disabling also halts new trips.
	if c.tuning.Load().ttl.negative > 0 {
		if rerr := c.breaker.replay(instanceID, c.clock()); rerr != nil {
			c.breakerSuppressed.Add(1)
			c.counters(instanceID).suppressed.Add(1)
			return nil, rerr
		}
	}
	return c.serveMiss(ctx, instanceID, cfg, builtEpoch, live, q, key)
}

// tripBreaker opens the breaker for instanceID when err is a tracker failure worth
// suppressing. A caller-cancelled context is excluded — that is the consumer aborting,
// not the tracker failing — so a cancellation never poisons the breaker.
//
// Two cancel shapes are filtered: ctx.Err() catches THIS caller aborting its own
// request; errors.Is(err, context.Canceled) catches a singleflight FOLLOWER that
// inherited the LEADER's cancelled-context error while its OWN ctx is still live.
// Without the second check, one disconnected client (the flight leader) would trip
// the breaker instance-wide, suppressing every other consumer for the full window.
func (c *SearchCache) tripBreaker(ctx context.Context, instanceID int64, err error) {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return
	}
	// A request-budget refusal (autobrr/harbrr#251) is a self-imposed guard, not the
	// tracker failing — tripping the breaker over it would suppress every OTHER
	// consumer's request for the negative-TTL window even though the tracker itself
	// is healthy. Composing budget-exhaustion with the breaker is explicitly a
	// serve-stale concern, never a circuit trip.
	if errors.Is(err, errBudgetExhausted) {
		return
	}
	// A circuit-open refusal (autobrr/harbrr#253) is likewise a self-imposed gate, not
	// a tracker failure — the request never left the process. Without this, funneling
	// every liveSearchFn error (including the circuit's) through fetchLive would let
	// the negative breaker replay a "disabled till T" error even after T has passed
	// and the circuit itself has recovered.
	if errors.Is(err, errCircuitOpen) {
		return
	}
	until, ok := classifyBreakerError(err, c.tuning.Load().ttl.negative, c.clock())
	if !ok {
		return
	}
	c.breaker.trip(instanceID, until, err)
}

// serveHit returns the decoded cached slice immediately, bumps the hit counters,
// records a Touch (async, best-effort), and fires a single refresh-ahead when the
// entry is past its refresh threshold. Before any of that it clamps the entry to
// effectiveExpiry: if the CURRENT tuning would no longer grant it a positive TTL
// (e.g. an operator just lowered a tier), it is treated as a miss and routed
// through the same missPath a stored-but-absent entry uses, so the fresh live
// search overwrites the row instead of the response serving content that is stale
// under today's config. Otherwise the effective (not stored) expiry is what gets
// surfaced in the CacheInfo, so HTTP validators/max-age reflect the clamp.
func (c *SearchCache) serveHit(ctx context.Context, instanceID int64, cfg map[string]string, builtEpoch uint64, live liveSearchFn, q search.Query, key string, entry database.SearchCacheEntry) ([]*normalizer.Release, error) {
	effective := c.effectiveExpiry(entry, cfg, q)
	if !effective.After(c.clock()) {
		return c.missPath(ctx, instanceID, cfg, builtEpoch, live, q, key)
	}
	releases, err := decodeReleases(entry.ResultsJSON, key)
	if err != nil {
		// A corrupt payload is treated as a miss: never fail the search over it.
		c.log.Warn().Str("cache_key", key).Str("error", apphttp.RedactError(err)).
			Msg("registry: search cache decode failed; serving live")
		return c.missPath(ctx, instanceID, cfg, builtEpoch, live, q, key)
	}
	c.hits.Add(1)
	c.counters(instanceID).hits.Add(1)
	c.recordCacheInfo(ctx, core.CacheInfo{Cached: true, ExpiresAt: effective})
	c.recordTouch(key)
	if c.shouldRefreshAhead(entry) {
		c.triggerSWR(ctx, instanceID, cfg, builtEpoch, live, q, key)
	}
	return releases, nil
}

// effectiveExpiry returns the expiry entry would receive if it were freshly stored
// right now, under the CURRENT tuning, clamped to never exceed its actual stored
// ExpiresAt: min(entry.ExpiresAt, entry.CachedAt + resolveTTL(cfg, q,
// entry.TotalResults)). This makes a TTL tier LOWERED via UpdateConfig take effect
// on already-stored rows immediately, one read at a time, instead of only once
// their original (now too generous) expiry lapses — the only other remedy being a
// full cache flush. A RAISED tier deliberately does NOT extend a stored row past
// its own ExpiresAt (the min never exceeds the stored value); that requires a fresh
// live fetch. resolveTTL's own thin clamp and warm floor apply exactly as they
// would to a fresh store of this same result, by construction.
func (c *SearchCache) effectiveExpiry(entry database.SearchCacheEntry, cfg map[string]string, q search.Query) time.Time {
	fresh := entry.CachedAt.Add(c.tuning.Load().ttl.resolveTTL(cfg, q, entry.TotalResults))
	if fresh.Before(entry.ExpiresAt) {
		return fresh
	}
	return entry.ExpiresAt
}

// serveMiss runs the live search under singleflight (so concurrent identical misses
// AT THE SAME invalidation epoch drive the tracker once), stores the result
// best-effort, and returns it. The flight key is epoch-scoped (cacheFlightKey) so a
// request whose epoch has advanced past an in-flight leader's never coalesces onto
// (and receives) that leader's stale-epoch result — it drives its own live search
// instead. The double-check inside the flight lets a request that lost the race read
// a freshly stored entry instead of re-searching.
//
// A singleflight FOLLOWER inherits the LEADER's flight result — including a context
// error if the leader's client disconnected or its request deadline elapsed
// mid-fetch. When our OWN context is still live, that cancellation is the LEADER's,
// not ours: we are a healthy request and must not return an errored feed just because
// the request we coalesced onto went away. retryMissFlight handles that recovery (see
// its doc for why it re-enters the flight rather than searching independently). The
// ctx.Err() == nil guard is load-bearing: a follower whose OWN ctx is cancelled must
// still return the cancellation (never mask a real client-gone with a fresh search).
// Both context.Canceled (client disconnect) and context.DeadlineExceeded (leader
// request deadline) qualify — once our ctx is proven live, ANY context error in the
// flight result can only be the leader's.
func (c *SearchCache) serveMiss(ctx context.Context, instanceID int64, cfg map[string]string, builtEpoch uint64, live liveSearchFn, q search.Query, key string) ([]*normalizer.Release, error) {
	c.misses.Add(1)
	c.counters(instanceID).misses.Add(1)
	flightKey := cacheFlightKey(key, builtEpoch)
	v, err, _ := c.sf.Do(flightKey, c.missFlight(ctx, instanceID, cfg, builtEpoch, live, q, key))
	if err != nil {
		if ctx.Err() == nil && isContextError(err) {
			return c.retryMissFlight(ctx, instanceID, cfg, builtEpoch, live, q, key, flightKey)
		}
		return nil, err //nolint:wrapcheck // already wrapped by liveAndStore/adapter; no key/payload to add.
	}
	return c.resolveMissFlightResult(ctx, instanceID, cfg, builtEpoch, live, q, key, v)
}

// missFlight returns the singleflight closure for one live-search attempt at key,
// scoped to ctx: a caller that lost the coalescing race reads a freshly stored entry
// (the double-check) instead of re-searching; otherwise it drives fetchLive — the
// single outbound funnel, so this is also where the negative breaker gates and learns
// — and stores a success best-effort. The returned error is already wrapped by
// liveAndStore/the adapter, so it is passed through unwrapped.
//
// The double-check is subject to the same effectiveExpiry clamp as serveHit: without
// it, a request routed HERE precisely because serveHit judged the stored row too old
// under the CURRENT tuning would immediately re-read that identical (DB-unexpired)
// row and serve it straight back, silently undoing the clamp instead of driving a
// fresh fetch. A row a genuine race-loser reads here was written moments ago under
// the current tuning, so its effective expiry is its stored one — the clamp is a
// no-op for the case the double-check exists for.
func (c *SearchCache) missFlight(ctx context.Context, instanceID int64, cfg map[string]string, builtEpoch uint64, live liveSearchFn, q search.Query, key string) func() (any, error) {
	return func() (any, error) {
		if entry, found, ferr := c.store.Fetch(ctx, c.db, key, c.clock()); ferr == nil && found {
			if effective := c.effectiveExpiry(entry, cfg, q); effective.After(c.clock()) {
				if releases, derr := decodeReleases(entry.ResultsJSON, key); derr == nil {
					info := core.CacheInfo{Cached: true, ExpiresAt: effective}
					return missResult{releases: releases, info: info}, nil
				}
			}
		}
		releases, info, lerr := c.liveAndStore(ctx, instanceID, cfg, builtEpoch, live, q, key)
		if lerr != nil {
			return nil, lerr
		}
		return missResult{releases: releases, info: info}, nil
	}
}

// retryMissFlight re-runs the miss flight ONCE more at flightKey after the first
// attempt inherited a dead flight leader's context error (the leader's own client
// disconnected or its request deadline elapsed) while OUR ctx is still live.
// Singleflight forgets a key once its flight completes, so this second Do starts a
// genuinely fresh flight: every follower that reaches this branch around the same
// time re-coalesces onto ONE retry leader, instead of each independently running its
// own live search against the tracker (a follower stampede). If the retry ALSO
// inherits a dead leader's context error — the retry leader died too — fall back to a
// bounded, un-coalesced live search so a healthy follower is still guaranteed an
// answer, mirroring the type-mismatch fallback below.
func (c *SearchCache) retryMissFlight(ctx context.Context, instanceID int64, cfg map[string]string, builtEpoch uint64, live liveSearchFn, q search.Query, key, flightKey string) ([]*normalizer.Release, error) {
	v, err, _ := c.sf.Do(flightKey, c.missFlight(ctx, instanceID, cfg, builtEpoch, live, q, key))
	if err != nil {
		if ctx.Err() == nil && isContextError(err) {
			return c.liveAndStoreRecording(ctx, instanceID, cfg, builtEpoch, live, q, key)
		}
		return nil, err //nolint:wrapcheck // already wrapped by liveAndStore/adapter; no key/payload to add.
	}
	return c.resolveMissFlightResult(ctx, instanceID, cfg, builtEpoch, live, q, key, v)
}

// resolveMissFlightResult type-asserts a completed flight's value to missResult and
// records its cache info per CALLER, outside the flight, so every coalesced miss
// fills its own sink. A value of an unexpected type can only mean this caller
// coalesced onto a flight that returned something else (defensive) — it never serves
// an empty success on a type mismatch, it runs its own live search instead.
func (c *SearchCache) resolveMissFlightResult(ctx context.Context, instanceID int64, cfg map[string]string, builtEpoch uint64, live liveSearchFn, q search.Query, key string, v any) ([]*normalizer.Release, error) {
	res, ok := v.(missResult)
	if !ok {
		return c.liveAndStoreRecording(ctx, instanceID, cfg, builtEpoch, live, q, key)
	}
	c.recordCacheInfo(ctx, res.info)
	return res.releases, nil
}

// liveAndStore runs the live search and, on success, writes the result back
// best-effort (a store failure never fails the search), returning the releases and
// whether the entry is now cached (plus its expiry). An inner error is returned and
// never cached. It does NOT record into the request sink — the caller does
// (per-caller, so a singleflight follower also gets the cache info).
func (c *SearchCache) liveAndStore(ctx context.Context, instanceID int64, cfg map[string]string, builtEpoch uint64, live liveSearchFn, q search.Query, key string) ([]*normalizer.Release, core.CacheInfo, error) {
	releases, err := c.fetchLive(ctx, instanceID, live, q)
	if err != nil {
		return nil, core.CacheInfo{}, err
	}
	cached, expiresAt := c.storeBestEffort(ctx, instanceID, cfg, builtEpoch, q, key, releases)
	return releases, core.CacheInfo{Cached: cached, ExpiresAt: expiresAt}, nil
}

// liveAndStoreRecording is liveAndStore for the synchronous, non-flight callers (the
// nocache bypass path and the defensive miss fallback): it records the cache info into
// this caller's own sink. The singleflight miss path instead records outside the flight
// so every coalesced caller is covered.
func (c *SearchCache) liveAndStoreRecording(ctx context.Context, instanceID int64, cfg map[string]string, builtEpoch uint64, live liveSearchFn, q search.Query, key string) ([]*normalizer.Release, error) {
	releases, info, err := c.liveAndStore(ctx, instanceID, cfg, builtEpoch, live, q, key)
	if err != nil {
		return nil, err //nolint:wrapcheck // already wrapped by the adapter; no key/payload to add.
	}
	c.recordCacheInfo(ctx, info)
	return releases, nil
}

// fetchLive is the single outbound funnel: the miss flight, the SWR refresh, the
// follower retry, the nocache bypass, and the degrade-open path (via missPath) ALL
// reach the tracker only through here, so the per-instance negative breaker gates and
// learns from every attempt. Replay is checked BEFORE live runs, so a replayed
// (suppressed) error can never re-trip the breaker and extend its window — only a
// genuine live-call error trips. The error live returns is already wrapped with the
// indexer id by the adapter's liveSearch; the caller redacts it.
func (c *SearchCache) fetchLive(ctx context.Context, instanceID int64, live liveSearchFn, q search.Query) ([]*normalizer.Release, error) {
	if c.tuning.Load().ttl.negative > 0 {
		if rerr := c.breaker.replay(instanceID, c.clock()); rerr != nil {
			c.breakerSuppressed.Add(1)
			c.counters(instanceID).suppressed.Add(1)
			return nil, rerr
		}
	}
	releases, err := live(ctx, q)
	if err != nil {
		c.tripBreaker(ctx, instanceID, err)
		return nil, err //nolint:wrapcheck // adapter already wraps with the indexer id.
	}
	return releases, nil
}

// storeBestEffort encodes and upserts the result. It resolves the TTL from the raw
// engine count (the slice is pre-dedupe/pre-filter, so this count can exceed what
// the user ultimately sees — the thin clamp is measured on raw results, by design).
// A non-positive TTL or any store error is logged (key only) and swallowed. It returns
// whether the entry is now cached and its expiry, so the synchronous caller can surface
// the conditional-GET signal; an encode failure returns cached=false. The cached signal
// is still true even if the Store write fails — the response content is still valid,
// and an unstored entry simply misses on the next request.
func (c *SearchCache) storeBestEffort(ctx context.Context, instanceID int64, cfg map[string]string, builtEpoch uint64, q search.Query, key string, releases []*normalizer.Release) (bool, time.Time) {
	// A config-mutation purge (InvalidateByInstance) bumps this instance's epoch. If it
	// has advanced since the adapter behind this fetch was built, the fetch ran
	// through an OLD engine/config and this write-back would resurrect a stale-config
	// entry the purge just removed — served until TTL, violating "a config change must
	// never serve stale results". Skip the store (and the announce tap below, so a
	// dropped write emits no spurious announce diff) and report not-cached; the
	// purged cache stays empty and the next request rebuilds through the fresh engine.
	if c.instanceEpoch(instanceID) != builtEpoch {
		c.log.Debug().Str("cache_key", key).
			Msg("registry: search cache store skipped; instance config changed during fetch")
		return false, time.Time{}
	}
	// Derive the "what's new" announce stream from this write-back BEFORE the store, so
	// the prior cached entry (the one we overwrite) is still readable for the GUID diff.
	c.tapAnnounce(ctx, instanceID, q, key, releases)

	payload, err := json.Marshal(releases)
	if err != nil {
		c.log.Warn().Str("cache_key", key).Str("error", apphttp.RedactError(err)).
			Msg("registry: search cache encode failed; not caching")
		return false, time.Time{}
	}
	now := c.clock()
	ttl := c.tuning.Load().ttl.resolveTTL(cfg, q, len(releases))
	entry := database.SearchCacheEntry{
		CacheKey:     key,
		InstanceID:   instanceID,
		ResultsJSON:  payload,
		TotalResults: len(releases),
		CachedAt:     now,
		LastUsedAt:   now,
		ExpiresAt:    now.Add(ttl),
	}
	if err := c.store.Store(ctx, c.db, entry); err != nil {
		c.log.Warn().Str("cache_key", key).Str("error", apphttp.RedactError(err)).
			Msg("registry: search cache store failed")
		return true, entry.ExpiresAt
	}
	// The initial epoch gate above and the Store call just made are not atomic: a purge
	// can bump the epoch and delete nothing (because our row didn't exist yet) in the
	// gap between them, then our Store lands after it and resurrects the row it just
	// purged. Re-reading the epoch here catches that: see compensateStaleStore for why
	// re-checking (rather than, say, a DB-side conditional write) is sufficient.
	if c.instanceEpoch(instanceID) != builtEpoch {
		c.compensateStaleStore(ctx, key)
		return false, time.Time{}
	}
	return true, entry.ExpiresAt
}

// compensateStaleStore deletes the row storeBestEffort just wrote, after the
// post-store epoch re-check found the epoch had moved during the check-then-store
// window — the TOCTOU gap the initial epoch gate alone cannot close.
//
// Soundness: InvalidateByInstance always bumps the epoch BEFORE it purges. If the
// re-check still observes the epoch the initial gate saw, the purge's bump had not
// happened yet, so its DELETE — which strictly follows its own bump — necessarily
// runs after our Store and removes the row itself; no action needed (and none was
// taken, this function isn't called on that path). If the re-check instead observes
// a moved epoch, the purge's DELETE may already have run before our Store committed,
// in which case our write resurrected a row the purge had just removed — deleting it
// here undoes that. Either way no row written under a superseded config generation
// survives; a redundant delete (if the purge's own DELETE gets there first or second)
// is a harmless no-op.
func (c *SearchCache) compensateStaleStore(ctx context.Context, key string) {
	// Mirror the pre-store skip's trace so both halves of the epoch gate are visible
	// when diagnosing an invalidation race — this one fires only in the rare
	// check-then-store window.
	c.log.Debug().Str("cache_key", key).
		Msg("registry: search cache store compensated; instance config changed during store")
	if err := c.store.Delete(ctx, c.db, key); err != nil {
		c.log.Warn().Str("cache_key", key).Str("error", apphttp.RedactError(err)).
			Msg("registry: search cache compensating delete failed")
	}
}

// recordCacheInfo surfaces whether this response came from — or was freshly stored
// into — the cache to the feed handler via the request's CacheInfo sink (a no-op when
// there is none — the JSON API and the detached SWR refresh carry none). An
// uncached info records nothing. It is called per CALLER, outside the singleflight,
// so coalesced misses each fill their own sink.
func (c *SearchCache) recordCacheInfo(ctx context.Context, info core.CacheInfo) {
	if !info.Cached {
		return
	}
	core.RecordCacheInfo(ctx, info)
}

// missResult is the singleflight return for a cache miss: the released slice plus the
// cache info to surface. The flight leader computes both; every coalesced caller then
// records the cache info into its own request sink after the flight returns (recording
// inside the flight would fill only the leader's sink).
type missResult struct {
	releases []*normalizer.Release
	info     core.CacheInfo
}

// recordTouch buffers a served hit in memory (cheap, non-blocking) instead of
// writing to SQLite per hit. The buffer coalesces repeated hits on the same key
// into one UPDATE at flush time — important because the cache's whole job is
// absorbing high-frequency identical polls, so a write per absorbed hit is wasteful.
func (c *SearchCache) recordTouch(key string) {
	now := c.clock()
	c.touchMu.Lock()
	e := c.touchPending[key]
	e.hits++
	e.lastUsed = now
	c.touchPending[key] = e
	c.touchMu.Unlock()
}

// FlushTouches drains the buffered hit bumps to the store, one coalesced UPDATE per
// key (hit_count += buffered hits, last_used_at = most recent). It is called on the
// cleanup tick, before Stats (so the API reflects current counts), and at shutdown.
// Best-effort: a failure is logged (key only) and the buffered counts for that key
// are lost — acceptable, since hit_count/last_used_at are observability, not state.
func (c *SearchCache) FlushTouches(ctx context.Context) {
	c.touchMu.Lock()
	pending := c.touchPending
	c.touchPending = make(map[string]pendingTouch, len(pending))
	c.touchMu.Unlock()

	for key, e := range pending {
		if err := c.store.BumpHits(ctx, c.db, key, e.hits, e.lastUsed); err != nil {
			c.log.Warn().Str("cache_key", key).Str("error", apphttp.RedactError(err)).
				Msg("registry: search cache touch flush failed")
		}
	}
}

// shouldRefreshAhead reports whether a hit is old enough to trigger a background
// refresh: it is true once the fraction of the entry's lifetime that has elapsed
// reaches refreshAt percent. It uses cached_at/expires_at (the entry's real
// lifetime), never now+ttl. A non-positive percentage disables refresh-ahead.
func (c *SearchCache) shouldRefreshAhead(entry database.SearchCacheEntry) bool {
	refreshAt := c.tuning.Load().refreshAt
	if refreshAt <= 0 {
		return false
	}
	lifetime := entry.ExpiresAt.Sub(entry.CachedAt)
	if lifetime <= 0 {
		return false
	}
	elapsed := c.clock().Sub(entry.CachedAt)
	return elapsed*100 >= lifetime*time.Duration(refreshAt)
}

// triggerSWR fires one background refresh for key, guarded by singleflight on a
// DEDICATED, epoch-scoped refresh key (swr:<key>@<epoch>) so at most one refresh runs
// per key per epoch even across many concurrent stale hits, while NEVER sharing a
// flight with a real cache miss on the same key (the two return incompatible value
// types) nor with a refresh triggered under a since-superseded epoch (which would
// otherwise write back under the OLD builtEpoch captured by whichever refresh won the
// coalesce — storeBestEffort's own epoch check would then drop it anyway, but keeping
// the flights separate avoids wasting the newer refresh's live fetch on a doomed
// write). The goroutine detaches from the request (WithoutCancel) but is bounded by a
// timeout. The write-back is success-only: an error leaves the existing entry intact
// (never poisons the cache). The refresh runs through fetchLive (the outbound funnel),
// so the negative breaker gates and learns from a refresh exactly as it does from a
// miss — a stale-while-revalidate refresh against a tripped instance is spared, and a
// refresh that fails trips the breaker for the next consumer.
func (c *SearchCache) triggerSWR(ctx context.Context, instanceID int64, cfg map[string]string, builtEpoch uint64, live liveSearchFn, q search.Query, key string) {
	bg := context.WithoutCancel(ctx)
	go func() {
		rctx, cancel := context.WithTimeout(bg, swrRefreshTimeout)
		defer cancel()
		_, _, _ = c.sf.Do(swrKey(cacheFlightKey(key, builtEpoch)), func() (any, error) {
			releases, err := c.fetchLive(rctx, instanceID, live, q)
			if err != nil {
				// Success-only: leave the old entry; do not cache the error.
				return struct{}{}, err
			}
			c.storeBestEffort(rctx, instanceID, cfg, builtEpoch, q, key, releases)
			return struct{}{}, nil
		})
	}()
}

// swrKey namespaces the refresh-ahead singleflight key so a background refresh never
// coalesces with a real cache miss on the same cache key (their flights return
// incompatible value types).
func swrKey(key string) string {
	return "swr:" + key
}

// cacheFlightKey scopes a singleflight key to the instance's invalidation epoch
// captured at the moment the caller resolved its engine (builtEpoch), so a request
// from a NEWER epoch can never coalesce onto — and receive the result of — an
// in-flight computation started under an OLDER, now-stale epoch. Without this, a
// miss driven under a stale epoch could be served (not just written, which
// storeBestEffort's own epoch check already blocks) to a follower whose own epoch
// is current, resurrecting stale releases across an invalidation. The underlying DB
// cache key (store.Fetch/liveAndStore) is intentionally left epoch-free — only the
// in-memory flight coalescing point needs this.
func cacheFlightKey(key string, builtEpoch uint64) string {
	return key + "@" + strconv.FormatUint(builtEpoch, 10)
}

// isContextError reports whether err is (or wraps) a context cancellation or deadline —
// the two shapes a singleflight leader's aborted request can leave in a coalesced
// follower's flight result. Used by serveMiss to decide whether an inherited flight
// error is the leader's (context error, our ctx live) and so should be retried on our
// own context rather than returned as the follower's failure.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// decodeReleases unmarshals a cached payload. The error wraps ONLY the cache key —
// never the payload — so a malformed row can never leak a passkey-bearing link.
func decodeReleases(payload []byte, key string) ([]*normalizer.Release, error) {
	var releases []*normalizer.Release
	if err := json.Unmarshal(payload, &releases); err != nil {
		return nil, decodeError(key, err)
	}
	return releases, nil
}
