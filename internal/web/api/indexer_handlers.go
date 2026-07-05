package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// optionalRef is a JSON PATCH field for a nullable id reference. json only calls
// UnmarshalJSON when the key is PRESENT, so an omitted field stays present=false
// (leave the stored value) while an explicit null/number is present=true — the
// tri-state that keeps a partial PATCH from clearing a reference it never mentions.
type optionalRef struct {
	present bool
	value   *int64
}

func (o *optionalRef) UnmarshalJSON(b []byte) error {
	o.present = true
	if string(b) == "null" {
		o.value = nil
		return nil
	}
	if err := json.Unmarshal(b, &o.value); err != nil {
		return fmt.Errorf("api: decode ref: %w", err)
	}
	return nil
}

func (o optionalRef) toRegistry() registry.RefUpdate {
	return registry.RefUpdate{Present: o.present, Value: o.value}
}

// toAppSync maps the same tri-state onto appsync.RefUpdate (the connection PATCH's
// sync-profile reference). appsync redeclares RefUpdate rather than importing registry,
// so the two mappers are parallel by design.
func (o optionalRef) toAppSync() appsync.RefUpdate {
	return appsync.RefUpdate{Present: o.present, Value: o.value}
}

// definitionSummary is the API view of an available definition (for the add form).
type definitionSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	Language    string `json:"language,omitempty"`
}

// listDefinitions returns the available tracker definitions (loaded once, cached).
func (rt *router) listDefinitions(w http.ResponseWriter, _ *http.Request) {
	rt.defsOnce.Do(func() {
		rt.defs, rt.defsErr = loadDefinitionSummaries(rt.loader, rt.registry.NativeDefinitions())
	})
	if rt.defsErr != nil {
		rt.writeServiceError(w, "list definitions", rt.defsErr)
		return
	}
	writeJSON(w, http.StatusOK, rt.defs)
}

// loadDefinitionSummaries summarizes all addable definitions — the vendored
// Cardigann corpus plus the native families (AvistaZ, …) — sorted by id.
func loadDefinitionSummaries(l *loader.Loader, nativeDefs []*loader.Definition) ([]definitionSummary, error) {
	defs, _, err := l.LoadAll()
	if err != nil {
		return nil, fmt.Errorf("api: load definitions: %w", err)
	}
	out := make([]definitionSummary, 0, len(defs)+len(nativeDefs))
	for _, d := range defs {
		out = append(out, summaryOf(d))
	}
	for _, d := range nativeDefs {
		out = append(out, summaryOf(d))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func summaryOf(d *loader.Definition) definitionSummary {
	return definitionSummary{
		ID: d.ID, Name: d.Name, Description: d.Description, Type: d.Type, Language: d.Language,
	}
}

// instanceResponse is the API view of a configured indexer (no secrets). The
// numeric id is the handle the app-sync ledger and the select-indexers call
// (PUT /api/app-connections/{id}/indexers) speak, so clients can map it to a
// slug without a second lookup.
type instanceResponse struct {
	ID           int64     `json:"id"`
	Slug         string    `json:"slug"`
	DefinitionID string    `json:"definitionId"`
	Name         string    `json:"name"`
	BaseURL      string    `json:"baseUrl,omitempty"`
	Enabled      bool      `json:"enabled"`
	ProxyID      *int64    `json:"proxyId"`
	SolverID     *int64    `json:"solverId"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// settingResponse is one configured setting; a secret's value is the <redacted>
// sentinel.
type settingResponse struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// instanceDetailResponse is an instance plus its (redacted) settings.
type instanceDetailResponse struct {
	instanceResponse
	Settings []settingResponse `json:"settings"`
}

// listIndexers returns all configured indexers.
func (rt *router) listIndexers(w http.ResponseWriter, r *http.Request) {
	list, err := rt.registry.List(r.Context())
	if err != nil {
		rt.writeServiceError(w, "list indexers", err)
		return
	}
	out := make([]instanceResponse, 0, len(list))
	for _, inst := range list {
		out = append(out, toInstanceResponse(inst))
	}
	writeJSON(w, http.StatusOK, out)
}

// addIndexer creates a configured indexer.
func (rt *router) addIndexer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Slug         string            `json:"slug"`
		DefinitionID string            `json:"definitionId"`
		Name         string            `json:"name"`
		BaseURL      string            `json:"baseUrl"`
		Settings     map[string]string `json:"settings"`
		ProxyID      *int64            `json:"proxyId"`
		SolverID     *int64            `json:"solverId"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	inst, err := rt.registry.Add(r.Context(), registry.AddParams{
		Slug: req.Slug, DefinitionID: req.DefinitionID, Name: req.Name,
		BaseURL: req.BaseURL, Settings: req.Settings, ProxyID: req.ProxyID, SolverID: req.SolverID,
	})
	if err != nil {
		rt.writeServiceError(w, "add indexer", err)
		return
	}
	writeJSON(w, http.StatusCreated, toInstanceResponse(inst))
}

// getIndexer returns one indexer with its settings (secrets redacted).
func (rt *router) getIndexer(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	inst, views, err := rt.registry.Get(r.Context(), slug)
	if err != nil {
		rt.writeServiceError(w, "get indexer", err)
		return
	}
	settings := make([]settingResponse, 0, len(views))
	for _, v := range views {
		settings = append(settings, settingResponse{Name: v.Name, Value: v.Value, Secret: v.Secret})
	}
	writeJSON(w, http.StatusOK, instanceDetailResponse{
		instanceResponse: toInstanceResponse(inst),
		Settings:         settings,
	})
}

// updateIndexer merges settings/metadata into an indexer.
func (rt *router) updateIndexer(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var req struct {
		Name     *string           `json:"name"`
		BaseURL  *string           `json:"baseUrl"`
		Settings map[string]string `json:"settings"`
		ProxyID  optionalRef       `json:"proxyId"`
		SolverID optionalRef       `json:"solverId"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.registry.Update(r.Context(), slug, registry.UpdateParams{
		Name: req.Name, BaseURL: req.BaseURL, Settings: req.Settings,
		ProxyID: req.ProxyID.toRegistry(), SolverID: req.SolverID.toRegistry(),
	}); err != nil {
		rt.writeServiceError(w, "update indexer", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteIndexer removes an indexer.
func (rt *router) deleteIndexer(w http.ResponseWriter, r *http.Request) {
	if err := rt.registry.Delete(r.Context(), chi.URLParam(r, "slug")); err != nil {
		rt.writeServiceError(w, "delete indexer", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// enableIndexer enables an indexer.
func (rt *router) enableIndexer(w http.ResponseWriter, r *http.Request) {
	rt.setEnabled(w, r, true)
}

// disableIndexer disables an indexer.
func (rt *router) disableIndexer(w http.ResponseWriter, r *http.Request) {
	rt.setEnabled(w, r, false)
}

// setEnabled is the shared enable/disable handler.
func (rt *router) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	if err := rt.registry.SetEnabled(r.Context(), chi.URLParam(r, "slug"), enabled); err != nil {
		rt.writeServiceError(w, "set enabled", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testResult is the JSON body of the indexer Test action.
type testResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// testIndexer validates a configured indexer's credentials/connectivity via the
// engine's login probe (against a fresh, uncached engine). A passing test returns
// {"ok":true}; a credential/connectivity failure returns 200
// {"ok":false,"error":<sanitized>}; an unknown slug is a 404. The error is
// RedactURL'd and secret-token-scrubbed so a passkey/cookie never reaches the
// client.
func (rt *router) testIndexer(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	switch err := rt.registry.Test(r.Context(), slug); {
	case err == nil:
		writeJSON(w, http.StatusOK, testResult{OK: true})
	case errors.Is(err, database.ErrNotFound):
		rt.writeServiceError(w, "test indexer", err)
	default:
		writeJSON(w, http.StatusOK, testResult{OK: false, Error: apphttp.RedactError(err)})
	}
}

// statusEvent is one health event in the status response (detail already scrubbed
// at write time).
type statusEvent struct {
	Kind       string    `json:"kind"`
	Detail     string    `json:"detail,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

// statusResponse is the JSON body of GET /api/indexers/{slug}/status: the derived
// overall status plus the recent health events behind it.
type statusResponse struct {
	Slug   string        `json:"slug"`
	Status string        `json:"status"`
	Events []statusEvent `json:"events"`
}

// indexerStatus returns a configured indexer's derived health (healthy/unhealthy)
// and its recent health events. An unknown slug is a 404. Details were scrubbed
// before storage, so no credential is surfaced here.
func (rt *router) indexerStatus(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	st, err := rt.registry.Status(r.Context(), slug)
	if err != nil {
		rt.writeServiceError(w, "indexer status", err)
		return
	}
	writeJSON(w, http.StatusOK, toStatusResponse(st))
}

// toStatusResponse maps the registry's health status to its API view.
func toStatusResponse(st registry.HealthStatus) statusResponse {
	events := make([]statusEvent, 0, len(st.Events))
	for _, e := range st.Events {
		events = append(events, statusEvent{Kind: e.Kind, Detail: e.Detail, OccurredAt: e.OccurredAt})
	}
	return statusResponse{Slug: st.Slug, Status: st.Status, Events: events}
}

// toInstanceResponse maps a domain instance to its API view.
func toInstanceResponse(inst domain.IndexerInstance) instanceResponse {
	return instanceResponse{
		ID: inst.ID, Slug: inst.Slug, DefinitionID: inst.DefinitionID, Name: inst.Name,
		BaseURL: inst.BaseURL, Enabled: inst.Enabled, ProxyID: inst.ProxyID, SolverID: inst.SolverID,
		CreatedAt: inst.CreatedAt, UpdatedAt: inst.UpdatedAt,
	}
}
