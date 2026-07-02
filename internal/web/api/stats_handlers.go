package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// indexerFailureCounts is the failure tally by health kind in the stats response.
type indexerFailureCounts struct {
	AuthFailure int64 `json:"authFailure"`
	RateLimited int64 `json:"rateLimited"`
	ParseError  int64 `json:"parseError"`
	AntiBot     int64 `json:"antiBot"`
}

// indexerStatsResponse is the JSON body of the per-indexer stats endpoints: the durable
// query/grab/latency counters plus the failure aggregation. queries counts searches that
// actually reached the tracker (a cache hit bypasses the instrumented path), so
// avgResponseMs reflects real upstream latency. lastQueryAt/lastFailureAt are nil when
// never observed (a never-queried indexer emits null rather than the zero time).
type indexerStatsResponse struct {
	Slug          string               `json:"slug"`
	Queries       int64                `json:"queries"`
	Grabs         int64                `json:"grabs"`
	AvgResponseMs int64                `json:"avgResponseMs"`
	Failures      indexerFailureCounts `json:"failures"`
	LastQueryAt   *time.Time           `json:"lastQueryAt,omitempty"`
	LastFailureAt *time.Time           `json:"lastFailureAt,omitempty"`
}

// indexerStats returns one configured indexer's Prowlarr-style stats. An unknown slug is
// a 404.
func (rt *router) indexerStats(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	st, err := rt.registry.Stats(r.Context(), slug)
	if err != nil {
		rt.writeServiceError(w, "indexer stats", err)
		return
	}
	writeJSON(w, http.StatusOK, toStatsResponse(st))
}

// allIndexerStats returns per-indexer stats for every configured indexer.
func (rt *router) allIndexerStats(w http.ResponseWriter, r *http.Request) {
	stats, err := rt.registry.AllStats(r.Context())
	if err != nil {
		rt.writeServiceError(w, "all indexer stats", err)
		return
	}
	out := make([]indexerStatsResponse, 0, len(stats))
	for _, st := range stats {
		out = append(out, toStatsResponse(st))
	}
	writeJSON(w, http.StatusOK, out)
}

// toStatsResponse maps the registry's stat to its API view, rendering never-observed
// timestamps as nil (JSON null/absent) rather than the zero time.
func toStatsResponse(st registry.IndexerStat) indexerStatsResponse {
	return indexerStatsResponse{
		Slug:          st.Slug,
		Queries:       st.Queries,
		Grabs:         st.Grabs,
		AvgResponseMs: st.AvgResponseMs,
		Failures: indexerFailureCounts{
			AuthFailure: st.Failures.AuthFailure,
			RateLimited: st.Failures.RateLimited,
			ParseError:  st.Failures.ParseError,
			AntiBot:     st.Failures.AntiBot,
		},
		LastQueryAt:   zeroToNil(st.LastQueryAt),
		LastFailureAt: zeroToNil(st.LastFailureAt),
	}
}

// zeroToNil returns nil for the zero time (never observed) so the JSON field is omitted
// rather than emitting 0001-01-01.
func zeroToNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
