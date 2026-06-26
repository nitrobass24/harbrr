package registry

import (
	"context"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func seedTTL() ttlConfig {
	return ttlConfig{rss: 5 * time.Minute, keyword: 30 * time.Minute, thin: 2 * time.Minute, thinThreshold: 5}
}

// TestSearchCacheConfigRoundTrip proves SetConfig both swaps the live tuning and
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
	if err := sc.SetConfig(ctx, want); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if sc.Config() != want {
		t.Errorf("after SetConfig Config = %+v, want %+v", sc.Config(), want)
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

// TestSearchCacheConfigValidation proves invalid configs are rejected and leave the
// live tuning untouched.
func TestSearchCacheConfigValidation(t *testing.T) {
	t.Parallel()

	sc, _, _ := testCache(t, seedTTL(), 80)
	before := sc.Config()
	for _, bad := range []CacheConfigView{
		{RSSTTL: 0, KeywordTTL: time.Minute, ThinTTL: time.Minute},
		{RSSTTL: time.Minute, KeywordTTL: time.Minute, ThinTTL: time.Minute, RefreshAheadPct: 150},
		{RSSTTL: time.Minute, KeywordTTL: time.Minute, ThinTTL: time.Minute, ThinThreshold: -1},
	} {
		if err := sc.SetConfig(context.Background(), bad); err == nil {
			t.Errorf("SetConfig(%+v) = nil, want validation error", bad)
		}
	}
	if sc.Config() != before {
		t.Errorf("Config changed after a rejected SetConfig: %+v != %+v", sc.Config(), before)
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

	base := CacheConfigView{RSSTTL: 5 * time.Minute, KeywordTTL: 30 * time.Minute, ThinTTL: 2 * time.Minute, ThinThreshold: 5}

	disabled := base
	disabled.Enabled = false
	if err := sc.SetConfig(ctx, disabled); err != nil {
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

	enabled := base
	enabled.Enabled = true
	if err := sc.SetConfig(ctx, enabled); err != nil {
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
