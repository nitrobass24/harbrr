package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/domain"
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

// listDefinitions returns the available tracker definitions (memoized on success).
// A first-call failure is surfaced but NOT cached, so the next call retries — a
// transient load blip never wedges the add-indexer UI at 500 until restart. The
// mutex is held across the load to serialize concurrent first-calls (one loads,
// the rest wait then see the cache); acceptable for this rarely-hit endpoint.
func (rt *router) listDefinitions(w http.ResponseWriter, _ *http.Request) {
	defs, err := rt.cachedDefinitions()
	if err != nil {
		rt.writeServiceError(w, "list definitions", err)
		return
	}
	writeJSON(w, http.StatusOK, defs)
}

// cachedDefinitions returns the memoized definitions, loading them once on first
// success. The lock is released via defer so a panic in the load can't leave the
// mutex held (which would wedge every future call — worse than the bug this
// fixes). A failed load leaves defsLoaded false, so the next call retries.
func (rt *router) cachedDefinitions() ([]definitionSummary, error) {
	rt.defsMu.Lock()
	defer rt.defsMu.Unlock()
	if !rt.defsLoaded {
		defs, err := rt.loadDefs()
		if err != nil {
			return nil, err
		}
		rt.defs = defs
		rt.defsLoaded = true
	}
	return rt.defs, nil
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
	ID                      int64     `json:"id"`
	Slug                    string    `json:"slug"`
	DefinitionID            string    `json:"definitionId"`
	Name                    string    `json:"name"`
	BaseURL                 string    `json:"baseUrl,omitempty"`
	Enabled                 bool      `json:"enabled"`
	Protocol                string    `json:"protocol"`
	ProxyID                 *int64    `json:"proxyId"`
	SolverID                *int64    `json:"solverId"`
	Freeleech               bool      `json:"freeleech"`
	Priority                int       `json:"priority"`
	MinSeeders              int       `json:"minSeeders"`
	SyncCategories          []int     `json:"syncCategories"`
	EnableRss               bool      `json:"enableRss"`
	EnableAutomaticSearch   bool      `json:"enableAutomaticSearch"`
	EnableInteractiveSearch bool      `json:"enableInteractiveSearch"`
	CreatedAt               time.Time `json:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt"`
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
		resp := toInstanceResponse(inst)
		freeleech, err := rt.registry.Freeleech(r.Context(), inst)
		if err != nil {
			rt.log.Warn().Err(err).Str("slug", inst.Slug).Msg("resolve freeleech state")
		}
		resp.Freeleech = freeleech
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

// addIndexer creates a configured indexer.
func (rt *router) addIndexer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Slug                    string            `json:"slug"`
		DefinitionID            string            `json:"definitionId"`
		Name                    string            `json:"name"`
		BaseURL                 string            `json:"baseUrl"`
		Settings                map[string]string `json:"settings"`
		ProxyID                 *int64            `json:"proxyId"`
		SolverID                *int64            `json:"solverId"`
		Priority                int               `json:"priority"`
		MinSeeders              int               `json:"minSeeders"`
		SyncCategories          []int             `json:"syncCategories"`
		EnableRss               *bool             `json:"enableRss"`
		EnableAutomaticSearch   *bool             `json:"enableAutomaticSearch"`
		EnableInteractiveSearch *bool             `json:"enableInteractiveSearch"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	inst, err := rt.registry.Add(r.Context(), registry.AddParams{
		Slug: req.Slug, DefinitionID: req.DefinitionID, Name: req.Name,
		BaseURL: req.BaseURL, Settings: req.Settings, ProxyID: req.ProxyID, SolverID: req.SolverID,
		Priority: req.Priority, MinSeeders: req.MinSeeders, SyncCategories: req.SyncCategories,
		EnableRss: req.EnableRss, EnableAutomaticSearch: req.EnableAutomaticSearch,
		EnableInteractiveSearch: req.EnableInteractiveSearch,
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
		Name                    *string           `json:"name"`
		BaseURL                 *string           `json:"baseUrl"`
		Settings                map[string]string `json:"settings"`
		ProxyID                 optionalRef       `json:"proxyId"`
		SolverID                optionalRef       `json:"solverId"`
		Priority                *int              `json:"priority"`
		MinSeeders              *int              `json:"minSeeders"`
		SyncCategories          *[]int            `json:"syncCategories"`
		EnableRss               *bool             `json:"enableRss"`
		EnableAutomaticSearch   *bool             `json:"enableAutomaticSearch"`
		EnableInteractiveSearch *bool             `json:"enableInteractiveSearch"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.registry.Update(r.Context(), slug, registry.UpdateParams{
		Name: req.Name, BaseURL: req.BaseURL, Settings: req.Settings,
		ProxyID: req.ProxyID.toRegistry(), SolverID: req.SolverID.toRegistry(),
		Priority: req.Priority, MinSeeders: req.MinSeeders, SyncCategories: req.SyncCategories,
		EnableRss: req.EnableRss, EnableAutomaticSearch: req.EnableAutomaticSearch,
		EnableInteractiveSearch: req.EnableInteractiveSearch,
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
	rt.testEndpoint(w, r, "test indexer", func(ctx context.Context) error {
		return rt.registry.Test(ctx, slug)
	})
}

// statusEvent is one health event in the status response (detail already scrubbed
// at write time).
type statusEvent struct {
	Kind       string    `json:"kind"`
	Detail     string    `json:"detail,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

// statusResponse is the JSON body of GET /api/indexers/{slug}/status: the derived
// overall status plus the recent health events behind it. DisabledTill is present
// only while the circuit breaker (#253) currently excludes the indexer from
// dispatch — the UI can diff it against now for a short-term/long-term read.
type statusResponse struct {
	Slug         string        `json:"slug"`
	Status       string        `json:"status"`
	Events       []statusEvent `json:"events"`
	DisabledTill *time.Time    `json:"disabledTill,omitempty"`
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
	return statusResponse{Slug: st.Slug, Status: st.Status, Events: events, DisabledTill: st.DisabledTill}
}

// fleetIndexerStatus is one indexer's entry in the fleet-wide status roll-up: its
// derived status plus the most recent health event (reusing statusEvent's shape),
// omitted when the indexer has no events. DisabledTill mirrors statusResponse.
type fleetIndexerStatus struct {
	Slug         string       `json:"slug"`
	Status       string       `json:"status"`
	LastEvent    *statusEvent `json:"lastEvent,omitempty"`
	DisabledTill *time.Time   `json:"disabledTill,omitempty"`
}

// fleetStatusResponse is the JSON body of GET /api/indexers/status: healthy/unhealthy
// counts across the fleet plus each configured indexer's derived status, sorted by slug.
type fleetStatusResponse struct {
	Healthy   int                  `json:"healthy"`
	Unhealthy int                  `json:"unhealthy"`
	Indexers  []fleetIndexerStatus `json:"indexers"`
}

// allIndexerStatus returns the fleet-wide health roll-up: healthy/unhealthy counts
// plus every configured indexer's derived status and most recent health event.
func (rt *router) allIndexerStatus(w http.ResponseWriter, r *http.Request) {
	statuses, err := rt.registry.AllStatuses(r.Context())
	if err != nil {
		rt.writeServiceError(w, "all indexer status", err)
		return
	}
	out := fleetStatusResponse{Indexers: make([]fleetIndexerStatus, 0, len(statuses))}
	for _, st := range statuses {
		if st.Status == "healthy" {
			out.Healthy++
		} else {
			out.Unhealthy++
		}
		out.Indexers = append(out.Indexers, toFleetIndexerStatus(st))
	}
	writeJSON(w, http.StatusOK, out)
}

// toFleetIndexerStatus maps a registry FleetStatus to its API view, reusing
// statusEvent for the most recent event (nil when the indexer has none).
func toFleetIndexerStatus(st registry.FleetStatus) fleetIndexerStatus {
	fs := fleetIndexerStatus{Slug: st.Slug, Status: st.Status, DisabledTill: st.DisabledTill}
	if len(st.Events) > 0 {
		e := st.Events[0]
		fs.LastEvent = &statusEvent{Kind: e.Kind, Detail: e.Detail, OccurredAt: e.OccurredAt}
	}
	return fs
}

// toInstanceResponse maps a domain instance to its API view.
func toInstanceResponse(inst domain.IndexerInstance) instanceResponse {
	cats := inst.SyncCategories
	if cats == nil {
		cats = []int{}
	}
	return instanceResponse{
		ID: inst.ID, Slug: inst.Slug, DefinitionID: inst.DefinitionID, Name: inst.Name,
		BaseURL: inst.BaseURL, Enabled: inst.Enabled, Protocol: inst.Protocol,
		ProxyID: inst.ProxyID, SolverID: inst.SolverID,
		Priority: inst.Priority, MinSeeders: inst.MinSeeders, SyncCategories: cats,
		EnableRss: inst.EnableRss, EnableAutomaticSearch: inst.EnableAutomaticSearch,
		EnableInteractiveSearch: inst.EnableInteractiveSearch,
		CreatedAt:               inst.CreatedAt, UpdatedAt: inst.UpdatedAt,
	}
}
