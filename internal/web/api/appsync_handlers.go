package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/secrets"
)

// appConnectionResponse is the API view of an app-sync connection. The app's API key
// is never echoed — it reads back as the <redacted> sentinel.
type appConnectionResponse struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Kind           string     `json:"kind"`
	AppID          *int64     `json:"appId,omitempty"`
	BaseURL        string     `json:"baseUrl"`
	HarbrrURL      string     `json:"harbrrUrl"`
	APIKey         string     `json:"apiKey"`
	Enabled        bool       `json:"enabled"`
	SyncLevel      string     `json:"syncLevel"`
	IndexScope     string     `json:"indexScope"`
	FreeleechMode  string     `json:"freeleechMode"`
	Priority       int        `json:"priority"`
	SyncProfileID  *int64     `json:"syncProfileId,omitempty"`
	LastSyncAt     *time.Time `json:"lastSyncAt,omitempty"`
	LastSyncStatus string     `json:"lastSyncStatus,omitempty"`
	LastSyncError  string     `json:"lastSyncError,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// connectionIndexerResponse is one row of a connection's per-indexer sync ledger.
type connectionIndexerResponse struct {
	InstanceID     int64      `json:"instanceId"`
	RemoteID       string     `json:"remoteId,omitempty"`
	Selected       bool       `json:"selected"`
	LastPushedAt   *time.Time `json:"lastPushedAt,omitempty"`
	LastPushStatus string     `json:"lastPushStatus,omitempty"`
	LastPushError  string     `json:"lastPushError,omitempty"`
}

// connectionStatusResponse is a connection plus its per-indexer ledger.
type connectionStatusResponse struct {
	appConnectionResponse
	Indexers []connectionIndexerResponse `json:"indexers"`
}

// syncResultResponse / syncResponse are the result of a sync run.
type syncResultResponse struct {
	Slug   string `json:"slug"`
	Action string `json:"action"`
	Error  string `json:"error,omitempty"`
}

type syncResponse struct {
	Status  string               `json:"status"`
	Results []syncResultResponse `json:"results"`
}

// syncAllResultResponse is one connection's outcome in a bulk-sync run. report reuses
// the single-sync shape; error is set (and report empty) when that connection failed.
type syncAllResultResponse struct {
	ConnectionID int64        `json:"connectionId"`
	Name         string       `json:"name"`
	Report       syncResponse `json:"report"`
	Error        string       `json:"error,omitempty"`
}

// listConnections returns all app-sync connections.
func (rt *router) listConnections(w http.ResponseWriter, r *http.Request) {
	list, err := rt.appsync.ListConnections(r.Context())
	if err != nil {
		rt.writeServiceError(w, "list connections", err)
		return
	}
	out := make([]appConnectionResponse, 0, len(list))
	for _, c := range list {
		out = append(out, toConnectionResponse(c))
	}
	writeJSON(w, http.StatusOK, out)
}

// createConnection adds an app-sync connection and mints its dedicated harbrr key.
func (rt *router) createConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string `json:"name"`
		Kind          string `json:"kind"`
		AppID         *int64 `json:"appId"`
		BaseURL       string `json:"baseUrl"`
		APIKey        string `json:"apiKey"`
		Username      string `json:"username"`
		HarbrrURL     string `json:"harbrrUrl"`
		SyncLevel     string `json:"syncLevel"`
		IndexScope    string `json:"indexScope"`
		FreeleechMode string `json:"freeleechMode"`
		Priority      int    `json:"priority"`
		SyncProfileID *int64 `json:"syncProfileId"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	conn, err := rt.appsync.CreateConnection(r.Context(), appsync.CreateConnectionParams{
		Name: req.Name, Kind: req.Kind, AppID: req.AppID, BaseURL: req.BaseURL, APIKey: req.APIKey,
		Username: req.Username, HarbrrURL: req.HarbrrURL, SyncLevel: req.SyncLevel, IndexScope: req.IndexScope,
		FreeleechMode: req.FreeleechMode, Priority: req.Priority, SyncProfileID: req.SyncProfileID,
	})
	if err != nil {
		rt.writeServiceError(w, "create connection", err)
		return
	}
	writeJSON(w, http.StatusCreated, toConnectionResponse(conn))
}

// getConnection returns one connection (app key redacted).
func (rt *router) getConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := rt.connectionID(w, r)
	if !ok {
		return
	}
	conn, err := rt.appsync.GetConnection(r.Context(), id)
	if err != nil {
		rt.writeServiceError(w, "get connection", err)
		return
	}
	writeJSON(w, http.StatusOK, toConnectionResponse(conn))
}

// updateConnection patches a connection (a new apiKey rotates the app credential).
func (rt *router) updateConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := rt.connectionID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name          *string     `json:"name"`
		SyncLevel     *string     `json:"syncLevel"`
		IndexScope    *string     `json:"indexScope"`
		FreeleechMode *string     `json:"freeleechMode"`
		Priority      *int        `json:"priority"`
		SyncProfileID optionalRef `json:"syncProfileId"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.appsync.UpdateConnection(r.Context(), id, appsync.UpdateConnectionParams{
		Name:      req.Name,
		SyncLevel: req.SyncLevel, IndexScope: req.IndexScope, FreeleechMode: req.FreeleechMode,
		Priority: req.Priority, SyncProfileID: req.SyncProfileID.toAppSync(),
	}); err != nil {
		rt.writeServiceError(w, "update connection", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteConnection removes a connection and revokes its minted key.
func (rt *router) deleteConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := rt.connectionID(w, r)
	if !ok {
		return
	}
	if err := rt.appsync.DeleteConnection(r.Context(), id); err != nil {
		rt.writeServiceError(w, "delete connection", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// enableConnection / disableConnection toggle a connection.
func (rt *router) enableConnection(w http.ResponseWriter, r *http.Request) {
	rt.setConnectionEnabled(w, r, true)
}

func (rt *router) disableConnection(w http.ResponseWriter, r *http.Request) {
	rt.setConnectionEnabled(w, r, false)
}

func (rt *router) setConnectionEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	id, ok := rt.connectionID(w, r)
	if !ok {
		return
	}
	if err := rt.appsync.SetEnabled(r.Context(), id, enabled); err != nil {
		rt.writeServiceError(w, "set connection enabled", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testConnection probes the app's reachability and credentials. A pass is
// {"ok":true}; a failure is 200 {"ok":false,"error":<scrubbed>}; an unknown id 404.
func (rt *router) testConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := rt.connectionID(w, r)
	if !ok {
		return
	}
	switch err := rt.appsync.TestConnection(r.Context(), id); {
	case err == nil:
		writeJSON(w, http.StatusOK, testResult{OK: true})
	case errors.Is(err, database.ErrNotFound):
		rt.writeServiceError(w, "test connection", err)
	default:
		writeJSON(w, http.StatusOK, testResult{OK: false, Error: apphttp.RedactError(err)})
	}
}

// syncConnection runs reconciliation now and returns the per-indexer outcomes.
func (rt *router) syncConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := rt.connectionID(w, r)
	if !ok {
		return
	}
	report, err := rt.appsync.Sync(r.Context(), id)
	if err != nil {
		rt.writeServiceError(w, "sync connection", err)
		return
	}
	writeJSON(w, http.StatusOK, toSyncResponse(report))
}

// syncAllConnections reconciles every connection in one call and returns a
// per-connection result array (one entry per connection, in list order).
func (rt *router) syncAllConnections(w http.ResponseWriter, r *http.Request) {
	results, err := rt.appsync.SyncAll(r.Context())
	if err != nil {
		rt.writeServiceError(w, "sync connections", err)
		return
	}
	out := make([]syncAllResultResponse, 0, len(results))
	for _, res := range results {
		out = append(out, syncAllResultResponse{
			ConnectionID: res.ConnectionID, Name: res.Name,
			Report: toSyncResponse(res.Report), Error: res.Error,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// setConnectionIndexers replaces a connection's selected-indexer set (used by
// index_scope=selected). The body is the instance ids to select; all others are
// cleared.
func (rt *router) setConnectionIndexers(w http.ResponseWriter, r *http.Request) {
	id, ok := rt.connectionID(w, r)
	if !ok {
		return
	}
	var req struct {
		InstanceIDs []int64 `json:"instanceIds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.appsync.SetSelectedIndexers(r.Context(), id, req.InstanceIDs); err != nil {
		rt.writeServiceError(w, "set connection indexers", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// connectionStatus returns a connection plus its per-indexer ledger.
func (rt *router) connectionStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := rt.connectionID(w, r)
	if !ok {
		return
	}
	conn, err := rt.appsync.GetConnection(r.Context(), id)
	if err != nil {
		rt.writeServiceError(w, "connection status", err)
		return
	}
	ledger, err := rt.appsync.ConnectionIndexers(r.Context(), id)
	if err != nil {
		rt.writeServiceError(w, "connection status", err)
		return
	}
	writeJSON(w, http.StatusOK, connectionStatusResponse{
		appConnectionResponse: toConnectionResponse(conn),
		Indexers:              toLedgerResponse(ledger),
	})
}

// connectionID parses the {id} path param, writing a 400 and returning false on a
// malformed value.
func (rt *router) connectionID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid connection id")
		return 0, false
	}
	return id, true
}

// toConnectionResponse maps a connection to its API view, redacting the app key.
func toConnectionResponse(c domain.AppConnection) appConnectionResponse {
	return appConnectionResponse{
		ID: c.ID, Name: c.Name, Kind: c.Kind, AppID: c.AppID, BaseURL: c.BaseURL, HarbrrURL: c.HarbrrURL,
		APIKey: secrets.Redacted, Enabled: c.Enabled, SyncLevel: c.SyncLevel,
		IndexScope: c.IndexScope, FreeleechMode: c.FreeleechMode, Priority: c.Priority,
		SyncProfileID: c.SyncProfileID, LastSyncAt: c.LastSyncAt,
		LastSyncStatus: c.LastSyncStatus, LastSyncError: c.LastSyncError,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

func toLedgerResponse(ledger []domain.AppConnectionIndexer) []connectionIndexerResponse {
	out := make([]connectionIndexerResponse, 0, len(ledger))
	for _, l := range ledger {
		out = append(out, connectionIndexerResponse{
			InstanceID: l.InstanceID, RemoteID: l.RemoteID, Selected: l.Selected,
			LastPushedAt: l.LastPushedAt, LastPushStatus: l.LastPushStatus, LastPushError: l.LastPushError,
		})
	}
	return out
}

func toSyncResponse(report appsync.SyncReport) syncResponse {
	results := make([]syncResultResponse, 0, len(report.Results))
	for _, res := range report.Results {
		results = append(results, syncResultResponse{Slug: res.Slug, Action: res.Action, Error: res.Error})
	}
	return syncResponse{Status: report.Status, Results: results}
}
