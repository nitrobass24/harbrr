package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	tzn "github.com/autobrr/harbrr/internal/torznab"
)

// capabilitiesResponse is the API view of an indexer's search capabilities. The
// internal tracker<->newznab CategoryMap is deliberately not serialized.
type capabilitiesResponse struct {
	Modes             map[string][]string `json:"modes"`
	AllowRawSearch    bool                `json:"allowRawSearch"`
	AllowTVSearchIMDB bool                `json:"allowTVSearchIMDB"`
	Categories        []categoryResponse  `json:"categories"`
	DefaultCategories []string            `json:"defaultCategories,omitempty"`
	Limits            limitsResponse      `json:"limits"`
}

// categoryResponse is one advertised category.
type categoryResponse struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	IsCustom bool   `json:"isCustom"`
	IsParent bool   `json:"isParent"`
	Parent   string `json:"parent,omitempty"`
}

// limitsResponse is the advertised page-size limit (default == max == 100).
type limitsResponse struct {
	Default int `json:"default"`
	Max     int `json:"max"`
}

// indexerCapabilities returns a configured indexer's search modes, category tree,
// and limits as JSON. An unknown or disabled slug is a 404.
func (rt *router) indexerCapabilities(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	idx, ok := rt.registry.Indexer(r.Context(), slug)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, toCapabilitiesResponse(idx.Capabilities()))
}

// toCapabilitiesResponse maps the engine capabilities to the API view, excluding
// the internal CategoryMap lookup (it is not part of the public contract).
func toCapabilitiesResponse(caps *mapper.Capabilities) capabilitiesResponse {
	cats := make([]categoryResponse, 0, len(caps.Categories))
	for _, c := range caps.Categories {
		cr := categoryResponse{ID: c.ID, Name: c.Name, IsCustom: c.IsCustom(), IsParent: c.IsParent()}
		if !cr.IsParent {
			cr.Parent = c.Parent()
		}
		cats = append(cats, cr)
	}
	return capabilitiesResponse{
		Modes:             caps.Modes,
		AllowRawSearch:    caps.AllowRawSearch,
		AllowTVSearchIMDB: caps.AllowTVSearchIMDB,
		Categories:        cats,
		DefaultCategories: caps.DefaultCategories,
		Limits:            limitsResponse{Default: tzn.LimitsDefault, Max: tzn.LimitsMax},
	}
}
