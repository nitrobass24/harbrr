package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/announce"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/secrets"
)

// announceConnectionResponse is the API view of a cross-seed announce connection. The
// tool's API key is never echoed — it reads back as the <redacted> sentinel.
type announceConnectionResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	BaseURL   string    `json:"baseUrl"`
	HarbrrURL string    `json:"harbrrUrl,omitempty"`
	APIKey    string    `json:"apiKey"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// listAnnounceConnections returns all configured announce targets (tool keys redacted).
func (rt *router) listAnnounceConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := rt.announce.ListConnections(r.Context())
	if err != nil {
		rt.writeServiceError(w, "list announce connections", err)
		return
	}
	out := make([]announceConnectionResponse, 0, len(conns))
	for _, c := range conns {
		out = append(out, toAnnounceResponse(c))
	}
	writeJSON(w, http.StatusOK, out)
}

// createAnnounceConnection adds an announce target and mints its dedicated harbrr key.
func (rt *router) createAnnounceConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		BaseURL   string `json:"baseUrl"`
		APIKey    string `json:"apiKey"`
		HarbrrURL string `json:"harbrrUrl"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	conn, err := rt.announce.CreateConnection(r.Context(), announce.CreateConnectionParams{
		Name: req.Name, Kind: req.Kind, BaseURL: req.BaseURL, APIKey: req.APIKey, HarbrrURL: req.HarbrrURL,
	})
	if err != nil {
		rt.writeServiceError(w, "create announce connection", err)
		return
	}
	writeJSON(w, http.StatusCreated, toAnnounceResponse(conn))
}

// createAnnounceTargetFromAppConnection seeds a new qui announce-connection from an
// existing qui app-connection: it reuses the app-connection's base URL, decrypted qui
// API key, and harbrr URL, minting a fresh dedicated harbrr key for the new row (issue
// #72 — one-click announce target, no re-entering credentials). QuiSeed rejects
// non-qui kinds; CreateConnection's existing (kind, base_url) unique constraint
// rejects a duplicate target with 409.
func (rt *router) createAnnounceTargetFromAppConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := rt.connectionID(w, r)
	if !ok {
		return
	}
	seed, err := rt.appsync.QuiSeed(r.Context(), id)
	if err != nil {
		rt.writeServiceError(w, "seed announce target", err)
		return
	}
	conn, err := rt.announce.CreateConnection(r.Context(), announce.CreateConnectionParams{
		Name: seed.Name, Kind: domain.AnnounceKindQui,
		BaseURL: seed.BaseURL, APIKey: seed.APIKey, HarbrrURL: seed.HarbrrURL,
	})
	if err != nil {
		rt.writeServiceError(w, "seed announce target", err)
		return
	}
	writeJSON(w, http.StatusCreated, toAnnounceResponse(conn))
}

// getAnnounceConnection returns one announce connection (tool key redacted).
func (rt *router) getAnnounceConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := announceConnectionID(w, r)
	if !ok {
		return
	}
	conn, err := rt.announce.GetConnection(r.Context(), id)
	if err != nil {
		rt.writeServiceError(w, "get announce connection", err)
		return
	}
	writeJSON(w, http.StatusOK, toAnnounceResponse(conn))
}

// updateAnnounceConnection patches an announce target. apiKey follows the pointer-omit
// convention (a new key rotates the tool credential; an omitted apiKey keeps the stored
// one — the client never re-submits the <redacted> sentinel).
func (rt *router) updateAnnounceConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := announceConnectionID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name      *string `json:"name"`
		BaseURL   *string `json:"baseUrl"`
		APIKey    *string `json:"apiKey"`
		HarbrrURL *string `json:"harbrrUrl"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.announce.UpdateConnection(r.Context(), id, announce.UpdateConnectionParams{
		Name: req.Name, BaseURL: req.BaseURL, APIKey: req.APIKey, HarbrrURL: req.HarbrrURL,
	}); err != nil {
		rt.writeServiceError(w, "update announce connection", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testAnnounceConnection probes the target without injecting anything (qui validates its
// API key; cross-seed v6 checks reachability only). A pass is {"ok":true}; a failure is
// 200 {"ok":false,"error":<scrubbed>}; an unknown id 404.
func (rt *router) testAnnounceConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := announceConnectionID(w, r)
	if !ok {
		return
	}
	switch err := rt.announce.TestConnection(r.Context(), id); {
	case err == nil:
		writeJSON(w, http.StatusOK, testResult{OK: true})
	case errors.Is(err, database.ErrNotFound):
		rt.writeServiceError(w, "test announce connection", err)
	default:
		writeJSON(w, http.StatusOK, testResult{OK: false, Error: apphttp.RedactError(err)})
	}
}

// deleteAnnounceConnection removes a connection and revokes its minted key.
func (rt *router) deleteAnnounceConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := announceConnectionID(w, r)
	if !ok {
		return
	}
	if err := rt.announce.DeleteConnection(r.Context(), id); err != nil {
		rt.writeServiceError(w, "delete announce connection", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (rt *router) enableAnnounceConnection(w http.ResponseWriter, r *http.Request) {
	rt.setAnnounceEnabled(w, r, true)
}

func (rt *router) disableAnnounceConnection(w http.ResponseWriter, r *http.Request) {
	rt.setAnnounceEnabled(w, r, false)
}

func (rt *router) setAnnounceEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id, ok := announceConnectionID(w, r)
	if !ok {
		return
	}
	if err := rt.announce.SetEnabled(r.Context(), id, enabled); err != nil {
		rt.writeServiceError(w, "set announce connection enabled", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// announceConnectionID parses the {id} path param.
func announceConnectionID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid connection id")
		return 0, false
	}
	return id, true
}

// toAnnounceResponse maps a connection to its API view, redacting the tool key.
func toAnnounceResponse(c domain.AnnounceConnection) announceConnectionResponse {
	return announceConnectionResponse{
		ID: c.ID, Name: c.Name, Kind: c.Kind, BaseURL: c.BaseURL, HarbrrURL: c.HarbrrURL,
		APIKey: secrets.Redacted, Enabled: c.Enabled, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}
