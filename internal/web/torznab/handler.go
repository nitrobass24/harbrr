package torznab

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	tzn "github.com/autobrr/harbrr/internal/torznab"
)

// handler serves the Torznab endpoint for a set of indexers resolved via a
// Provider.
type handler struct {
	provider        Provider
	apiKey          string
	apiKeyValidator func(string) bool
	basePath        string
	clock           func() time.Time
	log             zerolog.Logger
}

// Option configures the handler at construction.
type Option func(*handler)

// WithAPIKey sets the API key requests must present (apikey or passkey query
// param). When empty, the handler fails closed: every request is rejected with
// error 100, never silently unauthenticated.
func WithAPIKey(key string) Option { return func(h *handler) { h.apiKey = key } }

// WithAPIKeyValidator sets a validator for the apikey/passkey query param,
// replacing the fixed-key comparison. The production server wires this to the auth
// service so any minted API key (stored only as a hash) authorizes the feed,
// without holding a plaintext key in memory (docs/ideas.md §9). When set, it takes
// precedence over WithAPIKey.
func WithAPIKeyValidator(fn func(string) bool) Option {
	return func(h *handler) { h.apiKeyValidator = fn }
}

// WithBasePath sets the external base path (e.g. "/harbrr") so the served feed's
// self URL reflects the externally-visible URL after the server strips the prefix.
func WithBasePath(prefix string) Option { return func(h *handler) { h.basePath = prefix } }

// WithClock injects the reference clock used for the results pubDate fallback.
// Defaults to time.Now.
func WithClock(fn func() time.Time) Option {
	return func(h *handler) {
		if fn != nil {
			h.clock = fn
		}
	}
}

// WithLogger sets the logger for the internal-error path (errors are logged with
// secrets redacted; the served body is always generic).
func WithLogger(l zerolog.Logger) Option { return func(h *handler) { h.log = l } }

// NewHandler builds the *arr-facing Torznab HTTP handler. It serves:
//
//	GET /api/v2.0/indexers/{indexerId}/results/torznab
//	GET /api/v2.0/indexers/{indexerId}/results/torznab/api
//
// matching the URL Sonarr/Radarr are configured with for a Jackett/Prowlarr
// Torznab indexer.
func NewHandler(provider Provider, opts ...Option) http.Handler {
	h := &handler{provider: provider, clock: time.Now, log: zerolog.Nop()}
	for _, o := range opts {
		o(h)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2.0/indexers/{indexerId}/results/torznab", h.serve)
	mux.HandleFunc("GET /api/v2.0/indexers/{indexerId}/results/torznab/api", h.serve)
	return mux
}

// serve is the request entry point: authenticate, resolve the indexer, then
// dispatch on t=. Credential and indexer-resolution failures return HTTP 200
// with an <error> body (Jackett's torznab behavior) so *arr surfaces the error
// code rather than treating it as a transport failure.
func (h *handler) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !h.authorized(q) {
		writeError(w, http.StatusOK, codeInvalidAPIKey, "Invalid API Key")
		return
	}
	idx, ok := h.provider.Indexer(r.PathValue("indexerId"))
	if !ok {
		writeError(w, http.StatusOK, codeBadParameter, "Indexer is not supported")
		return
	}
	if t := q.Get("t"); strings.EqualFold(t, tzn.ReqCaps) {
		h.writeCaps(w, idx)
		return
	}
	h.writeResults(w, r, idx, q)
}

// authorized validates the apikey (or its passkey alias). A validator (the
// production hash-lookup) takes precedence; otherwise a fixed key is compared. It
// fails closed when neither a validator nor a key is configured.
func (h *handler) authorized(q url.Values) bool {
	key := q.Get("apikey")
	if key == "" {
		key = q.Get("passkey")
	}
	if h.apiKeyValidator != nil {
		return key != "" && h.apiKeyValidator(key)
	}
	if h.apiKey == "" {
		return false
	}
	return key == h.apiKey
}

// writeCaps serializes and writes the capabilities document (t=caps).
func (h *handler) writeCaps(w http.ResponseWriter, idx Indexer) {
	body, err := tzn.MarshalCaps(idx.Capabilities())
	if err != nil {
		h.writeInternalError(w, "caps", idx.Info().ID, err)
		return
	}
	writeXML(w, http.StatusOK, body)
}

// resolveDownloadLinks rewrites each served release's download Link to the real
// torrent URL via the engine's ResolveDownload, but ONLY when the definition
// declares a download block (NeedsResolver). For the direct-link trackers Phase 5
// targets this is skipped entirely, so the served link (the tracker's direct
// torrent URL) reaches the *arr unchanged. A per-release resolution failure keeps
// the original link rather than dropping the release or failing the feed
// (best-effort). The full resolver and a grab-time /dl proxy that resolves
// through harbrr's session are Phase 7; running here resolves only the served
// page (post-paging), bounding the work.
func resolveDownloadLinks(idx Indexer, releases []*normalizer.Release) {
	if !idx.NeedsResolver() {
		return
	}
	for _, rel := range releases {
		if rel == nil || rel.Link == "" {
			continue
		}
		if resolved, err := idx.ResolveDownload(rel.Link); err == nil && resolved != "" {
			rel.Link = resolved
		}
	}
}

// writeResults validates the search mode + id params, runs the search, then
// de-duplicates, paginates, and serializes the results feed. No-results yields a
// valid empty feed (HTTP 200), never an error.
func (h *handler) writeResults(w http.ResponseWriter, r *http.Request, idx Indexer, q url.Values) {
	caps := idx.Capabilities()
	if !h.resolveMode(w, q, caps) {
		return
	}
	query, requestedCats := buildQuery(q, caps)
	releases, err := idx.Search(query)
	if err != nil {
		h.writeInternalError(w, "search", idx.Info().ID, err)
		return
	}
	// Jackett pipeline order: FixResults (dedupe) -> FilterResults (category drop
	// + limit). Category filtering runs after dedupe and before paging.
	releases = filterResults(dedupeByGUID(releases), requestedCats, caps)
	releases = parsePaging(q).apply(releases)
	resolveDownloadLinks(idx, releases)
	body, err := tzn.MarshalResults(h.feedInfo(r, idx), releases, h.clock())
	if err != nil {
		h.writeInternalError(w, "results", idx.Info().ID, err)
		return
	}
	writeXML(w, http.StatusOK, body)
}

// resolveMode validates the t= search mode against the indexer's capabilities,
// writing the appropriate error and returning false on failure. A missing t
// defaults to the general search mode (Jackett's TorznabRequest default).
func (h *handler) resolveMode(w http.ResponseWriter, q url.Values, caps *mapper.Capabilities) bool {
	capsKey := mapper.ModeSearch
	if t := q.Get("t"); t != "" {
		var known bool
		if capsKey, known = tzn.ModeForRequest(t); !known {
			writeError(w, http.StatusBadRequest, codeNoSuchFunction, "No such function")
			return false
		}
	}
	if !tzn.ModeAvailable(caps, capsKey) {
		writeError(w, http.StatusBadRequest, codeNotAvailable, "Function Not Available: this indexer does not support that search mode")
		return false
	}
	if param, ok := unsupportedIDParam(caps, capsKey, q); !ok {
		writeError(w, http.StatusBadRequest, codeNotAvailable, "Function Not Available: "+param+" is not supported for this search mode")
		return false
	}
	return true
}

// gatedIDParams are the id search params Jackett rejects (error 203) when the
// mode does not advertise them: imdbid and tmdbid, and ONLY for the movie and tv
// search modes. tvdbid is deliberately NOT here — Jackett gates it only on
// tv-search availability (already verified by resolveMode), never on the param
// list, so an advertised TV search accepts tvdbid and degrades to a keyword
// search (the common Sonarr query). For general/music/book search Jackett gates
// no id params, so an id param there passes through (keyword-degraded) too.
var gatedIDParams = []string{"imdbid", "tmdbid"}

// unsupportedIDParam returns the first supplied id param the mode does not
// advertise (ok=false), reproducing Jackett's ResultsController imdbid/tmdbid
// gates which fire only for movie-search and tv-search. Other modes never gate
// an id param.
func unsupportedIDParam(caps *mapper.Capabilities, capsKey string, q url.Values) (string, bool) {
	if capsKey != mapper.ModeMovieSearch && capsKey != mapper.ModeTVSearch {
		return "", true
	}
	for _, p := range gatedIDParams {
		if q.Get(p) != "" && !tzn.SupportsParam(caps, capsKey, p) {
			return p, false
		}
	}
	return "", true
}

// dedupeByGUID drops releases sharing a guid (Jackett's post-FixResults GroupBy),
// keeping the first occurrence and preserving order, so *arr never sees duplicate
// items. nil entries are skipped defensively.
func dedupeByGUID(releases []*normalizer.Release) []*normalizer.Release {
	seen := make(map[string]struct{}, len(releases))
	out := make([]*normalizer.Release, 0, len(releases))
	for _, rel := range releases {
		if rel == nil {
			continue
		}
		guid := tzn.GUIDFor(rel)
		if _, dup := seen[guid]; dup {
			continue
		}
		seen[guid] = struct{}{}
		out = append(out, rel)
	}
	return out
}

// feedInfo assembles the feed metadata from the indexer identity + the request's
// self URL.
func (h *handler) feedInfo(r *http.Request, idx Indexer) tzn.FeedInfo {
	info := idx.Info()
	return tzn.FeedInfo{
		IndexerID:   info.ID,
		Name:        info.Name,
		Description: info.Description,
		SiteLink:    info.SiteLink,
		Type:        info.Type,
		SelfURL:     h.selfURL(r),
	}
}

// selfURL builds the atom:link self href from the request scheme/host/path,
// dropping the query string entirely so harbrr never reflects the caller's apikey,
// then routes it through RedactURL as defense in depth. It re-adds the configured
// base path (the server strips it before routing) so the served URL is the
// externally-visible one, and honors X-Forwarded-Proto so a TLS-terminating proxy
// yields https.
func (h *handler) selfURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return apphttp.RedactURL(scheme + "://" + r.Host + h.basePath + r.URL.Path)
}

// writeInternalError logs the failure and returns a generic 900 document — the
// raw error is never echoed to the client (the served body is a fixed string).
// The engine redacts resolved URLs at the HTTP stage (search/request.go), so its
// error text carries no resolved passkeys; the logged string is additionally run
// through RedactURL as defense in depth.
func (h *handler) writeInternalError(w http.ResponseWriter, stage, indexerID string, err error) {
	h.log.Error().
		Str("stage", stage).
		Str("indexer", indexerID).
		Str("error", apphttp.RedactURL(err.Error())).
		Msg("torznab request failed")
	writeError(w, http.StatusInternalServerError, codeUnknownError, "internal error processing the request")
}
