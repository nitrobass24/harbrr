package api

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/download"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
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

// grabRequest is POST /api/download-clients/{id}/grab's body: the search result the
// operator picked, named by its origin indexer plus the `link` that search response
// served VERBATIM. Name is the release title, used only to name the job on clients that
// take an uploaded file.
type grabRequest struct {
	Indexer string `json:"indexer"`
	Link    string `json:"link"`
	Name    string `json:"name"`
}

// grabToDownloadClient sends one search result to a configured download client
// (autobrr/harbrr#7). The link is classified, never blindly fetched through the
// indexer's session: a harbrr-sealed management download URL is resolved server-side
// (so the passkey stays inside harbrr and the client receives bytes, or a magnet), and
// any other link is handed to the client to fetch itself. 204 on success.
func (rt *router) grabToDownloadClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "download client")
	if !ok {
		return
	}
	var req grabRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Indexer, req.Link = strings.TrimSpace(req.Indexer), strings.TrimSpace(req.Link)
	if req.Indexer == "" || req.Link == "" {
		writeErrorCode(w, http.StatusBadRequest, "invalid", "indexer and link are required")
		return
	}
	idx, ok := rt.registry.Indexer(r.Context(), req.Indexer)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	payload, ok := rt.grabPayload(w, r, idx, req)
	if !ok {
		return
	}
	rt.sendToDownloadClient(w, r, id, payload)
}

// grabPayload turns the requested link into a resolved download.Payload, writing the
// error response and returning ok=false when it cannot. A sealed harbrr link is
// resolved here (bytes, or the magnet it redirects to); anything else is passed through
// as a URL for the client to fetch — deliberately NOT through idx.Grab, which would
// attach the indexer's session credentials to a caller-supplied URL.
func (rt *router) grabPayload(w http.ResponseWriter, r *http.Request, idx core.Indexer, req grabRequest) (download.Payload, bool) {
	info := idx.Info()
	p := download.Payload{Protocol: download.Protocol(info.Protocol), Name: cmp.Or(req.Name, info.ID)}
	token, sealed := sealedDownloadToken(req.Link, req.Indexer)
	if !sealed {
		p.URL = req.Link
		return p, true
	}
	grabbed, err := torznabhttp.ResolveGrab(r.Context(), idx, rt.dlToken, token)
	if err != nil {
		rt.writeResolveGrabError(w, info.ID, err)
		return download.Payload{}, false
	}
	if grabbed.Magnet != "" {
		p.URL = grabbed.Magnet
		return p, true
	}
	p.Bytes = grabbed.Body
	return p, true
}

// sealedDownloadToken reports whether link is one of harbrr's OWN sealed management
// download URLs for slug (…/api/indexers/{slug}/download/{token}) and returns its token.
// It matches on the path SUFFIX, not the origin: the served link is built from the
// configured external origin, which need not equal the host this request arrived on.
func sealedDownloadToken(link, slug string) (string, bool) {
	u, err := url.Parse(link)
	if err != nil {
		return "", false
	}
	dir, token := path.Split(u.EscapedPath())
	if token == "" || !strings.HasSuffix(dir, "/api/indexers/"+url.PathEscape(slug)+"/download/") {
		return "", false
	}
	return token, true
}

// writeResolveGrabError maps a resolve failure to this API's JSON envelope, mirroring
// the statuses the feed's /dl proxy answers with. The link never reaches the response
// or the log (writeServiceError redacts).
func (rt *router) writeResolveGrabError(w http.ResponseWriter, indexerID string, err error) {
	switch {
	case errors.Is(err, torznabhttp.ErrInvalidToken):
		writeError(w, http.StatusBadRequest, "invalid download token")
	case errors.Is(err, torznabhttp.ErrProxyDisabled):
		writeError(w, http.StatusServiceUnavailable, "download proxy is not enabled")
	case errors.Is(err, torznabhttp.ErrNotTorrent):
		rt.log.Warn().Str("stage", "grab").Str("indexer", indexerID).
			Msg("api: grab produced a non-torrent body (likely an expired session); refusing to send it to a download client")
		writeError(w, http.StatusNotFound, "requested torrent is not available")
	default:
		rt.writeServiceError(w, "grab release", err)
	}
}

// sendToDownloadClient hands the resolved payload to the client. A protocol mismatch or
// a bytes-only payload a URL-only client cannot take is the operator's choice to fix
// (they picked an nzb client for a torrent), so its text is echoed as a 400 — those
// sentinels carry no link.
func (rt *router) sendToDownloadClient(w http.ResponseWriter, r *http.Request, id int64, p download.Payload) {
	err := rt.download.Grab(r.Context(), id, p)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, download.ErrUnsupportedProtocol), errors.Is(err, download.ErrURLRequired):
		writeErrorCode(w, http.StatusBadRequest, "invalid", err.Error())
	default:
		rt.writeServiceError(w, "grab to download client", err)
	}
}

// toDownloadClientResponse maps a client to its API view, redacting the secret.
func toDownloadClientResponse(c domain.DownloadClient) downloadClientResponse {
	return downloadClientResponse{
		ID: c.ID, Name: c.Name, Kind: c.Kind, AppID: c.AppID, Enabled: c.Enabled, Host: c.Host, Username: c.Username,
		Secret: secrets.Redacted, Settings: c.Settings, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}
