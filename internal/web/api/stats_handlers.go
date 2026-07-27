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
	Transport   int64 `json:"transport"`
}

// indexerCategoryStat is one parent category's tally in the stats response: how many
// results the indexer returned in that family and how many were grabbed, summed over
// the retained months. id 0 ("Uncategorized") holds releases with no mappable standard
// category.
type indexerCategoryStat struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Results int64  `json:"results"`
	Grabs   int64  `json:"grabs"`
}

// indexerStatsResponse is the JSON body of the per-indexer stats endpoints: the durable
// query/grab/latency counters plus the failure aggregation and the per-category
// breakdown. queries counts searches that actually reached the tracker (a cache hit
// bypasses the instrumented path), so avgResponseMs reflects real upstream latency;
// grabAttempts counts grabs that reached it, so grabSuccessRate (0..1, absent until
// something has been attempted) is the "grabbed 200, 40 failed" number.
// lastQueryAt/lastFailureAt are nil when never observed (a never-queried indexer emits
// null rather than the zero time).
type indexerStatsResponse struct {
	Slug            string                `json:"slug"`
	Queries         int64                 `json:"queries"`
	GrabAttempts    int64                 `json:"grabAttempts"`
	Grabs           int64                 `json:"grabs"`
	GrabSuccessRate *float64              `json:"grabSuccessRate,omitempty"`
	AvgResponseMs   int64                 `json:"avgResponseMs"`
	Failures        indexerFailureCounts  `json:"failures"`
	Categories      []indexerCategoryStat `json:"categories"`
	LastQueryAt     *time.Time            `json:"lastQueryAt,omitempty"`
	LastFailureAt   *time.Time            `json:"lastFailureAt,omitempty"`
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
	cats := make([]indexerCategoryStat, 0, len(st.Categories))
	for _, c := range st.Categories {
		cats = append(cats, indexerCategoryStat{ID: c.CategoryID, Name: c.Name, Results: c.Results, Grabs: c.Grabs})
	}
	return indexerStatsResponse{
		Slug:            st.Slug,
		Queries:         st.Queries,
		GrabAttempts:    st.GrabAttempts,
		Grabs:           st.Grabs,
		GrabSuccessRate: st.GrabSuccessRate,
		AvgResponseMs:   st.AvgResponseMs,
		Failures: indexerFailureCounts{
			AuthFailure: st.Failures.AuthFailure,
			RateLimited: st.Failures.RateLimited,
			ParseError:  st.Failures.ParseError,
			AntiBot:     st.Failures.AntiBot,
			Transport:   st.Failures.Transport,
		},
		Categories:    cats,
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
