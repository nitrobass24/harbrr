package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

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
	rt.defsOnce.Do(func() { rt.defs, rt.defsErr = loadDefinitionSummaries(rt.loader) })
	if rt.defsErr != nil {
		rt.writeServiceError(w, "list definitions", rt.defsErr)
		return
	}
	writeJSON(w, http.StatusOK, rt.defs)
}

// loadDefinitionSummaries loads and summarizes all definitions, sorted by id.
func loadDefinitionSummaries(l *loader.Loader) ([]definitionSummary, error) {
	defs, _, err := l.LoadAll()
	if err != nil {
		return nil, fmt.Errorf("api: load definitions: %w", err)
	}
	out := make([]definitionSummary, 0, len(defs))
	for _, d := range defs {
		out = append(out, definitionSummary{
			ID: d.ID, Name: d.Name, Description: d.Description, Type: d.Type, Language: d.Language,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// instanceResponse is the API view of a configured indexer (no secrets).
type instanceResponse struct {
	Slug         string    `json:"slug"`
	DefinitionID string    `json:"definitionId"`
	Name         string    `json:"name"`
	BaseURL      string    `json:"baseUrl,omitempty"`
	Enabled      bool      `json:"enabled"`
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
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	inst, err := rt.registry.Add(r.Context(), registry.AddParams{
		Slug: req.Slug, DefinitionID: req.DefinitionID, Name: req.Name,
		BaseURL: req.BaseURL, Settings: req.Settings,
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
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.registry.Update(r.Context(), slug, registry.UpdateParams{
		Name: req.Name, BaseURL: req.BaseURL, Settings: req.Settings,
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
		writeJSON(w, http.StatusOK, testResult{OK: false, Error: sanitizeTestError(err)})
	}
}

// secretTokenRe matches a credential-shaped key and its value (in plain text or a
// URL query) so the value can be scrubbed from a test-failure message. The value
// run stops at whitespace and the URL/quote delimiters & " ' so surrounding error
// context (e.g. "dial tcp") and other query params survive. Defense in depth: the
// engine's login errors are credential-free by construction and transport-error
// URLs are already redacted in the login layer, but a credential must never reach
// the client through this surface.
var secretTokenRe = regexp.MustCompile(`(?i)(cookie|passkey|api_?key|auth_?key|rss_?key|torrent_pass|passid|passphrase|password|secret|token|downloadtoken|2fa|otp)([=:]\s*)[^\s&"']+`)

// authHeaderRe scrubs an Authorization header value (with or without a
// scheme like Bearer/Basic), since the scheme + token can span a space that
// secretTokenRe's value run would not cover. Per the AGENTS.md redaction rule
// (Authorization/Cookie headers are never emitted).
var authHeaderRe = regexp.MustCompile(`(?i)(authorization)(\s*[=:]\s*)(?:bearer|basic|digest|negotiate)?\s*\S+`)

// sanitizeTestError renders a test/login error safe to return to the client:
// every credential-shaped key=value / key: value pair (and any Authorization
// header) has its value replaced with <redacted>. It is applied to plain error
// text only (not URL-encoded), so it never mangles human-readable messages.
func sanitizeTestError(err error) string {
	msg := authHeaderRe.ReplaceAllString(err.Error(), "${1}${2}<redacted>")
	return secretTokenRe.ReplaceAllString(msg, "${1}${2}<redacted>")
}

// toInstanceResponse maps a domain instance to its API view.
func toInstanceResponse(inst domain.IndexerInstance) instanceResponse {
	return instanceResponse{
		Slug: inst.Slug, DefinitionID: inst.DefinitionID, Name: inst.Name,
		BaseURL: inst.BaseURL, Enabled: inst.Enabled,
		CreatedAt: inst.CreatedAt, UpdatedAt: inst.UpdatedAt,
	}
}
