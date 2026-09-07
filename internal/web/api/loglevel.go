package api

import (
	"net/http"

	"github.com/autobrr/harbrr/internal/config"
	"github.com/autobrr/harbrr/internal/logger"
)

// logLevelBody is the shared request/response shape for the log-level endpoints.
type logLevelBody struct {
	Level string `json:"level"`
}

// getLogLevel returns the effective runtime log level — the process-global zerolog
// threshold every subsystem's logger consults.
func (rt *router) getLogLevel(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, logLevelBody{Level: logger.Level()})
}

// putLogLevel changes the runtime log level and persists it. The change takes effect
// immediately across every subsystem and survives a restart. Persistence and the
// process-wide apply belong to the composition root (internal/app); this handler owns
// only the HTTP contract, including rejecting a level outside the enum with a 400.
func (rt *router) putLogLevel(w http.ResponseWriter, r *http.Request) {
	if rt.setLogLevel == nil {
		writeError(w, http.StatusServiceUnavailable, "log level control is unavailable")
		return
	}
	var req logLevelBody
	if !decodeJSON(w, r, &req) {
		return
	}
	if !config.ValidLogLevel(req.Level) {
		writeError(w, http.StatusBadRequest, "log level must be one of: trace, debug, info, warn, error")
		return
	}
	if err := rt.setLogLevel(r.Context(), req.Level); err != nil {
		rt.writeServiceError(w, "set log level", err)
		return
	}
	rt.log.Info().Str("level", req.Level).Msg("api: log level changed")
	writeJSON(w, http.StatusOK, logLevelBody{Level: logger.Level()})
}
