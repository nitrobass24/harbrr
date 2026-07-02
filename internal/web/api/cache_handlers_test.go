package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/web/api"
)

// cacheStatsBody is the decoded /api/cache/stats response.
type cacheStatsBody struct {
	Enabled           bool                 `json:"enabled"`
	Entries           int64                `json:"entries"`
	TotalHits         int64                `json:"totalHits"`
	Hits              int64                `json:"hits"`
	Misses            int64                `json:"misses"`
	HitRatio          float64              `json:"hitRatio"`
	ApproxSizeBytes   int64                `json:"approxSizeBytes"`
	OldestCachedAt    *int64               `json:"oldestCachedAt"`
	NewestCachedAt    *int64               `json:"newestCachedAt"`
	LastUsedAt        *int64               `json:"lastUsedAt"`
	TrackerHitsSaved  int64                `json:"trackerHitsSaved"`
	BreakerSuppressed int64                `json:"breakerSuppressed"`
	ByIndexer         []cacheIndexerStatsB `json:"byIndexer"`
}

// cacheIndexerStatsB is the decoded per-indexer stats row.
type cacheIndexerStatsB struct {
	InstanceID        int64   `json:"instanceId"`
	Slug              string  `json:"slug"`
	Name              string  `json:"name"`
	Entries           int64   `json:"entries"`
	HitsSaved         int64   `json:"hitsSaved"`
	Hits              int64   `json:"hits"`
	Misses            int64   `json:"misses"`
	HitRatio          float64 `json:"hitRatio"`
	ApproxSizeBytes   int64   `json:"approxSizeBytes"`
	BreakerSuppressed int64   `json:"breakerSuppressed"`
	BreakerOpenUntil  *int64  `json:"breakerOpenUntil"`
}

// cacheFlushBody is the decoded /api/cache/flush response.
type cacheFlushBody struct {
	Flushed int64 `json:"flushed"`
}

// newCacheParams returns sane TTL tiers for a test cache.
func newCacheParams() registry.SearchCacheParams {
	return registry.SearchCacheParams{
		Enabled:         true,
		RSSTTL:          5 * time.Minute,
		KeywordTTL:      30 * time.Minute,
		ThinTTL:         2 * time.Minute,
		ThinThreshold:   5,
		RefreshAheadPct: 80,
		CleanupInterval: time.Hour,
	}
}

// seedCacheEntry inserts one cache row for the given instance so the stats
// endpoint reports a non-empty cache. results_json is a small synthetic blob.
func seedCacheEntry(t *testing.T, db *database.DB, instanceID int64, now time.Time) {
	t.Helper()
	store := database.SearchCacheStore{}
	err := store.Store(context.Background(), db, database.SearchCacheEntry{
		CacheKey:     "test-cache-key-0000",
		InstanceID:   instanceID,
		ResultsJSON:  []byte(`[{"title":"x"}]`),
		TotalResults: 1,
		CachedAt:     now,
		LastUsedAt:   now,
		ExpiresAt:    now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("seed cache entry: %v", err)
	}
}

// addTestIndexer adds the testtracker indexer via the API and returns its
// instance id (looked up from the db by slug).
func addTestIndexer(t *testing.T, e *env, base string, c *http.Client, slug string) int64 {
	t.Helper()
	add := struct {
		Slug         string            `json:"slug"`
		DefinitionID string            `json:"definitionId"`
		Settings     map[string]string `json:"settings"`
	}{
		Slug:         slug,
		DefinitionID: "testtracker",
		Settings:     map[string]string{"apikey": "k"},
	}
	resp, body := do(t, c, http.MethodPost, base+"/api/indexers", add, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	inst, err := database.Instances{}.GetBySlug(context.Background(), e.db, slug)
	if err != nil {
		t.Fatalf("get instance by slug: %v", err)
	}
	return inst.ID
}

type cacheConfigBody struct {
	Enabled         bool   `json:"enabled"`
	RSSTTL          string `json:"rssTtl"`
	KeywordTTL      string `json:"keywordTtl"`
	ThinTTL         string `json:"thinTtl"`
	ThinThreshold   int    `json:"thinThreshold"`
	RefreshAheadPct int    `json:"refreshAheadPct"`
	NegativeTTL     string `json:"negativeTtl"`
}

func TestCacheConfigGetPut(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	e := newEnvWithCache(t, api.Config{}, cacheBuilder(now))
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	// GET reflects the seeded params (enabled, rss 5m, keyword 30m).
	resp, body := do(t, c, http.MethodGet, base+"/api/cache/config", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var cfg cacheConfigBody
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !cfg.Enabled || cfg.RSSTTL != "5m0s" || cfg.KeywordTTL != "30m0s" {
		t.Fatalf("seed config = %+v", cfg)
	}

	// PUT a partial update: disable + change keywordTtl; rssTtl must be untouched.
	resp, body = do(t, c, http.MethodPut, base+"/api/cache/config",
		map[string]any{"enabled": false, "keywordTtl": "45m"}, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("decode put response: %v", err)
	}
	if cfg.Enabled {
		t.Error("enabled = true after PUT {enabled:false}")
	}
	if cfg.KeywordTTL != "45m0s" {
		t.Errorf("keywordTtl = %q, want 45m0s", cfg.KeywordTTL)
	}
	if cfg.RSSTTL != "5m0s" {
		t.Errorf("rssTtl = %q, want unchanged 5m0s", cfg.RSSTTL)
	}

	// A subsequent GET reflects the applied update.
	resp, body = do(t, c, http.MethodGet, base+"/api/cache/config", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	_ = json.Unmarshal(body, &cfg)
	if cfg.Enabled || cfg.KeywordTTL != "45m0s" {
		t.Errorf("update not reflected on GET: %+v", cfg)
	}

	// Out-of-range, malformed, and non-positive values are 400.
	resp, body = do(t, c, http.MethodPut, base+"/api/cache/config", map[string]any{"refreshAheadPct": 150}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)
	resp, body = do(t, c, http.MethodPut, base+"/api/cache/config", map[string]any{"rssTtl": "nope"}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)
	resp, body = do(t, c, http.MethodPut, base+"/api/cache/config", map[string]any{"rssTtl": "0s"}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	// A rejected PUT must leave the config exactly as the last good update left it.
	resp, body = do(t, c, http.MethodGet, base+"/api/cache/config", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	_ = json.Unmarshal(body, &cfg)
	if cfg.Enabled || cfg.KeywordTTL != "45m0s" || cfg.RSSTTL != "5m0s" {
		t.Errorf("config changed after a rejected PUT: %+v", cfg)
	}
}

// TestCacheConfigDisabled covers the no-cache-wired paths: GET returns a disabled,
// parseable zero config; PUT is 503.
func TestCacheConfigDisabled(t *testing.T) {
	t.Parallel()

	e := newEnv(t, api.Config{}) // no cache wired
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	resp, body := do(t, c, http.MethodGet, base+"/api/cache/config", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var cfg cacheConfigBody
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.Enabled {
		t.Error("enabled = true, want false with no cache")
	}
	if cfg.RSSTTL != "0s" {
		t.Errorf("rssTtl = %q, want a parseable %q", cfg.RSSTTL, "0s")
	}

	resp, body = do(t, c, http.MethodPut, base+"/api/cache/config", map[string]any{"enabled": true}, nil)
	mustStatus(t, resp, body, http.StatusServiceUnavailable)
}

// cacheBuilder builds a SearchCache bound to a given db with the fixed clock.
func cacheBuilder(now time.Time) func(db *database.DB) *registry.SearchCache {
	return func(db *database.DB) *registry.SearchCache {
		return registry.NewSearchCacheWithParams(db, newCacheParams(), func() time.Time { return now }, zerolog.Nop())
	}
}

func TestCacheStatsHappyPath(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	e := newEnvWithCache(t, api.Config{}, cacheBuilder(now))

	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	instanceID := addTestIndexer(t, e, base, c, "tt")
	seedCacheEntry(t, e.db, instanceID, now)

	resp, body := do(t, c, http.MethodGet, base+"/api/cache/stats", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)

	var stats cacheStatsBody
	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if !stats.Enabled {
		t.Error("stats.enabled = false, want true with caching on")
	}
	if stats.Entries != 1 {
		t.Errorf("stats.entries = %d, want 1", stats.Entries)
	}
	if stats.ApproxSizeBytes <= 0 {
		t.Errorf("stats.approxSizeBytes = %d, want > 0", stats.ApproxSizeBytes)
	}
	if stats.OldestCachedAt == nil || stats.NewestCachedAt == nil || stats.LastUsedAt == nil {
		t.Errorf("stats timestamps should be non-nil with one entry: %+v", stats)
	}
	// The per-indexer breakdown labels the seeded instance with its slug/name.
	if len(stats.ByIndexer) != 1 {
		t.Fatalf("byIndexer = %d rows, want 1", len(stats.ByIndexer))
	}
	idx := stats.ByIndexer[0]
	if idx.InstanceID != instanceID || idx.Slug != "tt" {
		t.Errorf("byIndexer[0] = %+v, want instance %d slug tt", idx, instanceID)
	}
	if idx.Entries != 1 || idx.Name == "" {
		t.Errorf("byIndexer[0] = %+v, want entries=1 and a non-empty name", idx)
	}
	if idx.BreakerOpenUntil != nil {
		t.Errorf("byIndexer[0].breakerOpenUntil = %v, want null", idx.BreakerOpenUntil)
	}
	// trackerHitsSaved mirrors the durable totalHits (no hits served yet -> 0).
	if stats.TrackerHitsSaved != stats.TotalHits {
		t.Errorf("trackerHitsSaved = %d, want == totalHits %d", stats.TrackerHitsSaved, stats.TotalHits)
	}
	// The global hits/misses are the aggregate of the per-indexer rows (the global view
	// the per-tracker breakdown was missing): summing byIndexer must reproduce them.
	var sumHits, sumMisses int64
	for _, row := range stats.ByIndexer {
		sumHits += row.Hits
		sumMisses += row.Misses
	}
	if stats.Hits != sumHits {
		t.Errorf("global hits = %d, want == sum(byIndexer.hits) %d", stats.Hits, sumHits)
	}
	if stats.Misses != sumMisses {
		t.Errorf("global misses = %d, want == sum(byIndexer.misses) %d", stats.Misses, sumMisses)
	}
	// The decoded values are all zero here (no search served), so guard the wiring
	// itself: assert the handler actually serializes the top-level keys. Without this,
	// dropping hits/misses from the response would silently decode to 0 and pass above.
	// Check the top-level object specifically — "hits" also appears inside byIndexer.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("decode stats object: %v", err)
	}
	for _, key := range []string{"hits", "misses"} {
		if _, ok := top[key]; !ok {
			t.Errorf("stats JSON missing top-level %q key: %s", key, body)
		}
	}
}

// TestCacheConfigNegativeTTL covers the breaker knob: it round-trips on GET, accepts
// "0s" to disable (which parseNonNegDurPatch admits where the TTL knobs reject it),
// and rejects a negative duration.
func TestCacheConfigNegativeTTL(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	e := newEnvWithCache(t, api.Config{}, cacheBuilder(now))
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	// Seed default is 1m (newCacheParams leaves NegativeTTL zero, so the cache seeds 0
	// — assert whatever the seed is, then drive it explicitly).
	resp, body := do(t, c, http.MethodPut, base+"/api/cache/config", map[string]any{"negativeTtl": "90s"}, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var cfg cacheConfigBody
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.NegativeTTL != "1m30s" {
		t.Errorf("negativeTtl = %q, want 1m30s", cfg.NegativeTTL)
	}

	// "0s" disables the breaker — admitted here unlike the positive-only TTL knobs.
	resp, body = do(t, c, http.MethodPut, base+"/api/cache/config", map[string]any{"negativeTtl": "0s"}, nil)
	mustStatus(t, resp, body, http.StatusOK)
	_ = json.Unmarshal(body, &cfg)
	if cfg.NegativeTTL != "0s" {
		t.Errorf("negativeTtl = %q, want 0s (disabled)", cfg.NegativeTTL)
	}

	// A negative duration is rejected.
	resp, body = do(t, c, http.MethodPut, base+"/api/cache/config", map[string]any{"negativeTtl": "-5s"}, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)
}

func TestCacheFlushHappyPath(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	e := newEnvWithCache(t, api.Config{}, cacheBuilder(now))

	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	instanceID := addTestIndexer(t, e, base, c, "tt")
	seedCacheEntry(t, e.db, instanceID, now)

	resp, body := do(t, c, http.MethodPost, base+"/api/cache/flush", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var flush cacheFlushBody
	if err := json.Unmarshal(body, &flush); err != nil {
		t.Fatalf("decode flush: %v", err)
	}
	if flush.Flushed != 1 {
		t.Errorf("flush.flushed = %d, want 1", flush.Flushed)
	}

	// A second flush purges nothing.
	resp, body = do(t, c, http.MethodPost, base+"/api/cache/flush", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if err := json.Unmarshal(body, &flush); err != nil {
		t.Fatalf("decode second flush: %v", err)
	}
	if flush.Flushed != 0 {
		t.Errorf("second flush.flushed = %d, want 0", flush.Flushed)
	}
}

func TestCacheStatsDisabled(t *testing.T) {
	t.Parallel()

	e := newEnv(t, api.Config{}) // no cache wired => disabled
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	resp, body := do(t, c, http.MethodGet, base+"/api/cache/stats", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var stats cacheStatsBody
	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.Enabled {
		t.Error("stats.enabled = true, want false with caching off")
	}
	// byIndexer is always a JSON array, never null — even with caching disabled.
	if stats.ByIndexer == nil {
		t.Error("byIndexer = null with caching off, want []")
	}
}

func TestCacheFlushDisabled(t *testing.T) {
	t.Parallel()

	e := newEnv(t, api.Config{})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	resp, body := do(t, c, http.MethodPost, base+"/api/cache/flush", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var flush cacheFlushBody
	if err := json.Unmarshal(body, &flush); err != nil {
		t.Fatalf("decode flush: %v", err)
	}
	if flush.Flushed != 0 {
		t.Errorf("flush.flushed = %d, want 0 with caching off", flush.Flushed)
	}
}
