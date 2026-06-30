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

	"github.com/autobrr/harbrr/internal/announce"
	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// errorResponse is the JSON error envelope: a human-readable message plus a
// machine-readable code clients can branch on.
type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// writeJSON writes v as JSON with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeError writes a JSON error envelope, deriving the machine-readable code from
// the status. Use writeErrorCode for a more specific code.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeErrorCode(w, status, codeForStatus(status), msg)
}

// writeErrorCode writes a JSON error envelope with an explicit machine-readable code.
func writeErrorCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorResponse{Error: msg, Code: code})
}

// codeForStatus is the default machine-readable code for an HTTP status, used where
// no more specific sentinel applies.
func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusNotImplemented:
		return "not_implemented"
	default:
		return "internal"
	}
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
		writeErrorCode(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, registry.ErrConflict), errors.Is(err, appsync.ErrConflict), errors.Is(err, announce.ErrConflict):
		writeErrorCode(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, registry.ErrInvalid), errors.Is(err, appsync.ErrInvalid), errors.Is(err, announce.ErrInvalid), errors.Is(err, auth.ErrWeakPassword), errors.Is(err, auth.ErrInvalidInput):
		writeErrorCode(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, auth.ErrAlreadySetup):
		writeErrorCode(w, http.StatusConflict, "already_setup", "setup already complete")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeErrorCode(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
	case errors.Is(err, auth.ErrInvalidAPIKey):
		writeErrorCode(w, http.StatusUnauthorized, "invalid_api_key", "invalid api key")
	default:
		rt.log.Error().Str("op", op).Str("error", apphttp.RedactError(err)).Msg("api: request failed")
		writeErrorCode(w, http.StatusInternalServerError, "internal", "internal error")
	}
}
