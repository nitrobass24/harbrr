package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/autobrr/harbrr/internal/apps"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/secrets"
)

// appResponse is the API view of a first-class App (ADR 0004). The credential is never
// echoed — it reads back as the <redacted> sentinel. References counts how many surface
// rows use the app (the "used by N surfaces" list view + the delete-blocked 409).
type appResponse struct {
	ID         int64           `json:"id"`
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	BaseURL    string          `json:"baseUrl"`
	Username   string          `json:"username"`
	APIKey     string          `json:"apiKey"`
	HarbrrURL  string          `json:"harbrrUrl"`
	Enabled    bool            `json:"enabled"`
	References appRefsResponse `json:"references"`
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

// appRefsResponse is an app's per-surface reference counts.
type appRefsResponse struct {
	AppConnections int `json:"appConnections"`
	Announce       int `json:"announce"`
	Download       int `json:"download"`
}

// listApps returns all apps with their reference counts (credentials redacted).
func (rt *router) listApps(w http.ResponseWriter, r *http.Request) {
	list, err := rt.apps.List(r.Context())
	if err != nil {
		rt.writeServiceError(w, "list apps", err)
		return
	}
	out := make([]appResponse, 0, len(list))
	for _, a := range list {
		refs, err := rt.apps.References(r.Context(), a.ID)
		if err != nil {
			rt.writeServiceError(w, "list apps", err)
			return
		}
		out = append(out, toAppResponse(a, refs))
	}
	writeJSON(w, http.StatusOK, out)
}

// getApp returns one app with its reference counts (credential redacted).
func (rt *router) getApp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "app")
	if !ok {
		return
	}
	a, err := rt.apps.Get(r.Context(), id)
	if err != nil {
		rt.writeServiceError(w, "get app", err)
		return
	}
	refs, err := rt.apps.References(r.Context(), id)
	if err != nil {
		rt.writeServiceError(w, "get app", err)
		return
	}
	writeJSON(w, http.StatusOK, toAppResponse(a, refs))
}

// updateApp patches an app's fields and rotates its credential (an omitted apiKey keeps
// the stored one). A rotation propagates to every referencing surface.
func (rt *router) updateApp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "app")
	if !ok {
		return
	}
	var req struct {
		Name      *string `json:"name"`
		BaseURL   *string `json:"baseUrl"`
		Username  *string `json:"username"`
		HarbrrURL *string `json:"harbrrUrl"`
		Enabled   *bool   `json:"enabled"`
		APIKey    *string `json:"apiKey"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.apps.UpdateCredential(r.Context(), id, apps.UpdateParams{
		Name: req.Name, BaseURL: req.BaseURL, Username: req.Username,
		HarbrrURL: req.HarbrrURL, Enabled: req.Enabled, APIKey: req.APIKey,
	}); err != nil {
		rt.writeServiceError(w, "update app", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteApp removes an app, returning 409 (naming the referencing surfaces) when it is
// still in use.
func (rt *router) deleteApp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "app")
	if !ok {
		return
	}
	if err := rt.apps.Delete(r.Context(), id); err != nil {
		rt.writeServiceError(w, "delete app", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// appQuiInstances proxies a qui app's instance list server-side (the browser never sees
// the key). 400 for a non-qui app; a reachability/auth failure is 200
// {"ok":false,"error":<scrubbed>} mirroring the test endpoints; an unknown id 404.
func (rt *router) appQuiInstances(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "app")
	if !ok {
		return
	}
	instances, err := rt.apps.QuiInstances(r.Context(), id)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, toQuiInstancesResponse(instances))
	case errors.Is(err, database.ErrNotFound), errors.Is(err, domain.ErrInvalid):
		rt.writeServiceError(w, "app qui instances", err)
	default:
		writeJSON(w, http.StatusOK, quiInstancesResponse{OK: false, Error: apphttp.RedactError(err)})
	}
}

// quiInstancesResponse carries the proxied instance list plus an ok/error envelope
// (a reachability failure is reported like the test endpoints, not as a 5xx).
type quiInstancesResponse struct {
	OK        bool               `json:"ok"`
	Error     string             `json:"error,omitempty"`
	Instances []apps.QuiInstance `json:"instances,omitempty"`
}

func toQuiInstancesResponse(instances []apps.QuiInstance) quiInstancesResponse {
	return quiInstancesResponse{OK: true, Instances: instances}
}

// toAppResponse maps an app + its reference counts to the API view, redacting the
// credential.
func toAppResponse(a domain.App, refs database.AppReferences) appResponse {
	return appResponse{
		ID: a.ID, Kind: a.Kind, Name: a.Name, BaseURL: a.BaseURL, Username: a.Username,
		APIKey: secrets.Redacted, HarbrrURL: a.HarbrrURL, Enabled: a.Enabled,
		References: appRefsResponse{
			AppConnections: refs.AppConnections, Announce: refs.Announce, Download: refs.Download,
		},
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
}
