package api

import (
	"net/http"
	"time"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/proxy"
)

// proxyResponse is the API view of a proxy resource. Host/port/username are
// plain (no masking); the password is never echoed — it is omitted entirely,
// not redacted.
type proxyResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// listProxies returns all proxies (passwords omitted).
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

// createProxy adds a proxy with its password encrypted.
func (rt *router) createProxy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := rt.proxy.Create(r.Context(), proxy.CreateParams{
		Name: req.Name, Type: req.Type, Host: req.Host, Port: req.Port, Username: req.Username, Password: req.Password,
	})
	if err != nil {
		rt.writeServiceError(w, "create proxy", err)
		return
	}
	writeJSON(w, http.StatusCreated, toProxyResponse(p))
}

// getProxy returns one proxy (password omitted).
func (rt *router) getProxy(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "proxy")
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

// updateProxy patches a proxy (an omitted password keeps the stored one).
func (rt *router) updateProxy(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "proxy")
	if !ok {
		return
	}
	var req struct {
		Name     *string `json:"name"`
		Type     *string `json:"type"`
		Host     *string `json:"host"`
		Port     *int    `json:"port"`
		Username *string `json:"username"`
		Password *string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := rt.proxy.Update(r.Context(), id, proxy.UpdateParams{
		Name: req.Name, Type: req.Type, Host: req.Host, Port: req.Port, Username: req.Username, Password: req.Password,
	})
	if err != nil {
		rt.writeServiceError(w, "update proxy", err)
		return
	}
	// A cached engine bakes in the resolved proxy URL/transport, so evict them.
	rt.registry.InvalidateAll()
	w.WriteHeader(http.StatusNoContent)
}

// deleteProxy removes a proxy (referencing indexers fall back to no proxy).
func (rt *router) deleteProxy(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "proxy")
	if !ok {
		return
	}
	if err := rt.proxy.Delete(r.Context(), id); err != nil {
		rt.writeServiceError(w, "delete proxy", err)
		return
	}
	// The FK nulled proxy_id in the DB, but cached engines still tunnel through the
	// deleted proxy until evicted.
	rt.registry.InvalidateAll()
	w.WriteHeader(http.StatusNoContent)
}

// toProxyResponse maps a proxy to its API view; the password is never included.
func toProxyResponse(p domain.Proxy) proxyResponse {
	return proxyResponse{
		ID: p.ID, Name: p.Name, Type: p.Type, Host: p.Host, Port: p.Port, Username: p.Username,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}
