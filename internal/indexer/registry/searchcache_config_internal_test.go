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
		NegativeTTL: &v.NegativeTTL, CleanupInterval: &v.CleanupInterval,
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
		NegativeTTL: 30 * time.Second, CleanupInterval: 2 * time.Hour,
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
	reset := cacheTuning{enabled: true, ttl: seed, refreshAt: 80, cleanup: time.Hour}
	sc.tuning.Store(&reset)
	if err := sc.LoadOverrides(ctx); err != nil {
		t.Fatalf("LoadOverrides: %v", err)
	}
	if sc.Config() != want {
		t.Errorf("after LoadOverrides Config = %+v, want persisted %+v", sc.Config(), want)
	}
}

// TestCleanupIntervalRuntimeTunable proves cleanup_interval is live-tunable: an
// UpdateConfig swaps both the API view and the CleanupInterval() the ticker reads.
func TestCleanupIntervalRuntimeTunable(t *testing.T) {
	t.Parallel()
	sc, _, _ := testCache(t, seedTTL(), 80)
	if got := sc.CleanupInterval(); got != time.Hour {
		t.Fatalf("seed CleanupInterval = %v, want 1h", got)
	}
	next := 15 * time.Minute
	v, err := sc.UpdateConfig(context.Background(), CacheConfigPatch{CleanupInterval: &next})
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if v.CleanupInterval != next || sc.CleanupInterval() != next {
		t.Fatalf("after update: view=%v accessor=%v, want %v", v.CleanupInterval, sc.CleanupInterval(), next)
	}
}

// TestSearchCacheConfigValidation proves invalid configs are rejected (wrapping
// ErrInvalidCacheConfig) and leave the live tuning untouched.
func TestSearchCacheConfigValidation(t *testing.T) {
	t.Parallel()

	sc, _, _ := testCache(t, seedTTL(), 80)
	before := sc.Config()
	for _, bad := range []CacheConfigView{
		{RSSTTL: 0, KeywordTTL: time.Minute, ThinTTL: time.Minute, CleanupInterval: time.Hour},
		{RSSTTL: time.Minute, KeywordTTL: time.Minute, ThinTTL: time.Minute, RefreshAheadPct: 150, CleanupInterval: time.Hour},
		{RSSTTL: time.Minute, KeywordTTL: time.Minute, ThinTTL: time.Minute, ThinThreshold: -1, CleanupInterval: time.Hour},
		{RSSTTL: time.Minute, KeywordTTL: time.Minute, ThinTTL: time.Minute, CleanupInterval: 0},
		// Below the floor (MinCleanupInterval) is rejected too, not just non-positive —
		// a sub-second reap cadence would spin the cleanup loop.
		{RSSTTL: time.Minute, KeywordTTL: time.Minute, ThinTTL: time.Minute, CleanupInterval: time.Millisecond},
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

// TestLoadOverridesCorruptionGuards proves LoadOverrides' per-key guards (the
// production code already has them; this pins the behavior with tests -
// autobrr/harbrr#351): a malformed or non-positive persisted TTL is ignored and the
// config-file-seeded value stands; an out-of-range field fails Validate and the
// WHOLE overlay is discarded (not just the bad field); the one field that
// legitimately overlays zero (negative_ttl, breaker-disable) still applies; and a
// fully valid overlay applies.
func TestLoadOverridesCorruptionGuards(t *testing.T) {
	t.Parallel()

	// negSeed starts negative_ttl non-zero so a stored "0s" overlay is observably a
	// change, not just the already-zero default.
	negSeed := seedTTL()
	negSeed.negative = 30 * time.Second

	tests := []struct {
		name string
		ttl  ttlConfig
		kv   map[string]string
		want func(seed CacheConfigView) CacheConfigView
	}{
		{
			name: "unparseable TTL duration ignored",
			ttl:  seedTTL(),
			kv:   map[string]string{keyCacheRSSTTL: "banana"},
			want: func(v CacheConfigView) CacheConfigView { return v },
		},
		{
			name: "zero TTL duration ignored",
			ttl:  seedTTL(),
			kv:   map[string]string{keyCacheKeywordTTL: "0s"},
			want: func(v CacheConfigView) CacheConfigView { return v },
		},
		{
			name: "negative TTL duration ignored",
			ttl:  seedTTL(),
			kv:   map[string]string{keyCacheThinTTL: "-5m"},
			want: func(v CacheConfigView) CacheConfigView { return v },
		},
		{
			name: "negative_ttl zero applies (breaker-disable roundtrip)",
			ttl:  negSeed,
			kv:   map[string]string{keyCacheNegativeTTL: "0s"},
			want: func(v CacheConfigView) CacheConfigView { v.NegativeTTL = 0; return v },
		},
		{
			name: "out-of-range refresh_ahead_pct rejects the entire overlay",
			ttl:  seedTTL(),
			// rss_ttl would apply cleanly on its own; it must be discarded too, since
			// Validate rejects the WHOLE merged view, not field-by-field.
			kv:   map[string]string{keyCacheRefreshAhead: "150", keyCacheRSSTTL: "9m"},
			want: func(v CacheConfigView) CacheConfigView { return v },
		},
		{
			name: "valid overlay applies",
			ttl:  seedTTL(),
			kv:   map[string]string{keyCacheRSSTTL: "9m", keyCacheRefreshAhead: "42"},
			want: func(v CacheConfigView) CacheConfigView {
				v.RSSTTL = 9 * time.Minute
				v.RefreshAheadPct = 42
				return v
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sc, _, _ := testCache(t, tt.ttl, 80)
			ctx := context.Background()
			for k, v := range tt.kv {
				if err := (database.AppSettings{}).Set(ctx, sc.db, k, v, sc.clock()); err != nil {
					t.Fatalf("seed app_settings %s=%q: %v", k, v, err)
				}
			}
			before := sc.Config()
			if err := sc.LoadOverrides(ctx); err != nil {
				t.Fatalf("LoadOverrides: %v", err)
			}
			if want := tt.want(before); sc.Config() != want {
				t.Errorf("Config after LoadOverrides = %+v, want %+v", sc.Config(), want)
			}
		})
	}
}

// TestSearchCacheEnabledGate proves the runtime enabled toggle: disabled bypasses
// the cache entirely (every search hits the inner indexer), enabled caches.
func TestSearchCacheEnabledGate(t *testing.T) {
	t.Parallel()

	sc, instID, _ := testCache(t, seedTTL(), 0)
	inner := &fakeInner{releases: relSet("a")}
	idx := sc.probe(inner, instID, nil)
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
