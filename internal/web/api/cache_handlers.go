package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// cacheStatsResponse is the management view of the search-results cache. The
// durable figures come from the store; hitRatio (and its underlying hits/misses)
// is a process-lifetime, non-persistent counter that resets on restart.
type cacheStatsResponse struct {
	Enabled         bool    `json:"enabled"`
	Entries         int64   `json:"entries"`
	TotalHits       int64   `json:"totalHits"`
	HitRatio        float64 `json:"hitRatio"`
	ApproxSizeBytes int64   `json:"approxSizeBytes"`
	OldestCachedAt  *int64  `json:"oldestCachedAt"`
	NewestCachedAt  *int64  `json:"newestCachedAt"`
	LastUsedAt      *int64  `json:"lastUsedAt"`
}

// cacheFlushResponse reports how many entries a flush purged.
type cacheFlushResponse struct {
	Flushed int64 `json:"flushed"`
}

// cacheStats returns the search-results cache statistics. With caching disabled
// (no cache wired) it answers 200 with {"enabled":false} rather than 404.
func (rt *router) cacheStats(w http.ResponseWriter, r *http.Request) {
	if rt.cache == nil {
		writeJSON(w, http.StatusOK, cacheStatsResponse{Enabled: false})
		return
	}
	stats, err := rt.cache.Stats(r.Context())
	if err != nil {
		rt.writeServiceError(w, "cache.stats", err)
		return
	}
	writeJSON(w, http.StatusOK, cacheStatsResponse{
		Enabled:         rt.cache.Enabled(),
		Entries:         stats.Entries,
		TotalHits:       stats.TotalHits,
		HitRatio:        stats.HitRatio,
		ApproxSizeBytes: stats.ApproxSizeBytes,
		OldestCachedAt:  stats.OldestUnixSec,
		NewestCachedAt:  stats.NewestUnixSec,
		LastUsedAt:      stats.LastUsedUnixSec,
	})
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
}

func toCacheConfigResponse(v registry.CacheConfigView) cacheConfigResponse {
	return cacheConfigResponse{
		Enabled:         v.Enabled,
		RSSTTL:          v.RSSTTL.String(),
		KeywordTTL:      v.KeywordTTL.String(),
		ThinTTL:         v.ThinTTL.String(),
		ThinThreshold:   v.ThinThreshold,
		RefreshAheadPct: v.RefreshAheadPct,
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
		!parseDurPatch(w, req.ThinTTL, &patch.ThinTTL, "thinTtl") {
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
