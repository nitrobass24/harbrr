package torznabhttp

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/indexer/grab"
	"github.com/autobrr/harbrr/internal/secrets"
	tzn "github.com/autobrr/harbrr/internal/torznab"
)

// handler serves the Torznab endpoint for a set of indexers resolved via a
// core.Provider.
type handler struct {
	provider        core.Provider
	apiKey          string
	apiKeyValidator func(string) bool
	urlCfg          grab.URLConfig
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
func WithBasePath(prefix string) Option { return func(h *handler) { h.urlCfg.BasePath = prefix } }

// WithExternalURL sets the operator-configured external origin (scheme://host, no
// path — WithBasePath supplies that). When set, it is authoritative for every
// absolute URL the handler serves (self URL, /dl base) instead of the
// request-derived scheme+Host; "" restores the request-derived fallback.
func WithExternalURL(origin string) Option {
	return func(h *handler) { h.urlCfg.ExternalOrigin = origin }
}

// WithTrustedProxies gates X-Forwarded-Proto trust, in the request-derived fallback,
// on the direct peer being a configured trusted proxy — the feed-serving sibling of
// internal/web/api's trusted_proxies-gated X-Forwarded-For. Unset (nil), the header
// is never honored; wire it to auth.trusted_proxies to trust a fronting proxy.
func WithTrustedProxies(trusted apphttp.TrustedProxies) Option {
	return func(h *handler) { h.urlCfg.TrustedProxies = trusted }
}

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
func NewHandler(provider core.Provider, opts ...Option) http.Handler {
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
	// The /dl route returns real HTTP status codes, matching Jackett's
	// DownloadController — NOT the caps/search 200-envelope convention of
	// ResultsController (errors.go). *arr fetches enclosures verbatim, so a
	// gate failure must surface as a transport error (4xx/5xx) rather than a
	// 200 body that later fails downstream as an "invalid torrent file". The
	// <error> body stays informational (and secret-free); the status is what
	// *arr checks.
	if !h.authorized(q) {
		// Jackett DownloadController: `if (_serverConfig.APIKey != jackett_apikey) return Unauthorized();`
		writeError(w, http.StatusUnauthorized, codeInvalidAPIKey, "Invalid API Key")
		return
	}
	idx, ok := h.provider.Indexer(r.Context(), r.PathValue("slug"))
	if !ok {
		// Jackett's GetWebIndexer throws on an unknown id; DownloadController's catch
		// returns NotFound() — 404. Jackett also has a 403 Forbid path for a KNOWN but
		// unconfigured indexer; harbrr's provider returns !ok for both an unknown id AND
		// a disabled instance (registry errDisabled), collapsing both into 404. A benign
		// divergence: both are 4xx failed grabs to *arr, neither leaks, and the disabled
		// case is only reachable here with an already-minted token.
		//
		// This is also what keeps an AGGREGATE slug ("all", "profile:<name>") from ever
		// serving a download: it names a member set, not an instance, so Indexer never
		// resolves it and /dl on it 404s. An aggregate feed's enclosures point at the
		// ORIGIN member's /dl (see aggregateItems), which is the whole binding.
		writeError(w, http.StatusNotFound, codeBadParameter, "Indexer is not supported")
		return
	}
	grab.ServeGrab(w, r, idx, h.dlToken, h.log, q.Get("token"), torznabGrabError)
}

// torznabGrabError is serveDL's grab.ErrorWriter. Torznab codes derive from the status:
// 500 is the internal-error document (900), the serve-boundary 404 mirrors Jackett's
// plain-text NotFound (not an <error> doc), everything else is a bad parameter (201).
func torznabGrabError(w http.ResponseWriter, status int, msg string) {
	switch status {
	case http.StatusInternalServerError:
		writeError(w, status, codeUnknownError, msg)
	case http.StatusNotFound:
		http.Error(w, msg, status)
	default:
		writeError(w, status, codeBadParameter, msg)
	}
}

// serve is the request entry point: authenticate, resolve the slug to its member set,
// then dispatch on t=. Credential and indexer-resolution failures return HTTP 200
// with an <error> body (Jackett's torznab behavior) so *arr surfaces the error
// code rather than treating it as a transport failure.
//
// A slug naming a real indexer resolves to exactly one member and takes the untouched
// per-indexer path — that feed is byte-identical, by construction, to what it served
// before aggregation existed. Everything else is the aggregate fan-out (#400), including
// an aggregate slug that currently covers a single enabled indexer: it still renders the
// aggregate envelope and ledger, so the feed's shape does not change under the consumer
// when a second indexer is enabled. An unknown profile is not-found exactly as an
// unknown indexer slug is.
func (h *handler) serve(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !h.authorized(q) {
		writeError(w, http.StatusOK, codeInvalidAPIKey, "Invalid API Key")
		return
	}
	slug := r.PathValue("slug")
	members, err := h.provider.Resolve(r.Context(), slug)
	if err != nil {
		h.writeResolveError(w, slug, err)
		return
	}
	if !isAggregateSlug(slug) {
		// Resolve's contract: a non-aggregate slug that resolves is exactly one LIVE
		// indexer (a single slug that cannot be built does not resolve at all).
		h.serveIndexer(w, r, members[0].Indexer, q)
		return
	}
	h.serveAggregate(w, r, slug, members, q)
}

// writeResolveError answers a slug that produced no member set, distinguishing the two
// things that can mean (autobrr/harbrr#400, review decision):
//
//   - The slug names nothing (core.ErrNoSuchFeed) — a client error: the same 201
//     "Indexer is not supported" document an unknown per-indexer slug has always
//     rendered (Jackett parity), unlogged.
//   - The member set could not be READ (the instance/profile store failed) — harbrr's
//     problem, not the consumer's config: error 900, matching the 500→900 mapping the
//     /dl proxy already uses for internal failures. Telling an *arr "your config is
//     wrong" over a transient store failure sends its operator to the wrong ladder.
//
// Either way it is a loud error document, never the empty-200 feed a nil member set
// would otherwise serve — a whole-list failure must be distinguishable from "you have
// no indexers". Both the per-indexer and aggregate slug forms route through here, so
// the two feed shapes cannot disagree.
func (h *handler) writeResolveError(w http.ResponseWriter, slug string, err error) {
	if errors.Is(err, core.ErrNoSuchFeed) {
		writeError(w, http.StatusOK, codeBadParameter, "Indexer is not supported")
		return
	}
	grab.LogInternalError(h.log, "resolve", slug, err)
	writeError(w, http.StatusOK, codeUnknownError, "Internal server error")
}

// isAggregateSlug reports whether a feed slug names a member SET rather than one
// indexer: core.AggregateSlug, or a core.ProfileSlugPrefix / core.StatusSlugPrefix
// form. All three share the aggregate envelope, ledger and fan-out.
func isAggregateSlug(slug string) bool {
	return slug == core.AggregateSlug ||
		strings.HasPrefix(slug, core.ProfileSlugPrefix) ||
		strings.HasPrefix(slug, core.StatusSlugPrefix)
}

// serveIndexer is the per-indexer feed: caps or results for one resolved indexer.
func (h *handler) serveIndexer(w http.ResponseWriter, r *http.Request, idx core.Indexer, q url.Values) {
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

// writeCaps serializes and writes an indexer's capabilities document (t=caps).
func (h *handler) writeCaps(w http.ResponseWriter, idx core.Indexer) {
	h.writeCapsDoc(w, idx.Info().ID, idx.Capabilities())
}

// writeCapsDoc serializes a capabilities document under an explicit feed id, so the
// aggregate feed can serve the union of its members' caps and still log a failure
// against the feed the caller asked for.
func (h *handler) writeCapsDoc(w http.ResponseWriter, id string, caps *mapper.Capabilities) {
	body, err := tzn.MarshalCaps(caps)
	if err != nil {
		h.writeInternalError(w, "caps", id, err)
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
func (h *handler) dlRewriter(r *http.Request, idx core.Indexer) tzn.AcquisitionRewriter {
	if h.dlToken == nil || !grab.NeedsDLProxy(idx) {
		return nil
	}
	// grab.NewDLRewriter is the single implementation, shared with the
	// management API's JSON search so both seal resolver links identically.
	return grab.NewDLRewriter(h.dlToken, idx, h.dlBaseURL(r, idx.Info().ID), apiKeyParam(r.URL.Query()))
}

// dlBaseURL is the externally-visible /dl endpoint for an indexer (scheme/host from
// the request, the configured base path re-added), without query — the apikey and
// token are appended per release. It mirrors selfURL's scheme/host derivation.
func (h *handler) dlBaseURL(r *http.Request, indexerID string) string {
	return grab.DLBaseURL(r, h.urlCfg, indexerID)
}

// apiKeyParam returns the request's apikey (or its passkey alias) so the served /dl
// links reflect the caller's own key.
func apiKeyParam(q url.Values) string {
	if k := q.Get("apikey"); k != "" {
		return k
	}
	return q.Get("passkey")
}

// writeResults validates the search mode + id params, runs the search, then
// de-duplicates, paginates, and serializes the results feed. No-results yields a
// valid empty feed (HTTP 200), never an error. Resolver-needing indexers have their
// links routed through the /dl proxy at serialization (no per-release resolution
// happens here — the grab resolves server-side).
func (h *handler) writeResults(w http.ResponseWriter, r *http.Request, idx core.Indexer, q url.Values) {
	caps := idx.Capabilities()
	if _, ok := h.resolveMode(w, q, caps); !ok {
		return
	}
	// A CacheInfo sink lets the cache decorator surface whether this response came
	// from — or was freshly stored into — the cache, plus the entry's expiry, for the
	// conditional-GET response below. A `no-cache` request header forces a live fetch
	// — the header sibling of the `nocache=1` query param — and, like it, suppresses
	// the 304 short-circuit so the client gets a fresh body.
	ctx, ci := core.WithCacheInfoSink(r.Context())
	headerFresh := requestNoCache(r)
	if headerFresh {
		ctx = core.WithCacheBypass(ctx)
	}
	// SearchReleasesWithCaps is the shared read pipeline (map -> search -> dedupe ->
	// filter -> page); the management API's JSON search runs the same code for parity.
	res, err := core.SearchReleasesWithCaps(ctx, idx, caps, q)
	// A degenerate-query skip is not a failure: this indexer's own keywords filters
	// leave nothing of the question to search on (autobrr/harbrr#394), so it was never
	// asked. The honest Torznab answer on a per-indexer feed is the STANDARD empty
	// result document — identical bytes to a search that ran and matched nothing —
	// which is exactly what the empty, correctly-paged result the pipeline hands back
	// with the sentinel serializes to below. Only the aggregate feed distinguishes the
	// two, in its per-member ledger.
	if errors.Is(err, core.ErrDegenerateQuery) {
		err = nil
	}
	if err != nil {
		h.writeInternalError(w, "search", idx.Info().ID, err)
		return
	}
	// revalidate owns the full conditional-GET 304 protocol, including the "never answer
	// a 304 with the wrong feed-variant or page body" guard: the served validator hashes
	// the POST-filter page the freeleech view actually serves (not the cache's pre-filter
	// payload) and folds in both the freeleech-bypass variant and this page's window, so
	// the honor feed and the /full bypass feed sharing one cached entry can never
	// cross-match. fresh (header or query) forces a live body even on a match.
	sp := servedPage{releases: res.Releases, offset: res.Offset, limit: res.Limit, total: res.Total}
	fresh := headerFresh || core.WantsNoCache(q)
	if h.revalidate(w, r.Header, *ci, sp, core.FreeleechBypass(ctx), fresh) {
		return
	}
	page := tzn.Page{Offset: res.Offset, Total: res.Total}
	body, err := tzn.MarshalResultsRewritten(h.feedInfo(r, idx), res.Releases, page, h.clock(), h.dlRewriter(r, idx))
	if err != nil {
		h.writeInternalError(w, "results", idx.Info().ID, err)
		return
	}
	writeXML(w, http.StatusOK, body)
}

// resolveMode validates the t= search mode against the given capabilities, writing the
// appropriate error and returning ok=false on failure; on success it returns the
// resolved caps.modes key. A missing t defaults to the general search mode (Jackett's
// TorznabRequest default). The aggregate feed runs it against the UNION of member caps,
// so a mode no member supports is rejected exactly as an unsupported mode is on a
// per-indexer feed, and the key it returns is what selects the members that can serve it.
func (h *handler) resolveMode(w http.ResponseWriter, q url.Values, caps *mapper.Capabilities) (string, bool) {
	capsKey := mapper.ModeSearch
	if t := q.Get("t"); t != "" {
		var known bool
		if capsKey, known = tzn.ModeForRequest(t); !known {
			writeError(w, http.StatusBadRequest, codeNoSuchFunction, "No such function")
			return "", false
		}
	}
	if !tzn.ModeAvailable(caps, capsKey) {
		writeError(w, http.StatusBadRequest, codeNotAvailable, "Function Not Available: this indexer does not support that search mode")
		return "", false
	}
	if param, ok := unsupportedIDParam(caps, capsKey, q); !ok {
		writeError(w, http.StatusBadRequest, codeNotAvailable, "Function Not Available: "+param+" is not supported for this search mode")
		return "", false
	}
	return capsKey, true
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

// feedInfo assembles the feed metadata from the indexer identity + the request's
// self URL.
func (h *handler) feedInfo(r *http.Request, idx core.Indexer) tzn.FeedInfo {
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

// selfURL builds the atom:link self href, dropping the query string entirely so
// harbrr never reflects the caller's apikey, then routes it through RedactURL as
// defense in depth. It re-adds the configured base path (the server strips it before
// routing) so the served URL is the externally-visible one. The origin is
// h.urlCfg.ExternalOrigin when the operator configured one; otherwise it derives from
// the request scheme/host, honoring X-Forwarded-Proto only from a trusted proxy peer
// (apphttp.RequestScheme).
func (h *handler) selfURL(r *http.Request) string {
	origin := h.urlCfg.ExternalOrigin
	if origin == "" {
		origin = apphttp.RequestScheme(r, h.urlCfg.TrustedProxies) + "://" + r.Host
	}
	return apphttp.RedactURL(origin + h.urlCfg.BasePath + r.URL.Path)
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
	writeInternalErrorLog(w, h.log, stage, indexerID, err)
}

// writeInternalErrorLog is the logger-explicit form of writeInternalError.
func writeInternalErrorLog(w http.ResponseWriter, log zerolog.Logger, stage, indexerID string, err error) {
	grab.LogInternalError(log, stage, indexerID, err)
	writeError(w, http.StatusInternalServerError, codeUnknownError, grab.InternalErrorDescription(err))
}
