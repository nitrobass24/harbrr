package api

import "net/http"

// rateLimitBody is the shared request/response shape for the rate-limit-default
// endpoints (autobrr/harbrr#104).
type rateLimitBody struct {
	DefaultInterval string `json:"defaultInterval"`
}

// rateLimitGet returns the live global rate-limit default (a Go duration string).
func (rt *router) rateLimitGet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, rateLimitBody{DefaultInterval: rt.registry.RateDefault().String()})
}

// rateLimitPut sets the global rate-limit default: persists it and applies it to
// every indexer's paced client without a restart (registry.Resolver.SetRateDefault).
// An invalid duration answers 400 and changes nothing.
func (rt *router) rateLimitPut(w http.ResponseWriter, r *http.Request) {
	var req rateLimitBody
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.registry.SetRateDefault(r.Context(), req.DefaultInterval); err != nil {
		rt.writeServiceError(w, "rate-limit.config", err)
		return
	}
	writeJSON(w, http.StatusOK, rateLimitBody{DefaultInterval: rt.registry.RateDefault().String()})
}
