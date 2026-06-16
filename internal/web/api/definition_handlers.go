package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
)

// settingFieldResponse is one configurable field in a definition's settings schema
// (used to render an add-indexer form). secret marks a credential the operator must
// supply but harbrr stores encrypted and never echoes back.
type settingFieldResponse struct {
	Name    string            `json:"name"`
	Label   string            `json:"label,omitempty"`
	Type    string            `json:"type"`
	Default string            `json:"default,omitempty"`
	Options map[string]string `json:"options,omitempty"`
	Secret  bool              `json:"secret"`
}

// definitionDetailResponse is a definition's full schema: the summary, its ordered
// settings fields, and its capabilities. This is the DEFINITION schema (cleartext
// labels/defaults + a secret flag), not a configured instance's stored settings, so
// it carries no instance secret.
type definitionDetailResponse struct {
	definitionSummary
	Settings []settingFieldResponse `json:"settings"`
	Caps     capabilitiesResponse   `json:"caps"`
}

// getDefinition returns a single definition's settings-field schema and
// capabilities, composing the vendored corpus and the native catalog. An unknown id
// is a 404 (a traversal-shaped id is rejected by the loader and falls through to the
// not-found path).
func (rt *router) getDefinition(w http.ResponseWriter, r *http.Request) {
	def := rt.lookupDefinition(chi.URLParam(r, "id"))
	if def == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	caps, err := mapper.Build(def)
	if err != nil {
		rt.writeServiceError(w, "definition capabilities", err)
		return
	}
	writeJSON(w, http.StatusOK, toDefinitionDetail(def, caps))
}

// lookupDefinition resolves a definition id to a vendored (loader, which validates
// the id against path traversal) or native (registry catalog) definition, or nil
// when it is unknown.
func (rt *router) lookupDefinition(id string) *loader.Definition {
	if def, err := rt.loader.Load(id); err == nil {
		return def
	}
	for _, d := range rt.registry.NativeDefinitions() {
		if d.ID == id {
			return d
		}
	}
	return nil
}

// toDefinitionDetail maps a definition + its built caps to the API view.
func toDefinitionDetail(def *loader.Definition, caps *mapper.Capabilities) definitionDetailResponse {
	settings := make([]settingFieldResponse, 0, len(def.Settings))
	for _, s := range def.Settings {
		sf := settingFieldResponse{
			Name: s.Name, Label: s.Label, Type: s.Type, Options: s.Options, Secret: s.IsSecret(),
		}
		if s.Default != nil {
			sf.Default = s.Default.String()
		}
		settings = append(settings, sf)
	}
	return definitionDetailResponse{
		definitionSummary: summaryOf(def),
		Settings:          settings,
		Caps:              toCapabilitiesResponse(caps),
	}
}
