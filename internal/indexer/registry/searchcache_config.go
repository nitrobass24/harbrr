package registry

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/autobrr/harbrr/internal/database"
)

// cacheTuning is the live, atomically-swapped search-cache configuration. The
// SearchCache reads it per request (resolveTTL, shouldRefreshAhead, the enabled
// gate), so the global knobs are runtime-tunable via SetConfig without a restart.
type cacheTuning struct {
	enabled   bool
	ttl       ttlConfig
	refreshAt int           // refresh-ahead percentage of TTL (e.g. 80)
	cleanup   time.Duration // how often the background ticker reaps expired entries
}

// CacheConfigView is the API-facing snapshot of the live cache tuning (durations
// are formatted to/parsed from strings at the handler boundary).
type CacheConfigView struct {
	Enabled         bool
	RSSTTL          time.Duration
	KeywordTTL      time.Duration
	ThinTTL         time.Duration
	ThinThreshold   int
	RefreshAheadPct int
	// NegativeTTL is the negative-result circuit-breaker window; 0 disables the breaker.
	NegativeTTL time.Duration
	// CleanupInterval is how often the background ticker reaps expired entries.
	CleanupInterval time.Duration
}

// CacheConfigPatch is a partial cache-config update: a nil field is left unchanged,
// and ONLY the supplied fields are persisted — so an omitted knob keeps falling back
// to the config-file seed / default (the DB stores only explicit overrides).
type CacheConfigPatch struct {
	Enabled         *bool
	RSSTTL          *time.Duration
	KeywordTTL      *time.Duration
	ThinTTL         *time.Duration
	ThinThreshold   *int
	RefreshAheadPct *int
	NegativeTTL     *time.Duration
	CleanupInterval *time.Duration
}

// app_settings keys for the cache config — a DB row overrides the config-file seed.
const (
	keyCacheEnabled       = "cache.enabled"
	keyCacheRSSTTL        = "cache.rss_ttl"
	keyCacheKeywordTTL    = "cache.keyword_ttl"
	keyCacheThinTTL       = "cache.thin_ttl"
	keyCacheThinThreshold = "cache.thin_threshold"
	keyCacheRefreshAhead  = "cache.refresh_ahead_pct"
	keyCacheNegativeTTL   = "cache.negative_ttl"
	keyCacheCleanup       = "cache.cleanup_interval"
)

// MinCleanupInterval is the smallest accepted cleanup_interval. It floors the reap
// cadence so a tiny value cannot turn the cleanup loop into a tight SQLite DELETE spin;
// it is enforced both at config validation and at the runtime read (cleanupTickInterval).
const MinCleanupInterval = time.Second

// ErrInvalidCacheConfig wraps every cache-config validation failure so the API layer
// can map it to a 400 (vs a 500 for a persistence error).
var ErrInvalidCacheConfig = errors.New("invalid cache config")

var (
	errCacheTTLPositive = fmt.Errorf("%w: rss_ttl/keyword_ttl/thin_ttl must be positive durations", ErrInvalidCacheConfig)
	errThinThreshold    = fmt.Errorf("%w: thin_threshold must be >= 0", ErrInvalidCacheConfig)
	errRefreshPct       = fmt.Errorf("%w: refresh_ahead_pct must be between 0 and 100", ErrInvalidCacheConfig)
	errNegativeTTL      = fmt.Errorf("%w: negative_ttl must be >= 0 (0 disables the breaker)", ErrInvalidCacheConfig)
	errCleanupInterval  = fmt.Errorf("%w: cleanup_interval must be at least %s", ErrInvalidCacheConfig, MinCleanupInterval)
)

func (t cacheTuning) view() CacheConfigView {
	return CacheConfigView{
		Enabled:         t.enabled,
		RSSTTL:          t.ttl.rss,
		KeywordTTL:      t.ttl.keyword,
		ThinTTL:         t.ttl.thin,
		ThinThreshold:   t.ttl.thinThreshold,
		RefreshAheadPct: t.refreshAt,
		NegativeTTL:     t.ttl.negative,
		CleanupInterval: t.cleanup,
	}
}

func (v CacheConfigView) tuning() cacheTuning {
	return cacheTuning{
		enabled:   v.Enabled,
		ttl:       ttlConfig{rss: v.RSSTTL, keyword: v.KeywordTTL, thin: v.ThinTTL, thinThreshold: v.ThinThreshold, negative: v.NegativeTTL},
		refreshAt: v.RefreshAheadPct,
		cleanup:   v.CleanupInterval,
	}
}

// Validate reports whether the proposed config is usable; the handler maps the
// returned error to a 400.
func (v CacheConfigView) Validate() error {
	switch {
	case v.RSSTTL <= 0 || v.KeywordTTL <= 0 || v.ThinTTL <= 0:
		return errCacheTTLPositive
	case v.ThinThreshold < 0:
		return errThinThreshold
	case v.RefreshAheadPct < 0 || v.RefreshAheadPct > 100:
		return errRefreshPct
	case v.NegativeTTL < 0:
		return errNegativeTTL
	case v.CleanupInterval < MinCleanupInterval:
		return errCleanupInterval
	}
	return nil
}

// Enabled reports whether caching is currently on (read by the stats endpoint and
// the per-request gate).
func (c *SearchCache) Enabled() bool { return c.tuning.Load().enabled }

// Config returns the live cache tuning (GET /api/cache/config).
func (c *SearchCache) Config() CacheConfigView { return c.tuning.Load().view() }

// CleanupInterval returns the live expired-entry reap interval. The cleanup ticker
// re-reads it each cycle so a runtime change takes effect without a restart.
func (c *SearchCache) CleanupInterval() time.Duration { return c.tuning.Load().cleanup }

// UpdateConfig applies a partial patch: it merges the supplied fields onto the live
// config, validates the result (returning a wrapped ErrInvalidCacheConfig on a bad
// value), persists ONLY the supplied fields to app_settings — so an omitted knob
// keeps falling back to its config-file/default value — and atomically swaps the new
// tuning in. The whole read-merge-validate-persist-swap is serialized by cfgMu so
// concurrent updates cannot lose each other's fields. Returns the resulting config.
func (c *SearchCache) UpdateConfig(ctx context.Context, p CacheConfigPatch) (CacheConfigView, error) {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()

	v := c.tuning.Load().view()
	kv := map[string]string{}
	if p.Enabled != nil {
		v.Enabled = *p.Enabled
		kv[keyCacheEnabled] = strconv.FormatBool(*p.Enabled)
	}
	if p.RSSTTL != nil {
		v.RSSTTL = *p.RSSTTL
		kv[keyCacheRSSTTL] = p.RSSTTL.String()
	}
	if p.KeywordTTL != nil {
		v.KeywordTTL = *p.KeywordTTL
		kv[keyCacheKeywordTTL] = p.KeywordTTL.String()
	}
	if p.ThinTTL != nil {
		v.ThinTTL = *p.ThinTTL
		kv[keyCacheThinTTL] = p.ThinTTL.String()
	}
	if p.ThinThreshold != nil {
		v.ThinThreshold = *p.ThinThreshold
		kv[keyCacheThinThreshold] = strconv.Itoa(*p.ThinThreshold)
	}
	if p.RefreshAheadPct != nil {
		v.RefreshAheadPct = *p.RefreshAheadPct
		kv[keyCacheRefreshAhead] = strconv.Itoa(*p.RefreshAheadPct)
	}
	if p.NegativeTTL != nil {
		v.NegativeTTL = *p.NegativeTTL
		kv[keyCacheNegativeTTL] = p.NegativeTTL.String()
	}
	if p.CleanupInterval != nil {
		v.CleanupInterval = *p.CleanupInterval
		kv[keyCacheCleanup] = p.CleanupInterval.String()
	}

	if err := v.Validate(); err != nil {
		return CacheConfigView{}, err
	}
	if err := c.persistConfig(ctx, kv); err != nil {
		return CacheConfigView{}, err
	}
	t := v.tuning()
	c.tuning.Store(&t)
	return v, nil
}

// persistConfig writes ONLY the given app_settings keys, in one transaction so a
// mid-write failure leaves the stored config untouched. An empty map is a no-op.
func (c *SearchCache) persistConfig(ctx context.Context, kv map[string]string) error {
	if len(kv) == 0 {
		return nil
	}
	now := c.clock()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("registry: begin cache config tx: %w", err)
	}
	store := database.AppSettings{}
	for k, val := range kv {
		if err := store.Set(ctx, tx, k, val, now); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("registry: persist cache config: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("registry: commit cache config: %w", err)
	}
	return nil
}

// LoadOverrides overlays any persisted app_settings cache.* values onto the
// config-file-seeded tuning and swaps the result in. Called once at boot after the
// cache is built from config. A malformed or invalid stored value is ignored (the
// seed stands), never fatal — operator config must not brick startup.
func (c *SearchCache) LoadOverrides(ctx context.Context) error {
	all, err := database.AppSettings{}.GetAll(ctx, c.db)
	if err != nil {
		return fmt.Errorf("registry: load cache overrides: %w", err)
	}
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	v := c.tuning.Load().view() // start from the current (config-seeded) view
	if s, ok := all[keyCacheEnabled]; ok {
		if b, err := strconv.ParseBool(s); err == nil {
			v.Enabled = b
		}
	}
	applyDur(all, keyCacheRSSTTL, &v.RSSTTL)
	applyDur(all, keyCacheKeywordTTL, &v.KeywordTTL)
	applyDur(all, keyCacheThinTTL, &v.ThinTTL)
	applyInt(all, keyCacheThinThreshold, &v.ThinThreshold)
	applyInt(all, keyCacheRefreshAhead, &v.RefreshAheadPct)
	applyDurNonNeg(all, keyCacheNegativeTTL, &v.NegativeTTL)
	applyDur(all, keyCacheCleanup, &v.CleanupInterval)
	if v.Validate() == nil { // keep the seed if an overlaid view is invalid
		t := v.tuning()
		c.tuning.Store(&t)
	}
	return nil
}

func applyDur(all map[string]string, key string, dst *time.Duration) {
	if s, ok := all[key]; ok {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			*dst = d
		}
	}
}

// applyDurNonNeg overlays a stored duration that may be zero (unlike applyDur, which
// requires a positive value). The breaker window uses it so a persisted "0s" reloads
// as "breaker disabled" rather than being ignored and falling back to the seed.
func applyDurNonNeg(all map[string]string, key string, dst *time.Duration) {
	if s, ok := all[key]; ok {
		if d, err := time.ParseDuration(s); err == nil && d >= 0 {
			*dst = d
		}
	}
}

func applyInt(all map[string]string, key string, dst *int) {
	if s, ok := all[key]; ok {
		if n, err := strconv.Atoi(s); err == nil {
			*dst = n
		}
	}
}
