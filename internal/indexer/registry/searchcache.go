package registry

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/singleflight"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/web/torznab"
)

// swrRefreshTimeout bounds a stale-while-revalidate background refresh so a slow
// tracker can never leak a goroutine for the full request lifetime.
const swrRefreshTimeout = 30 * time.Second

// SearchCache is the registry-wide cache-aside layer for indexer searches. It
// holds the SQLite store, a singleflight group (so concurrent identical misses
// drive the tracker exactly once), the resolved TTL tiers, the refresh-ahead
// threshold, a clock, and a logger, plus process-lifetime hit/miss counters.
//
// SECRETS-AT-REST: the store's results_json holds the FULL pre-/dl-rewrite release
// slice, whose Link/Magnet embed passkeys for some trackers. This layer NEVER logs
// the payload, a release, or a link — only the cache_key (a SHA-256 hash) and a
// redacted error. The SWR goroutine is the most dangerous spot; it follows the same
// rule.
type SearchCache struct {
	store     database.SearchCacheStore
	db        dbinterface.Execer
	sf        singleflight.Group
	ttl       ttlConfig
	refreshAt int // refresh-ahead percentage of TTL (e.g. 80)
	clock     func() time.Time
	log       zerolog.Logger

	// hits/misses are process-lifetime (non-persistent) counters for the hit-ratio
	// metric the stats endpoint exposes; they reset on restart.
	hits   atomic.Int64
	misses atomic.Int64
}

// NewSearchCache builds the cache layer. db is the shared store handle, ttl the
// resolved tiers, refreshAheadPct the percentage of a TTL after which a live hit
// triggers a background refresh, clock the reference clock, and log a logger that
// only ever sees cache keys and redacted errors.
func NewSearchCache(db dbinterface.Execer, ttl ttlConfig, refreshAheadPct int, clock func() time.Time, log zerolog.Logger) *SearchCache {
	if clock == nil {
		clock = time.Now
	}
	return &SearchCache{
		db:        db,
		ttl:       ttl,
		refreshAt: refreshAheadPct,
		clock:     clock,
		log:       log,
	}
}

// wrap decorates an indexer with this cache. instanceID keys the entries; cfg
// carries the per-instance "cache_ttl" override resolveTTL reads.
func (c *SearchCache) wrap(inner torznab.Indexer, instanceID int64, cfg map[string]string) torznab.Indexer {
	return &cachedIndexer{Indexer: inner, cache: c, instanceID: instanceID, cfg: cfg}
}

// cachedIndexer is a torznab.Indexer decorator that adds cache-aside behavior to
// Search only. Embedding the interface forwards Info/Capabilities/NeedsResolver/
// DownloadNeedsAuth/Grab to the real adapter unchanged, so /dl rewriting and grabs
// keep working on cache hits (the cached value is the pre-/dl slice).
type cachedIndexer struct {
	torznab.Indexer
	cache      *SearchCache
	instanceID int64
	cfg        map[string]string
}

// Search overrides only the search seam, routing through the cache.
func (c *cachedIndexer) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	return c.cache.search(ctx, c.instanceID, c.cfg, c.Indexer, q)
}

// search is the cache-aside read path. nocache bypasses the cache entirely (live
// search + success-only write-back). Otherwise a live, unexpired hit is served
// immediately (Touch + optional refresh-ahead async); a miss runs the live search
// under singleflight and stores the result best-effort. A Fetch error degrades open
// (falls through to a live search) and never fails the user's search.
func (c *SearchCache) search(ctx context.Context, instanceID int64, cfg map[string]string, inner torznab.Indexer, q search.Query) ([]*normalizer.Release, error) {
	key := buildSearchCacheKey(instanceID, q)

	if torznab.CacheBypass(ctx) {
		return c.liveAndStore(ctx, instanceID, cfg, inner, q, key)
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
	return c.serveMiss(ctx, instanceID, cfg, inner, q, key)
}

// serveHit returns the decoded cached slice immediately, bumps the hit counters,
// records a Touch (async, best-effort), and fires a single refresh-ahead when the
// entry is past its refresh threshold.
func (c *SearchCache) serveHit(ctx context.Context, instanceID int64, cfg map[string]string, inner torznab.Indexer, q search.Query, key string, entry database.SearchCacheEntry) ([]*normalizer.Release, error) {
	releases, err := decodeReleases(entry.ResultsJSON, key)
	if err != nil {
		// A corrupt payload is treated as a miss: never fail the search over it.
		c.log.Warn().Str("cache_key", key).Str("error", apphttp.RedactError(err)).
			Msg("registry: search cache decode failed; serving live")
		return c.serveMiss(ctx, instanceID, cfg, inner, q, key)
	}
	c.hits.Add(1)
	c.touchAsync(key)
	if c.shouldRefreshAhead(entry) {
		c.triggerSWR(ctx, instanceID, cfg, inner, q, key)
	}
	return releases, nil
}

// serveMiss runs the live search under singleflight (so concurrent identical misses
// drive the tracker once), stores the result best-effort, and returns it. The
// double-check inside the flight lets a request that lost the race read a freshly
// stored entry instead of re-searching.
func (c *SearchCache) serveMiss(ctx context.Context, instanceID int64, cfg map[string]string, inner torznab.Indexer, q search.Query, key string) ([]*normalizer.Release, error) {
	c.misses.Add(1)
	// The flight returns ([]*normalizer.Release, error); the inner error is already
	// wrapped by liveAndStore/the adapter, so it is returned unwrapped here.
	v, err, _ := c.sf.Do(key, func() (any, error) {
		if entry, found, ferr := c.store.Fetch(ctx, c.db, key, c.clock()); ferr == nil && found {
			if releases, derr := decodeReleases(entry.ResultsJSON, key); derr == nil {
				return releases, nil
			}
		}
		return c.liveAndStore(ctx, instanceID, cfg, inner, q, key)
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // already wrapped by liveAndStore/adapter; no key/payload to add.
	}
	releases, ok := v.([]*normalizer.Release)
	if !ok {
		// Defensive: a value of an unexpected type can only mean this miss coalesced
		// onto a flight that returned something else. Never serve an empty success on
		// a type mismatch — run our own live search instead.
		return c.liveAndStore(ctx, instanceID, cfg, inner, q, key)
	}
	return releases, nil
}

// liveAndStore runs the live search and, on success, writes the result back
// best-effort (a store failure never fails the search). An inner error is returned
// and never cached.
func (c *SearchCache) liveAndStore(ctx context.Context, instanceID int64, cfg map[string]string, inner torznab.Indexer, q search.Query, key string) ([]*normalizer.Release, error) {
	releases, err := c.fetchLive(ctx, inner, q)
	if err != nil {
		return nil, err
	}
	c.storeBestEffort(ctx, instanceID, cfg, q, key, releases)
	return releases, nil
}

// fetchLive calls the wrapped indexer's live Search. The error is already wrapped
// with the indexer id by the adapter; the caller redacts it.
func (c *SearchCache) fetchLive(ctx context.Context, inner torznab.Indexer, q search.Query) ([]*normalizer.Release, error) {
	return inner.Search(ctx, q) //nolint:wrapcheck // the adapter already wraps with the indexer id; re-wrapping would double-wrap.
}

// storeBestEffort encodes and upserts the result. It resolves the TTL from the raw
// engine count (the slice is pre-dedupe/pre-filter, so this count can exceed what
// the user ultimately sees — the thin clamp is measured on raw results, by design).
// A non-positive TTL or any store error is logged (key only) and swallowed.
func (c *SearchCache) storeBestEffort(ctx context.Context, instanceID int64, cfg map[string]string, q search.Query, key string, releases []*normalizer.Release) {
	payload, err := json.Marshal(releases)
	if err != nil {
		c.log.Warn().Str("cache_key", key).Str("error", apphttp.RedactError(err)).
			Msg("registry: search cache encode failed; not caching")
		return
	}
	now := c.clock()
	ttl := c.ttl.resolveTTL(cfg, q, len(releases))
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
}

// touchAsync records a served hit without blocking the response. A Touch failure is
// logged (key only) and otherwise ignored — the counter is best-effort.
func (c *SearchCache) touchAsync(key string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), swrRefreshTimeout)
		defer cancel()
		if err := c.store.Touch(ctx, c.db, key, c.clock()); err != nil {
			c.log.Warn().Str("cache_key", key).Str("error", apphttp.RedactError(err)).
				Msg("registry: search cache touch failed")
		}
	}()
}

// shouldRefreshAhead reports whether a hit is old enough to trigger a background
// refresh: it is true once the fraction of the entry's lifetime that has elapsed
// reaches refreshAt percent. It uses cached_at/expires_at (the entry's real
// lifetime), never now+ttl. A non-positive percentage disables refresh-ahead.
func (c *SearchCache) shouldRefreshAhead(entry database.SearchCacheEntry) bool {
	if c.refreshAt <= 0 {
		return false
	}
	lifetime := entry.ExpiresAt.Sub(entry.CachedAt)
	if lifetime <= 0 {
		return false
	}
	elapsed := c.clock().Sub(entry.CachedAt)
	return elapsed*100 >= lifetime*time.Duration(c.refreshAt)
}

// triggerSWR fires one background refresh for key, guarded by singleflight on a
// DEDICATED refresh key (swr:<key>) so at most one refresh runs per key even across
// many concurrent stale hits, while NEVER sharing a flight with a real cache miss on
// the same key (the two return incompatible value types). The goroutine detaches
// from the request (WithoutCancel) but is bounded by a timeout. The write-back is
// success-only: an error leaves the existing entry intact (never poisons the cache).
func (c *SearchCache) triggerSWR(ctx context.Context, instanceID int64, cfg map[string]string, inner torznab.Indexer, q search.Query, key string) {
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
