package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func seedTTL() ttlConfig {
	return ttlConfig{rss: 5 * time.Minute, keyword: 30 * time.Minute, thin: 2 * time.Minute, thinThreshold: 5}
}

// fullPatch builds a patch that sets every field — a full replace, for tests that
// overwrite the whole config.
func fullPatch(v CacheConfigView) CacheConfigPatch {
	return CacheConfigPatch{
		Enabled: &v.Enabled, RSSTTL: &v.RSSTTL, KeywordTTL: &v.KeywordTTL,
		ThinTTL: &v.ThinTTL, ThinThreshold: &v.ThinThreshold, RefreshAheadPct: &v.RefreshAheadPct,
	}
}

// TestSearchCacheConfigRoundTrip proves UpdateConfig swaps the live tuning and
// persists it (a LoadOverrides after resetting the in-memory copy restores it).
func TestSearchCacheConfigRoundTrip(t *testing.T) {
	t.Parallel()

	sc, _, _ := testCache(t, seedTTL(), 80)
	ctx := context.Background()

	if got := sc.Config(); !got.Enabled || got.RSSTTL != 5*time.Minute || got.RefreshAheadPct != 80 {
		t.Fatalf("seed Config = %+v", got)
	}

	want := CacheConfigView{
		Enabled: false, RSSTTL: 10 * time.Minute, KeywordTTL: time.Hour,
		ThinTTL: time.Minute, ThinThreshold: 3, RefreshAheadPct: 50,
	}
	if _, err := sc.UpdateConfig(ctx, fullPatch(want)); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if sc.Config() != want {
		t.Errorf("after UpdateConfig Config = %+v, want %+v", sc.Config(), want)
	}

	// Reset the in-memory tuning to the seed, then LoadOverrides must restore the
	// persisted value from app_settings.
	seed := seedTTL()
	reset := cacheTuning{enabled: true, ttl: seed, refreshAt: 80}
	sc.tuning.Store(&reset)
	if err := sc.LoadOverrides(ctx); err != nil {
		t.Fatalf("LoadOverrides: %v", err)
	}
	if sc.Config() != want {
		t.Errorf("after LoadOverrides Config = %+v, want persisted %+v", sc.Config(), want)
	}
}

// TestSearchCacheConfigValidation proves invalid configs are rejected (wrapping
// ErrInvalidCacheConfig) and leave the live tuning untouched.
func TestSearchCacheConfigValidation(t *testing.T) {
	t.Parallel()

	sc, _, _ := testCache(t, seedTTL(), 80)
	before := sc.Config()
	for _, bad := range []CacheConfigView{
		{RSSTTL: 0, KeywordTTL: time.Minute, ThinTTL: time.Minute},
		{RSSTTL: time.Minute, KeywordTTL: time.Minute, ThinTTL: time.Minute, RefreshAheadPct: 150},
		{RSSTTL: time.Minute, KeywordTTL: time.Minute, ThinTTL: time.Minute, ThinThreshold: -1},
	} {
		if _, err := sc.UpdateConfig(context.Background(), fullPatch(bad)); err == nil || !errors.Is(err, ErrInvalidCacheConfig) {
			t.Errorf("UpdateConfig(%+v) err = %v, want ErrInvalidCacheConfig", bad, err)
		}
	}
	if sc.Config() != before {
		t.Errorf("Config changed after a rejected update: %+v != %+v", sc.Config(), before)
	}
}

// TestUpdateConfigPersistsOnlyPatched proves a partial patch stores ONLY the supplied
// keys in app_settings, so omitted knobs keep falling back to the config-file seed
// (rather than being frozen as explicit DB overrides).
func TestUpdateConfigPersistsOnlyPatched(t *testing.T) {
	t.Parallel()

	sc, _, _ := testCache(t, seedTTL(), 80)
	ctx := context.Background()

	enabled := false
	if _, err := sc.UpdateConfig(ctx, CacheConfigPatch{Enabled: &enabled}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	all, err := database.AppSettings{}.GetAll(ctx, sc.db)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if _, ok := all[keyCacheEnabled]; !ok {
		t.Error("cache.enabled should be persisted")
	}
	if _, ok := all[keyCacheRSSTTL]; ok {
		t.Error("cache.rss_ttl should NOT be persisted (only the patched key)")
	}
	if len(all) != 1 {
		t.Errorf("persisted %d keys, want only the 1 patched (enabled): %v", len(all), all)
	}
	// In-memory config still reflects the seed for the untouched TTLs.
	if got := sc.Config(); got.Enabled || got.RSSTTL != 5*time.Minute {
		t.Errorf("Config = %+v, want enabled=false + seed rss 5m", got)
	}
}

// TestSearchCacheEnabledGate proves the runtime enabled toggle: disabled bypasses
// the cache entirely (every search hits the inner indexer), enabled caches.
func TestSearchCacheEnabledGate(t *testing.T) {
	t.Parallel()

	sc, instID, _ := testCache(t, seedTTL(), 0)
	inner := &fakeInner{releases: relSet("a")}
	idx := sc.wrap(inner, instID, nil)
	ctx := context.Background()

	disabled := false
	if _, err := sc.UpdateConfig(ctx, CacheConfigPatch{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := idx.Search(ctx, search.Query{Keywords: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := inner.callCount(); got != 2 {
		t.Errorf("disabled: inner calls = %d, want 2 (no caching)", got)
	}

	enabled := true
	if _, err := sc.UpdateConfig(ctx, CacheConfigPatch{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := idx.Search(ctx, search.Query{Keywords: "y"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := inner.callCount(); got != 3 {
		t.Errorf("enabled: inner calls = %d, want 3 (the 2 disabled + 1 cached miss for \"y\")", got)
	}
}
