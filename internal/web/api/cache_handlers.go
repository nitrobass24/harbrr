package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// cacheStatsResponse is the management view of the search-results cache. hitRatio
// (and its underlying hits/misses/trackerHitsSaved) is a cumulative counter
// persisted across restarts (see registry/searchcache_counters.go); totalHits is
// the durable row-derived figure and — unlike the cumulative counters — SHRINKS
// whenever its backing rows are reaped (cleanup, flush, or an instance
// invalidation), since it is a live SUM over rows currently in the store.
type cacheStatsResponse struct {
	Enabled bool  `json:"enabled"`
	Entries int64 `json:"entries"`
	// TotalHits is the durable SUM of per-entry hit counts over rows CURRENTLY in the
	// cache — it falls (including to 0) whenever those rows are reaped. It is not the
	// headline metric; see trackerHitsSaved below.
	TotalHits int64 `json:"totalHits"`
	// Hits/Misses are the global counters (the sum across all indexers, the aggregate
	// of the per-indexer byIndexer rows). hitRatio is hits / (hits + misses) over the
	// same window. All three are cumulative and survive a restart.
	Hits            int64   `json:"hits"`
	Misses          int64   `json:"misses"`
	HitRatio        float64 `json:"hitRatio"`
	ApproxSizeBytes int64   `json:"approxSizeBytes"`
	OldestCachedAt  *int64  `json:"oldestCachedAt"`
	NewestCachedAt  *int64  `json:"newestCachedAt"`
	LastUsedAt      *int64  `json:"lastUsedAt"`
	// TrackerHitsSaved is the cumulative count of tracker requests served from cache —
	// the headline kind-to-trackers metric. It mirrors Hits (same cumulative,
	// restart-persisted counter) and, unlike totalHits, never drops when cached
	// entries are reaped.
	TrackerHitsSaved int64 `json:"trackerHitsSaved"`
	// BreakerSuppressed is the cumulative count of misses short-circuited by the
	// negative-result breaker (extra tracker requests spared a failing tracker).
	BreakerSuppressed int64 `json:"breakerSuppressed"`
	// ByIndexer is the per-indexer breakdown (ordered by instance id).
	ByIndexer []cacheIndexerStats `json:"byIndexer"`
}

// cacheIndexerStats is one indexer's cache observability row in the stats response.
type cacheIndexerStats struct {
	InstanceID int64  `json:"instanceId"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	Entries    int64  `json:"entries"`
	// HitsSaved is this indexer's cumulative tracker requests served from cache (mirrors
	// Hits below) — never drops when this indexer's cached entries are reaped.
	HitsSaved         int64   `json:"hitsSaved"`
	Hits              int64   `json:"hits"`
	Misses            int64   `json:"misses"`
	HitRatio          float64 `json:"hitRatio"`
	ApproxSizeBytes   int64   `json:"approxSizeBytes"`
	BreakerSuppressed int64   `json:"breakerSuppressed"`
	// BreakerOpenUntil is the Unix-seconds instant the breaker reopens this indexer to
	// live traffic, or null when the breaker is currently closed for it.
	BreakerOpenUntil *int64 `json:"breakerOpenUntil"`
}

// cacheFlushResponse reports how many entries a flush purged.
type cacheFlushResponse struct {
	Flushed int64 `json:"flushed"`
}

// cacheStats returns the search-results cache statistics. With caching disabled
// (no cache wired) it answers 200 with {"enabled":false} rather than 404.
func (rt *router) cacheStats(w http.ResponseWriter, r *http.Request) {
	if rt.cache == nil {
		// Keep byIndexer a JSON array (never null) so the response always matches the
		// CacheStats schema, even with caching off.
		writeJSON(w, http.StatusOK, cacheStatsResponse{Enabled: false, ByIndexer: []cacheIndexerStats{}})
		return
	}
	stats, err := rt.cache.Stats(r.Context())
	if err != nil {
		rt.writeServiceError(w, "cache.stats", err)
		return
	}
	byIndexer, err := rt.cacheStatsByIndexer(r.Context())
	if err != nil {
		rt.writeServiceError(w, "cache.stats", err)
		return
	}
	writeJSON(w, http.StatusOK, cacheStatsResponse{
		Enabled:           rt.cache.Enabled(),
		Entries:           stats.Entries,
		TotalHits:         stats.TotalHits,
		Hits:              stats.Hits,
		Misses:            stats.Misses,
		HitRatio:          stats.HitRatio,
		ApproxSizeBytes:   stats.ApproxSizeBytes,
		OldestCachedAt:    stats.OldestUnixSec,
		NewestCachedAt:    stats.NewestUnixSec,
		LastUsedAt:        stats.LastUsedUnixSec,
		TrackerHitsSaved:  stats.Hits,
		BreakerSuppressed: stats.BreakerSuppressed,
		ByIndexer:         byIndexer,
	})
}

// cacheStatsByIndexer builds the per-indexer stats rows, labeling each with its
// configured slug/name. A name-lookup failure is non-fatal (names are cosmetic): the
// rows are still returned, just unlabeled.
func (rt *router) cacheStatsByIndexer(ctx context.Context) ([]cacheIndexerStats, error) {
	rows, err := rt.cache.StatsByInstance(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // surfaced to writeServiceError (the redaction sink); nothing secret to add.
	}
	names, slugs := rt.instanceLabels(ctx)
	out := make([]cacheIndexerStats, 0, len(rows))
	for _, s := range rows {
		out = append(out, cacheIndexerStats{
			InstanceID:        s.InstanceID,
			Slug:              slugs[s.InstanceID],
			Name:              names[s.InstanceID],
			Entries:           s.Entries,
			HitsSaved:         s.Hits,
			Hits:              s.Hits,
			Misses:            s.Misses,
			HitRatio:          s.HitRatio,
			ApproxSizeBytes:   s.ApproxSizeBytes,
			BreakerSuppressed: s.BreakerSuppressed,
			BreakerOpenUntil:  s.BreakerOpenUntil,
		})
	}
	return out, nil
}

// instanceLabels maps instance id -> name and id -> slug from the registry. A list
// failure leaves both maps empty (labels are cosmetic and must not fail stats).
func (rt *router) instanceLabels(ctx context.Context) (names, slugs map[int64]string) {
	names, slugs = map[int64]string{}, map[int64]string{}
	list, err := rt.registry.List(ctx)
	if err != nil {
		rt.log.Warn().Str("error", apphttp.RedactError(err)).Msg("cache stats: indexer label lookup failed")
		return names, slugs
	}
	for _, inst := range list {
		names[inst.ID] = inst.Name
		slugs[inst.ID] = inst.Slug
	}
	return names, slugs
}

// cacheFlush purges every cache entry and reports the count. With caching
// disabled it answers 200 with {"flushed":0} rather than 404.
func (rt *router) cacheFlush(w http.ResponseWriter, r *http.Request) {
	if rt.cache == nil {
		writeJSON(w, http.StatusOK, cacheFlushResponse{Flushed: 0})
		return
	}
	n, err := rt.cache.Flush(r.Context())
	if err != nil {
		rt.writeServiceError(w, "cache.flush", err)
		return
	}
	writeJSON(w, http.StatusOK, cacheFlushResponse{Flushed: n})
}

// cacheConfigResponse is the management view of the runtime-tunable cache config.
// Durations are Go duration strings (e.g. "5m0s"), parseable on the way back in.
type cacheConfigResponse struct {
	Enabled         bool   `json:"enabled"`
	RSSTTL          string `json:"rssTtl"`
	KeywordTTL      string `json:"keywordTtl"`
	ThinTTL         string `json:"thinTtl"`
	ThinThreshold   int    `json:"thinThreshold"`
	RefreshAheadPct int    `json:"refreshAheadPct"`
	// NegativeTTL is the negative-result circuit-breaker window ("0s" disables it).
	NegativeTTL string `json:"negativeTtl"`
	// CleanupInterval is how often expired entries are reaped (Go duration string).
	CleanupInterval string `json:"cleanupInterval"`
}

// cacheConfigUpdate is the PUT body. Every field is optional (a nil field leaves
// that knob unchanged), so a client can flip one setting without resending the rest.
type cacheConfigUpdate struct {
	Enabled         *bool   `json:"enabled"`
	RSSTTL          *string `json:"rssTtl"`
	KeywordTTL      *string `json:"keywordTtl"`
	ThinTTL         *string `json:"thinTtl"`
	ThinThreshold   *int    `json:"thinThreshold"`
	RefreshAheadPct *int    `json:"refreshAheadPct"`
	NegativeTTL     *string `json:"negativeTtl"`
	CleanupInterval *string `json:"cleanupInterval"`
}

func toCacheConfigResponse(v registry.CacheConfigView) cacheConfigResponse {
	return cacheConfigResponse{
		Enabled:         v.Enabled,
		RSSTTL:          v.RSSTTL.String(),
		KeywordTTL:      v.KeywordTTL.String(),
		ThinTTL:         v.ThinTTL.String(),
		ThinThreshold:   v.ThinThreshold,
		RefreshAheadPct: v.RefreshAheadPct,
		NegativeTTL:     v.NegativeTTL.String(),
		CleanupInterval: v.CleanupInterval.String(),
	}
}

// cacheConfigGet returns the live cache configuration. With no cache wired it
// answers 200 with a disabled, zero-valued config rather than 404.
func (rt *router) cacheConfigGet(w http.ResponseWriter, _ *http.Request) {
	if rt.cache == nil {
		writeJSON(w, http.StatusOK, toCacheConfigResponse(registry.CacheConfigView{}))
		return
	}
	writeJSON(w, http.StatusOK, toCacheConfigResponse(rt.cache.Config()))
}

// cacheConfigPut applies a partial update to the cache configuration. Only the
// supplied fields are persisted (omitted knobs keep their config-file/default value),
// and the merge+validate+persist+swap happens atomically inside the cache. A bad
// duration or out-of-range value answers 400; the config is left unchanged.
func (rt *router) cacheConfigPut(w http.ResponseWriter, r *http.Request) {
	if rt.cache == nil {
		writeError(w, http.StatusServiceUnavailable, "search cache is not available")
		return
	}
	var req cacheConfigUpdate
	if !decodeJSON(w, r, &req) {
		return
	}
	patch := registry.CacheConfigPatch{
		Enabled:         req.Enabled,
		ThinThreshold:   req.ThinThreshold,
		RefreshAheadPct: req.RefreshAheadPct,
	}
	if !parseDurPatch(w, req.RSSTTL, &patch.RSSTTL, "rssTtl") ||
		!parseDurPatch(w, req.KeywordTTL, &patch.KeywordTTL, "keywordTtl") ||
		!parseDurPatch(w, req.ThinTTL, &patch.ThinTTL, "thinTtl") ||
		!parseDurPatch(w, req.CleanupInterval, &patch.CleanupInterval, "cleanupInterval") ||
		!parseNonNegDurPatch(w, req.NegativeTTL, &patch.NegativeTTL, "negativeTtl") {
		return
	}
	v, err := rt.cache.UpdateConfig(r.Context(), patch)
	if err != nil {
		if errors.Is(err, registry.ErrInvalidCacheConfig) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		rt.writeServiceError(w, "cache.config", err)
		return
	}
	writeJSON(w, http.StatusOK, toCacheConfigResponse(v))
}

// parseDurPatch parses an optional positive duration string into a *time.Duration
// patch field, writing a 400 and returning false on a malformed/non-positive value.
// A nil input leaves the patch field nil (that knob is left unchanged).
func parseDurPatch(w http.ResponseWriter, in *string, dst **time.Duration, name string) bool {
	if in == nil {
		return true
	}
	d, err := time.ParseDuration(*in)
	if err != nil || d <= 0 {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid duration for %s: %q (want a positive duration like \"10m\")", name, *in))
		return false
	}
	*dst = &d
	return true
}

// parseNonNegDurPatch parses an optional NON-negative duration (unlike parseDurPatch,
// it admits "0s") into a *time.Duration patch field, writing a 400 and returning false
// on a malformed or negative value. The negative-result breaker uses it so "0s" can
// disable the breaker at runtime. A nil input leaves the patch field nil (unchanged).
func parseNonNegDurPatch(w http.ResponseWriter, in *string, dst **time.Duration, name string) bool {
	if in == nil {
		return true
	}
	d, err := time.ParseDuration(*in)
	if err != nil || d < 0 {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid duration for %s: %q (want a non-negative duration like \"1m\", or \"0s\" to disable)", name, *in))
		return false
	}
	*dst = &d
	return true
}
