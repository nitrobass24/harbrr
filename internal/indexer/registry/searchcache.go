package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
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

// instanceSnapshot reads instanceID's current hit/miss/suppressed counts (zeroes for
// an instance with no recorded traffic).
func (c *SearchCache) instanceSnapshot(instanceID int64) (hits, misses, suppressed int64) {
	v, ok := c.instCounters.Load(instanceID)
	if !ok {
		return 0, 0, 0
	}
	ic, _ := v.(*instanceCounters)
	return ic.hits.Load(), ic.misses.Load(), ic.suppressed.Load()
}

// NewSearchCache builds the cache layer. db is the shared store handle, t the
// initial (config-seeded) tuning, clock the reference clock, and log a logger that
// only ever sees cache keys and redacted errors. The tuning is held atomically so
// SetConfig can swap it at runtime.
func NewSearchCache(db dbinterface.Querier, t cacheTuning, clock func() time.Time, log zerolog.Logger) *SearchCache {
	if clock == nil {
		clock = time.Now
	}
	c := &SearchCache{
		db:           db,
		clock:        clock,
		log:          log,
		touchPending: make(map[string]pendingTouch),
		breaker:      newNegativeBreaker(),
		announced:    newAnnounceWindow(),
	}
	c.tuning.Store(&t)
	return c
}

// SetAnnounceSink installs the cross-seed announce source tap. It is a setter (not a
// constructor arg) because the announce service is built after the cache in cmd/harbrr;
// a nil sink leaves the tap a no-op. Called once at wiring time, before serving.
func (c *SearchCache) SetAnnounceSink(sink AnnounceSink) { c.announceSink = sink }

// SearchCacheParams carries the resolved TTL tiers and refresh-ahead threshold a
// caller (cmd/harbrr) reads from config to build a SearchCache without reaching
// into the unexported ttlConfig.
type SearchCacheParams struct {
	Enabled         bool
	RSSTTL          time.Duration
	KeywordTTL      time.Duration
	ThinTTL         time.Duration
	ThinThreshold   int
	RefreshAheadPct int
	NegativeTTL     time.Duration
	CleanupInterval time.Duration
}

// NewSearchCacheWithParams builds a SearchCache from config-resolved tiers. It is
// the exported entry point for cmd/harbrr; NewSearchCache stays internal so the
// ttlConfig tier struct does not leak across the package boundary.
func NewSearchCacheWithParams(db dbinterface.Querier, p SearchCacheParams, clock func() time.Time, log zerolog.Logger) *SearchCache {
	t := cacheTuning{
		enabled:   p.Enabled,
		ttl:       ttlConfig{rss: p.RSSTTL, keyword: p.KeywordTTL, thin: p.ThinTTL, thinThreshold: p.ThinThreshold, negative: p.NegativeTTL},
		refreshAt: p.RefreshAheadPct,
		cleanup:   p.CleanupInterval,
	}
	return NewSearchCache(db, t, clock, log)
}

// wrap decorates an indexer with this cache. instanceID keys the entries; cfg
// carries the per-instance "cache_ttl" override resolveTTL reads.
func (c *SearchCache) wrap(inner torznabhttp.Indexer, instanceID int64, cfg map[string]string) torznabhttp.Indexer {
	return &cachedIndexer{Indexer: inner, cache: c, instanceID: instanceID, cfg: cfg}
}

// cachedIndexer is a torznabhttp.Indexer decorator that adds cache-aside behavior to
// Search only. Embedding the interface forwards Info/Capabilities/NeedsResolver/
// DownloadNeedsAuth/Grab to the real adapter unchanged, so /dl rewriting and grabs
// keep working on cache hits (the cached value is the pre-/dl slice).
type cachedIndexer struct {
	torznabhttp.Indexer
	cache      *SearchCache
	instanceID int64
	cfg        map[string]string
}

// SupportsOffsetPaging explicitly delegates to the wrapped indexer's OffsetPager
// capability. This MUST be hand-written: cachedIndexer embeds the torznabhttp.Indexer
// INTERFACE, which does not promote a method that isn't on that interface, so without
// this the *cachedIndexer the registry returns would NOT satisfy torznabhttp.OffsetPager
// and the handler would take the non-paging (local-slice) branch — double-offsetting a
// driver that already paged upstream. Delegating here makes the cache layer and the
// handler read the SAME signal off the same wrapped adapter.
func (c *cachedIndexer) SupportsOffsetPaging() bool {
	return supportsOffsetPaging(c.Indexer)
}

// supportsOffsetPaging reports whether inner forwards offset/limit upstream. It is the
// single type-assert both the cache-key path (which keys per-page only for paging
// drivers) and the cachedIndexer delegation use, so the two can never disagree.
func supportsOffsetPaging(inner torznabhttp.Indexer) bool {
	if p, ok := inner.(torznabhttp.OffsetPager); ok {
		return p.SupportsOffsetPaging()
	}
	return false
}

// Search overrides only the search seam, routing through the cache — unless caching
// is currently disabled (the runtime toggle), in which case it passes straight
// through to the live search with no read or write-back.
func (c *cachedIndexer) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	if !c.cache.tuning.Load().enabled {
		return c.cache.fetchLive(ctx, c.Indexer, q)
	}
	return c.cache.search(ctx, c.instanceID, c.cfg, c.Indexer, q)
}

// search is the cache-aside read path. nocache bypasses the cache entirely (live
// search + success-only write-back). Otherwise a live, unexpired hit is served
// immediately (Touch + optional refresh-ahead async); a miss runs the live search
// under singleflight and stores the result best-effort. A Fetch error degrades open
// (falls through to a live search) and never fails the user's search.
func (c *SearchCache) search(ctx context.Context, instanceID int64, cfg map[string]string, inner torznabhttp.Indexer, q search.Query) ([]*normalizer.Release, error) {
	// A paging-capable driver forwards offset/limit upstream, so each page is a distinct
	// outbound request and gets its own cache entry; a non-paging driver keys page-free
	// (one fetch serves every locally-sliced page), preserving the pre-paging key.
	key := buildSearchCacheKey(instanceID, q, supportsOffsetPaging(inner))

	if torznabhttp.CacheBypass(ctx) {
		return c.liveAndStoreRecording(ctx, instanceID, cfg, inner, q, key)
	}

	entry, found, err := c.store.Fetch(ctx, c.db, key, c.clock())
	if err != nil {
		// Degrade open: a read failure must never fail the user's search.
		c.log.Warn().Str("cache_key", key).Str("error", apphttp.RedactError(err)).
			Msg("registry: search cache fetch failed; serving live")
		return c.fetchLive(ctx, inner, q)
	}
	if found {
		return c.serveHit(ctx, instanceID, cfg, inner, q, key, entry)
	}
	return c.missPath(ctx, instanceID, cfg, inner, q, key)
}

// missPath drives a cache miss with the negative-result circuit breaker around it.
// If the breaker is open for the instance, it short-circuits to the recorded error
// without touching the tracker (counting one suppression). Otherwise it runs the live
// miss and, on a tracker failure, trips the breaker so the next consumer's miss is
// spared. It is the single funnel for every miss (the read-path miss and serveHit's
// corrupt-payload fallback) so both honor the breaker.
func (c *SearchCache) missPath(ctx context.Context, instanceID int64, cfg map[string]string, inner torznabhttp.Indexer, q search.Query, key string) ([]*normalizer.Release, error) {
	// Consult the breaker only while it is armed (negative window > 0); reading the
	// live config means a runtime disable (negative_ttl -> 0) stops suppression at
	// once, without waiting for already-open windows to lapse. tripBreaker self-gates
	// the same way, so disabling also halts new trips.
	if c.tuning.Load().ttl.negative > 0 {
		if rerr := c.breaker.replay(instanceID, c.clock()); rerr != nil {
			c.breakerSuppressed.Add(1)
			c.counters(instanceID).suppressed.Add(1)
			return nil, rerr
		}
	}
	releases, err := c.serveMiss(ctx, instanceID, cfg, inner, q, key)
	if err != nil {
		c.tripBreaker(ctx, instanceID, err)
		return nil, err
	}
	return releases, nil
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
	until, ok := classifyBreakerError(err, c.tuning.Load().ttl.negative, c.clock())
	if !ok {
		return
	}
	c.breaker.trip(instanceID, until, err)
}

// serveHit returns the decoded cached slice immediately, bumps the hit counters,
// records a Touch (async, best-effort), and fires a single refresh-ahead when the
// entry is past its refresh threshold.
func (c *SearchCache) serveHit(ctx context.Context, instanceID int64, cfg map[string]string, inner torznabhttp.Indexer, q search.Query, key string, entry database.SearchCacheEntry) ([]*normalizer.Release, error) {
	releases, err := decodeReleases(entry.ResultsJSON, key)
	if err != nil {
		// A corrupt payload is treated as a miss: never fail the search over it.
		c.log.Warn().Str("cache_key", key).Str("error", apphttp.RedactError(err)).
			Msg("registry: search cache decode failed; serving live")
		return c.missPath(ctx, instanceID, cfg, inner, q, key)
	}
	c.hits.Add(1)
	c.counters(instanceID).hits.Add(1)
	c.recordCacheInfo(ctx, torznabhttp.CacheInfo{ETag: payloadETag(entry.ResultsJSON), ExpiresAt: entry.ExpiresAt})
	c.recordTouch(key)
	if c.shouldRefreshAhead(entry) {
		c.triggerSWR(ctx, instanceID, cfg, inner, q, key)
	}
	return releases, nil
}

// serveMiss runs the live search under singleflight (so concurrent identical misses
// drive the tracker once), stores the result best-effort, and returns it. The
// double-check inside the flight lets a request that lost the race read a freshly
// stored entry instead of re-searching.
func (c *SearchCache) serveMiss(ctx context.Context, instanceID int64, cfg map[string]string, inner torznabhttp.Indexer, q search.Query, key string) ([]*normalizer.Release, error) {
	c.misses.Add(1)
	c.counters(instanceID).misses.Add(1)
	// The flight returns ([]*normalizer.Release, error); the inner error is already
	// wrapped by liveAndStore/the adapter, so it is returned unwrapped here.
	v, err, _ := c.sf.Do(key, func() (any, error) {
		if entry, found, ferr := c.store.Fetch(ctx, c.db, key, c.clock()); ferr == nil && found {
			if releases, derr := decodeReleases(entry.ResultsJSON, key); derr == nil {
				info := torznabhttp.CacheInfo{ETag: payloadETag(entry.ResultsJSON), ExpiresAt: entry.ExpiresAt}
				return missResult{releases: releases, info: info}, nil
			}
		}
		releases, info, lerr := c.liveAndStore(ctx, instanceID, cfg, inner, q, key)
		if lerr != nil {
			return nil, lerr
		}
		return missResult{releases: releases, info: info}, nil
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // already wrapped by liveAndStore/adapter; no key/payload to add.
	}
	res, ok := v.(missResult)
	if !ok {
		// Defensive: a value of an unexpected type can only mean this miss coalesced
		// onto a flight that returned something else. Never serve an empty success on
		// a type mismatch — run our own live search instead.
		return c.liveAndStoreRecording(ctx, instanceID, cfg, inner, q, key)
	}
	// Record per CALLER, outside the flight, so every coalesced miss fills its own sink.
	c.recordCacheInfo(ctx, res.info)
	return res.releases, nil
}

// liveAndStore runs the live search and, on success, writes the result back
// best-effort (a store failure never fails the search), returning the releases and the
// freshly stored entry's HTTP validators. An inner error is returned and never cached.
// It does NOT record into the request sink — the caller does (per-caller, so a
// singleflight follower also gets the validators).
func (c *SearchCache) liveAndStore(ctx context.Context, instanceID int64, cfg map[string]string, inner torznabhttp.Indexer, q search.Query, key string) ([]*normalizer.Release, torznabhttp.CacheInfo, error) {
	releases, err := c.fetchLive(ctx, inner, q)
	if err != nil {
		return nil, torznabhttp.CacheInfo{}, err
	}
	etag, expiresAt := c.storeBestEffort(ctx, instanceID, cfg, q, key, releases)
	return releases, torznabhttp.CacheInfo{ETag: etag, ExpiresAt: expiresAt}, nil
}

// liveAndStoreRecording is liveAndStore for the synchronous, non-flight callers (the
// nocache bypass path and the defensive miss fallback): it records the validators into
// this caller's own sink. The singleflight miss path instead records outside the flight
// so every coalesced caller is covered.
func (c *SearchCache) liveAndStoreRecording(ctx context.Context, instanceID int64, cfg map[string]string, inner torznabhttp.Indexer, q search.Query, key string) ([]*normalizer.Release, error) {
	releases, info, err := c.liveAndStore(ctx, instanceID, cfg, inner, q, key)
	if err != nil {
		return nil, err //nolint:wrapcheck // already wrapped by the adapter; no key/payload to add.
	}
	c.recordCacheInfo(ctx, info)
	return releases, nil
}

// fetchLive calls the wrapped indexer's live Search. The error is already wrapped
// with the indexer id by the adapter; the caller redacts it.
func (c *SearchCache) fetchLive(ctx context.Context, inner torznabhttp.Indexer, q search.Query) ([]*normalizer.Release, error) {
	return inner.Search(ctx, q) //nolint:wrapcheck // the adapter already wraps with the indexer id; re-wrapping would double-wrap.
}

// storeBestEffort encodes and upserts the result. It resolves the TTL from the raw
// engine count (the slice is pre-dedupe/pre-filter, so this count can exceed what
// the user ultimately sees — the thin clamp is measured on raw results, by design).
// A non-positive TTL or any store error is logged (key only) and swallowed. It returns
// the content ETag and expiry so the synchronous caller can surface the conditional-GET
// validators; an encode failure returns an empty etag (no validator emitted). The
// validators are returned even if the Store write fails — the response content is still
// valid, and an unstored entry simply misses on the next request.
func (c *SearchCache) storeBestEffort(ctx context.Context, instanceID int64, cfg map[string]string, q search.Query, key string, releases []*normalizer.Release) (string, time.Time) {
	// Derive the "what's new" announce stream from this write-back BEFORE the store, so
	// the prior cached entry (the one we overwrite) is still readable for the GUID diff.
	c.tapAnnounce(ctx, instanceID, q, key, releases)

	payload, err := json.Marshal(releases)
	if err != nil {
		c.log.Warn().Str("cache_key", key).Str("error", apphttp.RedactError(err)).
			Msg("registry: search cache encode failed; not caching")
		return "", time.Time{}
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
	}
	return payloadETag(payload), entry.ExpiresAt
}

// payloadETag is a strong HTTP validator over the cached payload: a quoted hex SHA-256
// of the serialized release slice. It changes iff the result set changes, so a client
// holding it can be answered with 304 while the cached answer is unchanged.
func payloadETag(payload []byte) string {
	sum := sha256.Sum256(payload)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// recordCacheInfo surfaces the served entry's HTTP validators to the feed handler via
// the request's CacheInfo sink (a no-op when there is none — the JSON API and the
// detached SWR refresh carry none). An empty etag records nothing. It is called per
// CALLER, outside the singleflight, so coalesced misses each fill their own sink.
func (c *SearchCache) recordCacheInfo(ctx context.Context, info torznabhttp.CacheInfo) {
	if info.ETag == "" {
		return
	}
	torznabhttp.RecordCacheInfo(ctx, info)
}

// missResult is the singleflight return for a cache miss: the released slice plus the
// validators to surface. The flight leader computes both; every coalesced caller then
// records the validators into its own request sink after the flight returns (recording
// inside the flight would fill only the leader's sink).
type missResult struct {
	releases []*normalizer.Release
	info     torznabhttp.CacheInfo
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
// DEDICATED refresh key (swr:<key>) so at most one refresh runs per key even across
// many concurrent stale hits, while NEVER sharing a flight with a real cache miss on
// the same key (the two return incompatible value types). The goroutine detaches
// from the request (WithoutCancel) but is bounded by a timeout. The write-back is
// success-only: an error leaves the existing entry intact (never poisons the cache).
func (c *SearchCache) triggerSWR(ctx context.Context, instanceID int64, cfg map[string]string, inner torznabhttp.Indexer, q search.Query, key string) {
	bg := context.WithoutCancel(ctx)
	go func() {
		rctx, cancel := context.WithTimeout(bg, swrRefreshTimeout)
		defer cancel()
		_, _, _ = c.sf.Do(swrKey(key), func() (any, error) {
			releases, err := c.fetchLive(rctx, inner, q)
			if err != nil {
				// Success-only: leave the old entry; do not cache the error.
				return struct{}{}, err
			}
			c.storeBestEffort(rctx, instanceID, cfg, q, key, releases)
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

// decodeReleases unmarshals a cached payload. The error wraps ONLY the cache key —
// never the payload — so a malformed row can never leak a passkey-bearing link.
func decodeReleases(payload []byte, key string) ([]*normalizer.Release, error) {
	var releases []*normalizer.Release
	if err := json.Unmarshal(payload, &releases); err != nil {
		return nil, decodeError(key, err)
	}
	return releases, nil
}
