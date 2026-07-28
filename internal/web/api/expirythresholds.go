package api

import (
	"net/http"
)

// expiryThresholdsBody is the shared request/response shape of the expiry lead-time
// dial (#399): how many days before a tracked VIP/membership expiry harbrr warns.
// Stored in app_settings like the other runtime dials and re-read by the hourly scan,
// so a change applies without a restart. The response is always the canonical list —
// deduped, descending, and including the at-expiry (0) warning, which is appended
// unconditionally and is not the operator's to remove.
type expiryThresholdsBody struct {
	Days []int `json:"days"`
}

// getExpiryThresholds returns the effective lead times, defaults included, so the UI
// never has to duplicate the fallback logic.
func (rt *router) getExpiryThresholds(w http.ResponseWriter, r *http.Request) {
	days, err := rt.notify.ExpiryThresholds(r.Context())
	if err != nil {
		rt.writeServiceError(w, "expiry thresholds", err)
		return
	}
	writeJSON(w, http.StatusOK, expiryThresholdsBody{Days: days})
}

// putExpiryThresholds sets the lead times, echoing back what was actually stored.
func (rt *router) putExpiryThresholds(w http.ResponseWriter, r *http.Request) {
	var req expiryThresholdsBody
	if !decodeJSON(w, r, &req) {
		return
	}
	days, err := rt.notify.SetExpiryThresholds(r.Context(), req.Days)
	if err != nil {
		rt.writeServiceError(w, "expiry thresholds", err)
		return
	}
	writeJSON(w, http.StatusOK, expiryThresholdsBody{Days: days})
}
