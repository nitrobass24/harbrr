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
	refreshAt int // refresh-ahead percentage of TTL (e.g. 80)
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
}

// app_settings keys for the cache config — a DB row overrides the config-file seed.
const (
	keyCacheEnabled       = "cache.enabled"
	keyCacheRSSTTL        = "cache.rss_ttl"
	keyCacheKeywordTTL    = "cache.keyword_ttl"
	keyCacheThinTTL       = "cache.thin_ttl"
	keyCacheThinThreshold = "cache.thin_threshold"
	keyCacheRefreshAhead  = "cache.refresh_ahead_pct"
)

var (
	errCacheTTLPositive = errors.New("cache TTLs (rss_ttl/keyword_ttl/thin_ttl) must be positive durations")
	errThinThreshold    = errors.New("thin_threshold must be >= 0")
	errRefreshPct       = errors.New("refresh_ahead_pct must be between 0 and 100")
)

func (t cacheTuning) view() CacheConfigView {
	return CacheConfigView{
		Enabled:         t.enabled,
		RSSTTL:          t.ttl.rss,
		KeywordTTL:      t.ttl.keyword,
		ThinTTL:         t.ttl.thin,
		ThinThreshold:   t.ttl.thinThreshold,
		RefreshAheadPct: t.refreshAt,
	}
}

func (v CacheConfigView) tuning() cacheTuning {
	return cacheTuning{
		enabled:   v.Enabled,
		ttl:       ttlConfig{rss: v.RSSTTL, keyword: v.KeywordTTL, thin: v.ThinTTL, thinThreshold: v.ThinThreshold},
		refreshAt: v.RefreshAheadPct,
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
	}
	return nil
}

// Enabled reports whether caching is currently on (read by the stats endpoint and
// the per-request gate).
func (c *SearchCache) Enabled() bool { return c.tuning.Load().enabled }

// Config returns the live cache tuning (GET /api/cache/config).
func (c *SearchCache) Config() CacheConfigView { return c.tuning.Load().view() }

// SetConfig validates v, persists it to app_settings, and atomically swaps the live
// tuning, so the change both survives a restart and takes effect immediately.
func (c *SearchCache) SetConfig(ctx context.Context, v CacheConfigView) error {
	if err := v.Validate(); err != nil {
		return err
	}
	now := c.clock()
	// All six keys persist in one transaction, so a mid-write failure leaves the
	// stored config untouched (no partial overlay for the next LoadOverrides to read).
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("registry: begin cache config tx: %w", err)
	}
	store := database.AppSettings{}
	for k, val := range map[string]string{
		keyCacheEnabled:       strconv.FormatBool(v.Enabled),
		keyCacheRSSTTL:        v.RSSTTL.String(),
		keyCacheKeywordTTL:    v.KeywordTTL.String(),
		keyCacheThinTTL:       v.ThinTTL.String(),
		keyCacheThinThreshold: strconv.Itoa(v.ThinThreshold),
		keyCacheRefreshAhead:  strconv.Itoa(v.RefreshAheadPct),
	} {
		if err := store.Set(ctx, tx, k, val, now); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("registry: persist cache config: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("registry: commit cache config: %w", err)
	}
	t := v.tuning()
	c.tuning.Store(&t)
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
	v := c.Config() // start from the current (config-seeded) view
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

func applyInt(all map[string]string, key string, dst *int) {
	if s, ok := all[key]; ok {
		if n, err := strconv.Atoi(s); err == nil {
			*dst = n
		}
	}
}
