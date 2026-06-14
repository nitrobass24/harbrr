// Package api is harbrr's management HTTP API (the OpenAPI surface, distinct from
// the *arr-facing Torznab contract — architecture invariant #3). It exposes
// first-run setup, session + API-key auth, API-key management, and indexer CRUD
// over a chi router; the embedded spec is served by the server.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// errorResponse is the JSON error envelope.
type errorResponse struct {
	Error string `json:"error"`
}

// writeJSON writes v as JSON with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeError writes a JSON error envelope.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// decodeJSON decodes a single JSON object from the request body into dst,
// rejecting unknown fields and any trailing data after the first object. On
// failure it writes a 400 and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	// Exactly one JSON value is allowed; a second Decode must hit EOF.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "unexpected trailing data after JSON body")
		return false
	}
	return true
}

// writeServiceError maps a service-layer error to an HTTP status. Known sentinels
// map to 4xx with a safe message; anything else is logged (redacted) and returned
// as a generic 500 so internal detail never reaches the client.
func (rt *router) writeServiceError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, database.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, registry.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, registry.ErrInvalid), errors.Is(err, auth.ErrWeakPassword), errors.Is(err, auth.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, auth.ErrAlreadySetup):
		writeError(w, http.StatusConflict, "setup already complete")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid credentials")
	case errors.Is(err, auth.ErrInvalidAPIKey):
		writeError(w, http.StatusUnauthorized, "invalid api key")
	default:
		rt.log.Error().Str("op", op).Str("error", apphttp.RedactURL(err.Error())).Msg("api: request failed")
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
