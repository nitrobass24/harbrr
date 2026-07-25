package api

import (
	"net/http"
	"time"

	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/domain"
)

// syncProfileResponse is the API view of a sync profile — a pure routing set since
// #365. indexerIds is always a (possibly empty) array, never null, so clients can
// iterate it unconditionally.
type syncProfileResponse struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	IndexerIDs []int64   `json:"indexerIds"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
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
		Name       string  `json:"name"`
		IndexerIDs []int64 `json:"indexerIds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := rt.appsync.CreateProfile(r.Context(), appsync.CreateProfileParams{
		Name: req.Name, IndexerIDs: req.IndexerIDs,
	})
	if err != nil {
		rt.writeServiceError(w, "create sync profile", err)
		return
	}
	writeJSON(w, http.StatusCreated, toSyncProfileResponse(p))
}

// getSyncProfile returns one sync profile.
func (rt *router) getSyncProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "sync profile")
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

// updateSyncProfile patches a sync profile (present-empty indexerIds clears the
// selection — every compatible indexer).
func (rt *router) updateSyncProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "sync profile")
	if !ok {
		return
	}
	var req struct {
		Name       *string  `json:"name"`
		IndexerIDs *[]int64 `json:"indexerIds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.appsync.UpdateProfile(r.Context(), id, appsync.UpdateProfileParams{
		Name: req.Name, IndexerIDs: req.IndexerIDs,
	}); err != nil {
		rt.writeServiceError(w, "update sync profile", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteSyncProfile removes a sync profile (refused with a 409 while any connection
// still references it).
func (rt *router) deleteSyncProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "sync profile")
	if !ok {
		return
	}
	if err := rt.appsync.DeleteProfile(r.Context(), id); err != nil {
		rt.writeServiceError(w, "delete sync profile", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// toSyncProfileResponse maps a sync profile to its API view (indexerIds never null).
func toSyncProfileResponse(p domain.SyncProfile) syncProfileResponse {
	ids := p.IndexerIDs
	if ids == nil {
		ids = []int64{}
	}
	return syncProfileResponse{
		ID: p.ID, Name: p.Name, IndexerIDs: ids,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}
