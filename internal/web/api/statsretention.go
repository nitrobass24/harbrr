package api

import "net/http"

// statsRetentionBody is the shared request/response shape for the per-category stats
// retention window, in whole months.
type statsRetentionBody struct {
	Months int `json:"months"`
}

// getStatsRetention returns the operator's retention window for the per-category
// indexer tallies (the default when never set).
func (rt *router) getStatsRetention(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, statsRetentionBody{Months: rt.registry.CategoryStatsRetention(r.Context())})
}

// putStatsRetention sets the retention window. It takes effect on the next daily reap;
// shortening it drops the now-expired month buckets then, not immediately.
func (rt *router) putStatsRetention(w http.ResponseWriter, r *http.Request) {
	var req statsRetentionBody
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.registry.SetCategoryStatsRetention(r.Context(), req.Months); err != nil {
		rt.writeServiceError(w, "stats retention", err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}
