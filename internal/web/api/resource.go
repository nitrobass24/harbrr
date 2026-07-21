package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/database"
	apphttp "github.com/autobrr/harbrr/internal/http"
)

// pathID parses the {id} path param, writing a 400 naming the resource on a
// malformed value.
func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+name+" id")
		return 0, false
	}
	return id, true
}

// testEndpoint runs a connectivity probe and writes the shared test envelope: a pass
// is 200 {"ok":true}; an unknown resource maps through writeServiceError; any other
// failure is 200 {"ok":false,"error":<scrubbed>} so credentials never reach the client.
func (rt *router) testEndpoint(w http.ResponseWriter, r *http.Request, op string, probe func(context.Context) error) {
	switch err := probe(r.Context()); {
	case err == nil:
		writeJSON(w, http.StatusOK, testResult{OK: true})
	case errors.Is(err, database.ErrNotFound):
		rt.writeServiceError(w, op, err)
	default:
		writeJSON(w, http.StatusOK, testResult{OK: false, Error: apphttp.RedactError(err)})
	}
}

// setResourceEnabled parses {id} and flips the resource's enabled flag: 204 on
// success, service errors mapped via writeServiceError.
func (rt *router) setResourceEnabled(w http.ResponseWriter, r *http.Request, name, op string, set func(context.Context, int64, bool) error, enabled bool) {
	id, ok := pathID(w, r, name)
	if !ok {
		return
	}
	if err := set(r.Context(), id, enabled); err != nil {
		rt.writeServiceError(w, op, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
