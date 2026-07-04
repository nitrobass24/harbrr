package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/proxy"
	"github.com/autobrr/harbrr/internal/secrets"
)

// proxyResponse is the API view of a proxy resource. The URL (which may embed
// user:pass) is never echoed — it reads back as the <redacted> sentinel.
type proxyResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// listProxies returns all proxies (URLs redacted).
func (rt *router) listProxies(w http.ResponseWriter, r *http.Request) {
	list, err := rt.proxy.List(r.Context())
	if err != nil {
		rt.writeServiceError(w, "list proxies", err)
		return
	}
	out := make([]proxyResponse, 0, len(list))
	for _, p := range list {
		out = append(out, toProxyResponse(p))
	}
	writeJSON(w, http.StatusOK, out)
}

// createProxy adds a proxy with its URL encrypted.
func (rt *router) createProxy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := rt.proxy.Create(r.Context(), proxy.CreateParams{Name: req.Name, Type: req.Type, URL: req.URL})
	if err != nil {
		rt.writeServiceError(w, "create proxy", err)
		return
	}
	writeJSON(w, http.StatusCreated, toProxyResponse(p))
}

// getProxy returns one proxy (URL redacted).
func (rt *router) getProxy(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	p, err := rt.proxy.Get(r.Context(), id)
	if err != nil {
		rt.writeServiceError(w, "get proxy", err)
		return
	}
	writeJSON(w, http.StatusOK, toProxyResponse(p))
}

// updateProxy patches a proxy (a new url rotates the endpoint).
func (rt *router) updateProxy(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name *string `json:"name"`
		Type *string `json:"type"`
		URL  *string `json:"url"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.proxy.Update(r.Context(), id, proxy.UpdateParams{Name: req.Name, Type: req.Type, URL: req.URL}); err != nil {
		rt.writeServiceError(w, "update proxy", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteProxy removes a proxy (referencing indexers fall back to no proxy).
func (rt *router) deleteProxy(w http.ResponseWriter, r *http.Request) {
	id, ok := proxyID(w, r)
	if !ok {
		return
	}
	if err := rt.proxy.Delete(r.Context(), id); err != nil {
		rt.writeServiceError(w, "delete proxy", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// proxyID parses the {id} path param, writing a 400 on a malformed value.
func proxyID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid proxy id")
		return 0, false
	}
	return id, true
}

// toProxyResponse maps a proxy to its API view, redacting the URL.
func toProxyResponse(p domain.Proxy) proxyResponse {
	return proxyResponse{
		ID: p.ID, Name: p.Name, Type: p.Type, URL: secrets.Redacted,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}
