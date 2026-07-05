package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/domain"
)

// syncProfileResponse is the API view of a sync profile. categories is always a
// (possibly empty) array, never null, so clients can iterate it unconditionally.
type syncProfileResponse struct {
	ID                      int64     `json:"id"`
	Name                    string    `json:"name"`
	Categories              []int     `json:"categories"`
	MinSeeders              int       `json:"minSeeders"`
	EnableRss               bool      `json:"enableRss"`
	EnableAutomaticSearch   bool      `json:"enableAutomaticSearch"`
	EnableInteractiveSearch bool      `json:"enableInteractiveSearch"`
	CreatedAt               time.Time `json:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

// listSyncProfiles returns all sync profiles.
func (rt *router) listSyncProfiles(w http.ResponseWriter, r *http.Request) {
	list, err := rt.appsync.ListProfiles(r.Context())
	if err != nil {
		rt.writeServiceError(w, "list sync profiles", err)
		return
	}
	out := make([]syncProfileResponse, 0, len(list))
	for _, p := range list {
		out = append(out, toSyncProfileResponse(p))
	}
	writeJSON(w, http.StatusOK, out)
}

// createSyncProfile adds a sync profile (unique name → 409).
func (rt *router) createSyncProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                    string `json:"name"`
		Categories              []int  `json:"categories"`
		MinSeeders              int    `json:"minSeeders"`
		EnableRss               *bool  `json:"enableRss"`
		EnableAutomaticSearch   *bool  `json:"enableAutomaticSearch"`
		EnableInteractiveSearch *bool  `json:"enableInteractiveSearch"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := rt.appsync.CreateProfile(r.Context(), appsync.CreateProfileParams{
		Name: req.Name, Categories: req.Categories, MinSeeders: req.MinSeeders,
		EnableRss: req.EnableRss, EnableAutomaticSearch: req.EnableAutomaticSearch,
		EnableInteractiveSearch: req.EnableInteractiveSearch,
	})
	if err != nil {
		rt.writeServiceError(w, "create sync profile", err)
		return
	}
	writeJSON(w, http.StatusCreated, toSyncProfileResponse(p))
}

// getSyncProfile returns one sync profile.
func (rt *router) getSyncProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := syncProfileID(w, r)
	if !ok {
		return
	}
	p, err := rt.appsync.GetProfile(r.Context(), id)
	if err != nil {
		rt.writeServiceError(w, "get sync profile", err)
		return
	}
	writeJSON(w, http.StatusOK, toSyncProfileResponse(p))
}

// updateSyncProfile patches a sync profile (present-empty categories clears the set).
func (rt *router) updateSyncProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := syncProfileID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name                    *string `json:"name"`
		Categories              *[]int  `json:"categories"`
		MinSeeders              *int    `json:"minSeeders"`
		EnableRss               *bool   `json:"enableRss"`
		EnableAutomaticSearch   *bool   `json:"enableAutomaticSearch"`
		EnableInteractiveSearch *bool   `json:"enableInteractiveSearch"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.appsync.UpdateProfile(r.Context(), id, appsync.UpdateProfileParams{
		Name: req.Name, Categories: req.Categories, MinSeeders: req.MinSeeders,
		EnableRss: req.EnableRss, EnableAutomaticSearch: req.EnableAutomaticSearch,
		EnableInteractiveSearch: req.EnableInteractiveSearch,
	}); err != nil {
		rt.writeServiceError(w, "update sync profile", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteSyncProfile removes a sync profile (referencing connections revert to defaults).
func (rt *router) deleteSyncProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := syncProfileID(w, r)
	if !ok {
		return
	}
	if err := rt.appsync.DeleteProfile(r.Context(), id); err != nil {
		rt.writeServiceError(w, "delete sync profile", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// syncProfileID parses the {id} path param, writing a 400 on a malformed value.
func syncProfileID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid sync profile id")
		return 0, false
	}
	return id, true
}

// toSyncProfileResponse maps a sync profile to its API view (categories never null).
func toSyncProfileResponse(p domain.SyncProfile) syncProfileResponse {
	cats := p.Categories
	if cats == nil {
		cats = []int{}
	}
	return syncProfileResponse{
		ID: p.ID, Name: p.Name, Categories: cats, MinSeeders: p.MinSeeders,
		EnableRss: p.EnableRss, EnableAutomaticSearch: p.EnableAutomaticSearch,
		EnableInteractiveSearch: p.EnableInteractiveSearch,
		CreatedAt:               p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}
