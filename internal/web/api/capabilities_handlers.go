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
	// UpstreamLimits is the indexer's own advertised request-count limit — for Newznab, the
	// remote `?t=caps` <limits max= default=> element (#250). It defaults to 100/100
	// (Prowlarr's convention) when the source has none. Measure-only: nothing enforces a
	// budget against it yet (#251).
	UpstreamLimits limitsResponse `json:"upstreamLimits"`
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
	writeJSON(w, http.StatusOK, toCapabilitiesResponse(idx.Capabilities(), rt.adultCats.Hidden()))
}

// toCapabilitiesResponse maps the engine capabilities to the API view, excluding
// the internal CategoryMap lookup (it is not part of the public contract). When
// hideAdult is set the XXX taxonomy is omitted from the category list, which is
// how every current and future category picker inherits the operator's
// hide-adult-categories choice without knowing about it (autobrr/harbrr#383).
// Only this management view is filtered — the Torznab/Newznab caps document is
// served unchanged.
func toCapabilitiesResponse(caps *mapper.Capabilities, hideAdult bool) capabilitiesResponse {
	var hidden map[int]struct{}
	if hideAdult {
		hidden = caps.AdultCategoryIDs()
	}
	cats := make([]categoryResponse, 0, len(caps.Categories))
	for _, c := range caps.Categories {
		if _, drop := hidden[c.ID]; drop {
			continue
		}
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
		UpstreamLimits:    limitsResponse{Default: caps.Limits.Default, Max: caps.Limits.Max},
	}
}
