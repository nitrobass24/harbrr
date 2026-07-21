package api

import (
	"net/http"
	"time"

	"github.com/autobrr/harbrr/internal/domain"
)

// apiKeyResponse is the API view of a key — never includes the key or its hash.
type apiKeyResponse struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// listAPIKeys returns all API keys (metadata only).
func (rt *router) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := rt.auth.ListAPIKeys(r.Context())
	if err != nil {
		rt.writeServiceError(w, "list api keys", err)
		return
	}
	out := make([]apiKeyResponse, 0, len(keys))
	for _, k := range keys {
		out = append(out, toAPIKeyResponse(k))
	}
	writeJSON(w, http.StatusOK, out)
}

// mintResponse carries the plaintext key, shown to the caller exactly once.
type mintResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"createdAt"`
}

// mintAPIKey creates a new API key and returns the plaintext once.
func (rt *router) mintAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	plaintext, k, err := rt.auth.MintAPIKey(r.Context(), req.Name)
	if err != nil {
		rt.writeServiceError(w, "mint api key", err)
		return
	}
	writeJSON(w, http.StatusCreated, mintResponse{ID: k.ID, Name: k.Name, Key: plaintext, CreatedAt: k.CreatedAt})
}

// deleteAPIKey revokes an API key by id.
func (rt *router) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "api key")
	if !ok {
		return
	}
	if err := rt.auth.RevokeAPIKey(r.Context(), id); err != nil {
		rt.writeServiceError(w, "revoke api key", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// toAPIKeyResponse maps a domain key to its API view.
func toAPIKeyResponse(k domain.APIKey) apiKeyResponse {
	return apiKeyResponse{ID: k.ID, Name: k.Name, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt}
}
