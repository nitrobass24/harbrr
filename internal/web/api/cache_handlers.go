package api

import "net/http"

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
		Enabled:         true,
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
