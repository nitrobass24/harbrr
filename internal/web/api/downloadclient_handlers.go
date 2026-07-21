package api

import (
	"context"
	"net/http"
	"time"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/download"
	"github.com/autobrr/harbrr/internal/secrets"
)

// downloadClientResponse is the API view of a download client. The secret is
// never echoed — it reads back as the <redacted> sentinel.
type downloadClientResponse struct {
	ID        int64                         `json:"id"`
	Name      string                        `json:"name"`
	Kind      string                        `json:"kind"`
	AppID     *int64                        `json:"appId,omitempty"`
	Enabled   bool                          `json:"enabled"`
	Host      string                        `json:"host"`
	Username  string                        `json:"username"`
	Secret    string                        `json:"secret"`
	Settings  domain.DownloadClientSettings `json:"settings"`
	CreatedAt time.Time                     `json:"createdAt"`
	UpdatedAt time.Time                     `json:"updatedAt"`
}

// listDownloadClients returns all download clients (secrets redacted).
func (rt *router) listDownloadClients(w http.ResponseWriter, r *http.Request) {
	list, err := rt.download.List(r.Context())
	if err != nil {
		rt.writeServiceError(w, "list download clients", err)
		return
	}
	out := make([]downloadClientResponse, 0, len(list))
	for _, c := range list {
		out = append(out, toDownloadClientResponse(c))
	}
	writeJSON(w, http.StatusOK, out)
}

// createDownloadClient adds a download client with its secret encrypted.
func (rt *router) createDownloadClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string                        `json:"name"`
		Kind     string                        `json:"kind"`
		AppID    *int64                        `json:"appId"`
		Host     string                        `json:"host"`
		Username string                        `json:"username"`
		Secret   string                        `json:"secret"`
		Settings domain.DownloadClientSettings `json:"settings"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	c, err := rt.download.Create(r.Context(), download.CreateParams{
		Name: req.Name, Kind: req.Kind, AppID: req.AppID, Host: req.Host, Username: req.Username,
		Secret: req.Secret, Settings: req.Settings,
	})
	if err != nil {
		rt.writeServiceError(w, "create download client", err)
		return
	}
	writeJSON(w, http.StatusCreated, toDownloadClientResponse(c))
}

// getDownloadClient returns one download client (secret redacted).
func (rt *router) getDownloadClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "download client")
	if !ok {
		return
	}
	c, err := rt.download.Get(r.Context(), id)
	if err != nil {
		rt.writeServiceError(w, "get download client", err)
		return
	}
	writeJSON(w, http.StatusOK, toDownloadClientResponse(c))
}

// updateDownloadClient patches a download client (an omitted secret keeps the
// stored one; Kind is immutable and not accepted here).
func (rt *router) updateDownloadClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "download client")
	if !ok {
		return
	}
	var req struct {
		Name     *string                        `json:"name"`
		Settings *domain.DownloadClientSettings `json:"settings"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := rt.download.Update(r.Context(), id, download.UpdateParams{
		Name: req.Name, Settings: req.Settings,
	})
	if err != nil {
		rt.writeServiceError(w, "update download client", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteDownloadClient removes a download client.
func (rt *router) deleteDownloadClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "download client")
	if !ok {
		return
	}
	if err := rt.download.Delete(r.Context(), id); err != nil {
		rt.writeServiceError(w, "delete download client", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// enableDownloadClient / disableDownloadClient toggle a client.
func (rt *router) enableDownloadClient(w http.ResponseWriter, r *http.Request) {
	rt.setResourceEnabled(w, r, "download client", "set download client enabled", rt.download.SetEnabled, true)
}

func (rt *router) disableDownloadClient(w http.ResponseWriter, r *http.Request) {
	rt.setResourceEnabled(w, r, "download client", "set download client enabled", rt.download.SetEnabled, false)
}

// testDownloadClient confirms the configured client is reachable with its
// stored credentials. A pass is {"ok":true}; a connection failure is 200
// {"ok":false,"error":<scrubbed>}; an unknown id 404.
func (rt *router) testDownloadClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "download client")
	if !ok {
		return
	}
	rt.testEndpoint(w, r, "test download client", func(ctx context.Context) error {
		return rt.download.TestConnection(ctx, id)
	})
}

// toDownloadClientResponse maps a client to its API view, redacting the secret.
func toDownloadClientResponse(c domain.DownloadClient) downloadClientResponse {
	return downloadClientResponse{
		ID: c.ID, Name: c.Name, Kind: c.Kind, AppID: c.AppID, Enabled: c.Enabled, Host: c.Host, Username: c.Username,
		Secret: secrets.Redacted, Settings: c.Settings, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}
