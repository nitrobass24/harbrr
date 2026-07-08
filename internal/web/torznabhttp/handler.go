package torznabhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/secrets"
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
	dlToken         *secrets.Keyring
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
// without holding a plaintext key in memory (docs/security.md). When set, it takes
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

// WithDLToken enables the grab-time /dl proxy: the served feed routes a
// resolver-needing indexer's download links through harbrr's /dl endpoint with an
// opaque token (sealed with the keyring), so the passkey-bearing link is resolved
// and fetched server-side and never appears in the feed. Without it, no /dl URLs are
// emitted (resolver-needing links would be served unresolved).
func WithDLToken(kr *secrets.Keyring) Option { return func(h *handler) { h.dlToken = kr } }

// Route is one Torznab HTTP route: its method and path template. The path uses the
// same {slug} brace syntax as the OpenAPI spec, so Routes is the single source
// of truth the OpenAPI drift test checks the spec against (the feed mux is not
// reachable via chi.Walk).
type Route struct {
	Method string
	Path   string
}

// torznabRoutes are the *arr-facing feed routes, matching the URL Sonarr/Radarr are
// configured with for a Jackett/Prowlarr Torznab indexer. dl selects the download
// proxy handler; the rest dispatch to serve (caps + search).
var torznabRoutes = []struct {
	Route
	dl bool
	// bypass marks the freeleech-bypass feed variant: the same caps/search handler,
	// but the request is tagged so the serve-time freeleech view returns the full
	// catalog (for qui/cross-seed). dl and bypass are mutually exclusive.
	bypass bool
}{
	{Route{http.MethodGet, "/api/indexers/{slug}/results/torznab"}, false, false},
	{Route{http.MethodGet, "/api/indexers/{slug}/results/torznab/api"}, false, false},
	{Route{http.MethodGet, "/api/indexers/{slug}/results/torznab/full"}, false, true},
	{Route{http.MethodGet, "/api/indexers/{slug}/results/torznab/full/api"}, false, true},
	{Route{http.MethodGet, "/api/indexers/{slug}/dl"}, true, false},
}

// Routes returns the method/path pairs the Torznab handler serves, so the OpenAPI
// drift test can assert each is documented without re-listing the patterns.
func Routes() []Route {
	out := make([]Route, len(torznabRoutes))
	for i, r := range torznabRoutes {
		out[i] = r.Route
	}
	return out
}

// NewHandler builds the *arr-facing Torznab HTTP handler over the routes in
// torznabRoutes (see Routes).
func NewHandler(provider Provider, opts ...Option) http.Handler {
	h := &handler{provider: provider, clock: time.Now, log: zerolog.Nop()}
	for _, o := range opts {
		o(h)
	}
	mux := http.NewServeMux()
	for _, r := range torznabRoutes {
		fn := h.serve
		switch {
		case r.dl:
			fn = h.serveDL
		case r.bypass:
			fn = withFreeleechBypass(h.serve)
		}
		mux.HandleFunc(r.Method+" "+r.Path, fn)
	}
	return mux
}

// serveDL is the grab-time download proxy. It authenticates the apikey (gating
// access), decodes the opaque token into the pre-resolution link (bound to this
// indexer), resolves and fetches the torrent server-side through harbrr's session,
// and streams it back — so a passkey-bearing link is never exposed in the feed. A
// resolved magnet (public, no secret) is served as a 302. Every failure is generic;
// the link/passkey never reaches a log, error body, or redirect.
func (h *handler) serveDL(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !h.authorized(q) {
		writeError(w, http.StatusOK, codeInvalidAPIKey, "Invalid API Key")
		return
	}
	idx, ok := h.provider.Indexer(r.Context(), r.PathValue("slug"))
	if !ok {
		writeError(w, http.StatusOK, codeBadParameter, "Indexer is not supported")
		return
	}
	if h.dlToken == nil {
		writeError(w, http.StatusOK, codeBadParameter, "download proxy is not enabled")
		return
	}
	link, err := decodeDLToken(h.dlToken, idx.Info().ID, q.Get("token"))
	if err != nil {
		// The error never carries the link; an invalid/forged token is a bad request.
		writeError(w, http.StatusBadRequest, codeBadParameter, "invalid download token")
		return
	}
	// The decoded link is trusted because the token is AEAD-authenticated under the
	// keyring (so only harbrr could mint it) and the endpoint is apikey-gated. In
	// plaintext mode (no key, opt-in behind a loud startup warning) the token is not
	// authenticated, so an apikey-holder could forge one for an arbitrary host — a
	// known, gated SSRF. We do not host-filter the link here: a self-hosted operator
	// may run a private/LAN tracker, and the attacker is already an apikey-holder on
	// single-user software, so a filter would break legitimate setups for little gain.
	result, err := idx.Grab(r.Context(), link)
	if err != nil {
		h.writeInternalError(w, "grab", idx.Info().ID, err)
		return
	}
	if result.Redirect != "" {
		// Only a magnet (public, no secret) is ever redirected. Guard so a resolved
		// http(s) link can never become an open redirect or leak a passkey in Location.
		if !strings.HasPrefix(result.Redirect, "magnet:") {
			h.writeInternalError(w, "grab", idx.Info().ID, errors.New("grab returned a non-magnet redirect"))
			return
		}
		http.Redirect(w, r, result.Redirect, http.StatusFound) //nolint:gosec // G710: validated magnet: URI above, not a web open-redirect
		return
	}
	ct := result.ContentType
	if ct == "" {
		ct = torrentContentType
	}
	// Serve boundary (Jackett's DownloadController analogue): a torrent body must be a
	// bencoded dictionary before it is served as a .torrent. When the session has
	// expired, the .torrent fetch 302s to the login page and the client follows it
	// (deliberate, matching Jackett), so the login-page HTML can come back with HTTP
	// 200 — refuse to hand that to *arr as a .torrent. Jackett runs BencodeParser.Parse
	// on the bytes and returns 404 on failure; we mirror that. This gates on the torrent
	// content type only: a magnet is the Redirect branch above, and a usenet .nzb (served
	// as application/x-nzb) is XML, not bencode — neither is bencode-checked.
	if ct == torrentContentType && !isBencodeTorrent(result.Body) {
		h.log.Warn().
			Str("stage", "grab").
			Str("indexer", idx.Info().ID).
			Int("bytes", len(result.Body)).
			Msg("grab produced a non-torrent body (likely an expired session); refusing to serve it as a .torrent")
		http.Error(w, "requested torrent is not available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Body) //nolint:gosec // G705: torrent file served as application/x-bittorrent, fixed non-HTML content type
}

// isBencodeTorrent reports whether body is a bencoded torrent: a top-level bencoded
// dictionary starts with 'd'. This is the serve-boundary equivalent of Jackett's
// BencodeParser.Parse — a cheap, robust sniff (all real .torrents begin with 'd', with
// no leading whitespace) that rejects an empty body and login-page HTML alike.
func isBencodeTorrent(body []byte) bool {
	return len(body) > 0 && body[0] == 'd'
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
	idx, ok := h.provider.Indexer(r.Context(), r.PathValue("slug"))
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

// dlRewriter builds the per-release acquisition rewriter for a resolver-needing
// indexer: it replaces the served <link>/<enclosure> with a /dl proxy URL carrying
// an opaque token for the original (passkey-bearing) link, and derives a stable,
// passkey-free guid so *arr's dedup stays consistent across polls even though the
// token rotates. It returns nil when the proxy is not enabled or the indexer needs
// no resolution (direct links/magnets are served as-is). A magnet release keeps its
// magnet (public, no secret), and a token-mint failure falls back to the direct
// link rather than dropping the release.
func (h *handler) dlRewriter(r *http.Request, idx Indexer) tzn.AcquisitionRewriter {
	if h.dlToken == nil || !NeedsDLProxy(idx) {
		return nil
	}
	// The exported NewDLRewriter is the single implementation, shared with the
	// management API's JSON search so both seal resolver links identically.
	return NewDLRewriter(h.dlToken, idx, h.dlBaseURL(r, idx.Info().ID), apiKeyParam(r.URL.Query()))
}

// dlBaseURL is the externally-visible /dl endpoint for an indexer (scheme/host from
// the request, the configured base path re-added), without query — the apikey and
// token are appended per release. It mirrors selfURL's scheme/host derivation.
func (h *handler) dlBaseURL(r *http.Request, indexerID string) string {
	return DLBaseURL(r, h.basePath, indexerID)
}

// dlURLWithToken appends the caller's apikey (so *arr can authenticate the grab) and
// the opaque token to the /dl base URL.
func dlURLWithToken(base, apiKey, token string) string {
	q := url.Values{}
	if apiKey != "" {
		q.Set("apikey", apiKey)
	}
	q.Set("token", token)
	return base + "?" + q.Encode()
}

// apiKeyParam returns the request's apikey (or its passkey alias) so the served /dl
// links reflect the caller's own key.
func apiKeyParam(q url.Values) string {
	if k := q.Get("apikey"); k != "" {
		return k
	}
	return q.Get("passkey")
}

// stableGUID derives a deterministic, passkey-free guid from the indexer id and the
// original link, so a proxied release keeps a stable identity across polls (the /dl
// token rotates per request and the original link may embed a passkey).
func stableGUID(indexerID, original string) string {
	sum := sha256.Sum256([]byte(indexerID + "\x00" + original))
	return "harbrr-" + hex.EncodeToString(sum[:])
}

// writeResults validates the search mode + id params, runs the search, then
// de-duplicates, paginates, and serializes the results feed. No-results yields a
// valid empty feed (HTTP 200), never an error. Resolver-needing indexers have their
// links routed through the /dl proxy at serialization (no per-release resolution
// happens here — the grab resolves server-side).
func (h *handler) writeResults(w http.ResponseWriter, r *http.Request, idx Indexer, q url.Values) {
	caps := idx.Capabilities()
	if !h.resolveMode(w, q, caps) {
		return
	}
	// A CacheInfo sink lets the cache decorator surface the served entry's validators
	// (ETag + expiry) for the conditional-GET response below. A `no-cache` request
	// header forces a live fetch — the header sibling of the `nocache=1` query param —
	// and, like it, suppresses the 304 short-circuit so the client gets a fresh body.
	ctx, ci := WithCacheInfoSink(r.Context())
	headerFresh := requestNoCache(r)
	if headerFresh {
		ctx = WithCacheBypass(ctx)
	}
	// searchReleases is the shared read pipeline (map -> search -> dedupe -> filter
	// -> page); the management API's JSON search runs the same code for parity.
	res, err := searchReleases(ctx, idx, caps, q)
	if err != nil {
		h.writeInternalError(w, "search", idx.Info().ID, err)
		return
	}
	// When the response came through the cache, emit validators and honor a matching
	// If-None-Match with 304 — unless the client forced a fresh fetch (header or query).
	// The served validator hashes the POST-filter page the freeleech view actually serves
	// (not the cache's pre-filter payload) and folds in both the freeleech-bypass variant
	// and this page's window (servedPayloadETag + pagedETag): the honor feed and the /full
	// bypass feed share one cached entry, and the payload ETag is page-independent, so
	// without these folds a revalidation of one feed/page could be answered 304 with
	// another variant's or page's body.
	if ci.ETag != "" {
		if view, ok := servedPayloadETag(res.Releases, freeleechBypass(ctx)); ok {
			served := &CacheInfo{ETag: pagedETag(view, res.Offset, res.Limit), ExpiresAt: ci.ExpiresAt}
			setCacheValidators(w, served, h.clock())
			if !headerFresh && !wantsNoCache(q) && ifNoneMatchMatches(r.Header.Get("If-None-Match"), served.ETag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}
	page := tzn.Page{Offset: res.Offset, Total: res.Total}
	body, err := tzn.MarshalResultsRewritten(h.feedInfo(r, idx), res.Releases, page, h.clock(), h.dlRewriter(r, idx))
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
		Protocol:    info.Protocol,
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
// through RedactError as defense in depth. (RedactURL must not be used here: it
// parses its argument as a single URL and re-encodes the query via url.Values,
// which mangles a multi-clause error message — sorting/merging the URL's params
// and percent-encoding the prose — into unreadable garbage.)
func (h *handler) writeInternalError(w http.ResponseWriter, stage, indexerID string, err error) {
	h.log.Error().
		Str("stage", stage).
		Str("indexer", indexerID).
		Str("error", apphttp.RedactError(err)).
		Msg("torznab request failed")
	writeError(w, http.StatusInternalServerError, codeUnknownError, "internal error processing the request")
}
