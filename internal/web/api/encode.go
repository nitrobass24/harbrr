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
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
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
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusNotImplemented:
		return "not_implemented"
	default:
		return "internal"
	}
}

// maxRequestBodyBytes caps how much of a JSON request body decodeJSON will read.
// Every management-API body is small (login credentials, indexer/proxy/solver
// settings, sync profiles — KB-scale at most), so 1 MiB is a generous ceiling that
// never rejects a legitimate request while bounding an unauthenticated memory-
// exhaustion attack: without a cap json.Decoder buffers the whole body, so a
// multi-GB POST to a public route (/api/auth/login, /api/auth/setup) is fully read
// into memory before it is even parsed. MaxBytesReader tracks total bytes read
// across both Decode calls below, so trailing data past the first object is capped
// too.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// decodeJSON decodes a single JSON object from the request body into dst,
// rejecting unknown fields and any trailing data after the first object. The body
// is size-capped (maxRequestBodyBytes); an oversize body writes a 413, any other
// decode failure a 400. Returns false when it has written an error.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSONLimit(w, r, dst, maxRequestBodyBytes)
}

// decodeJSONLimit is decodeJSON with a caller-chosen body cap, for the rare endpoint
// (a config-restore bundle) whose legitimate body can exceed the default 1 MiB.
func decodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return failDecode(w, err, "invalid JSON body")
	}
	// Exactly one JSON value is allowed; a second Decode must hit EOF.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return failDecode(w, err, "unexpected trailing data after JSON body")
	}
	return true
}

// failDecode writes the error response for a failed Decode and returns false. An
// oversize body (MaxBytesReader tripped) is a 413; everything else uses badMsg with
// a 400.
func failDecode(w http.ResponseWriter, err error, badMsg string) bool {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return false
	}
	writeError(w, http.StatusBadRequest, badMsg)
	return false
}

// writeServiceError maps a service-layer error to an HTTP status. Known sentinels
// map to 4xx with a safe message; anything else is logged (redacted) and returned
// as a generic 500 so internal detail never reaches the client.
func (rt *router) writeServiceError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, database.ErrNotFound):
		writeErrorCode(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, domain.ErrConflict):
		writeErrorCode(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, domain.ErrInvalid), errors.Is(err, auth.ErrWeakPassword), errors.Is(err, auth.ErrInvalidInput):
		writeErrorCode(w, http.StatusBadRequest, "invalid", err.Error())
	case errors.Is(err, auth.ErrAlreadySetup):
		writeErrorCode(w, http.StatusConflict, "already_setup", "setup already complete")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeErrorCode(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
	case errors.Is(err, auth.ErrInvalidAPIKey):
		writeErrorCode(w, http.StatusUnauthorized, "invalid_api_key", "invalid api key")
	case errors.Is(err, errOIDCTokenInvalid):
		rt.log.Warn().Str("op", op).Str("error", apphttp.RedactError(err)).Msg("api: oidc id token verification failed")
		writeErrorCode(w, http.StatusUnauthorized, "invalid_credentials", "oidc: id token verification failed")
	case errors.Is(err, search.ErrGatewayStatus):
		writeErrorCode(w, http.StatusBadGateway, "upstream_unreachable", "indexer origin unreachable")
	default:
		rt.log.Error().Str("op", op).Str("error", apphttp.RedactError(err)).Msg("api: request failed")
		writeErrorCode(w, http.StatusInternalServerError, "internal", "internal error")
	}
}
