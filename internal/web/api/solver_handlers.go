package api

import (
	"net/http"
	"time"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/solver"
)

// solverResponse is the API view of an anti-bot-solver resource. The endpoint URL
// is never echoed — it reads back as the <redacted> sentinel.
type solverResponse struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	URL        string    `json:"url"`
	MaxTimeout int       `json:"maxTimeout"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// listSolvers returns all solvers (URLs redacted).
func (rt *router) listSolvers(w http.ResponseWriter, r *http.Request) {
	list, err := rt.solver.List(r.Context())
	if err != nil {
		rt.writeServiceError(w, "list solvers", err)
		return
	}
	out := make([]solverResponse, 0, len(list))
	for _, s := range list {
		out = append(out, toSolverResponse(s))
	}
	writeJSON(w, http.StatusOK, out)
}

// createSolver adds a solver with its endpoint URL encrypted.
func (rt *router) createSolver(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		Type       string `json:"type"`
		URL        string `json:"url"`
		MaxTimeout int    `json:"maxTimeout"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s, err := rt.solver.Create(r.Context(), solver.CreateParams{
		Name: req.Name, Type: req.Type, URL: req.URL, MaxTimeout: req.MaxTimeout,
	})
	if err != nil {
		rt.writeServiceError(w, "create solver", err)
		return
	}
	writeJSON(w, http.StatusCreated, toSolverResponse(s))
}

// getSolver returns one solver (URL redacted).
func (rt *router) getSolver(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "solver")
	if !ok {
		return
	}
	s, err := rt.solver.Get(r.Context(), id)
	if err != nil {
		rt.writeServiceError(w, "get solver", err)
		return
	}
	writeJSON(w, http.StatusOK, toSolverResponse(s))
}

// updateSolver patches a solver (a new url rotates the endpoint).
func (rt *router) updateSolver(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "solver")
	if !ok {
		return
	}
	var req struct {
		Name       *string `json:"name"`
		Type       *string `json:"type"`
		URL        *string `json:"url"`
		MaxTimeout *int    `json:"maxTimeout"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.solver.Update(r.Context(), id, solver.UpdateParams{
		Name: req.Name, Type: req.Type, URL: req.URL, MaxTimeout: req.MaxTimeout,
	}); err != nil {
		rt.writeServiceError(w, "update solver", err)
		return
	}
	// A cached engine bakes in the resolved solver config, so evict them.
	rt.registry.InvalidateAll()
	w.WriteHeader(http.StatusNoContent)
}

// deleteSolver removes a solver (referencing indexers fall back to no solver).
func (rt *router) deleteSolver(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "solver")
	if !ok {
		return
	}
	if err := rt.solver.Delete(r.Context(), id); err != nil {
		rt.writeServiceError(w, "delete solver", err)
		return
	}
	// The FK nulled solver_id, but cached engines still use the deleted solver
	// until evicted.
	rt.registry.InvalidateAll()
	w.WriteHeader(http.StatusNoContent)
}

// toSolverResponse maps a solver to its API view, redacting the endpoint URL.
func toSolverResponse(s domain.Solver) solverResponse {
	return solverResponse{
		ID: s.ID, Name: s.Name, Type: s.Type, URL: secrets.Redacted, MaxTimeout: s.MaxTimeout,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}
