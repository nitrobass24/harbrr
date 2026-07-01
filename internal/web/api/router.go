package api

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/announce"
	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/version"
)

// Deps are the collaborators the management API drives.
type Deps struct {
	Auth     *auth.Service
	Registry *registry.Registry
	Loader   *loader.Loader
	AppSync  *appsync.Service
	Announce *announce.Service
	Sessions *scs.SessionManager
	// DLToken seals a resolver-needing indexer's download link behind the /dl proxy
	// for the JSON search response, exactly as the Torznab feed does, so a passkey
	// never reaches the client. Nil disables the proxy (then resolver links are
	// withheld from the JSON response rather than served in the clear).
	DLToken *secrets.Keyring
	// BasePath is the externally-visible base path, used to build absolute /dl URLs
	// (the server strips it before routing, so it must be re-added).
	BasePath string
	// Cache is the search-results cache backing the /api/cache stats/flush routes.
	// Nil means caching is disabled; those routes then report a disabled state
	// rather than 404 (wired in a later leaf).
	Cache  *registry.SearchCache
	Logger zerolog.Logger
	// LogLevel backs the runtime log-level endpoints (get/set + persistence). Nil
	// leaves those routes reporting an unavailable state rather than panicking.
	LogLevel *LogLevelStore
}

// Config is the API's auth posture (mapped from the app config by the server).
type Config struct {
	// AuthDisabled serves a synthetic admin to allowlisted IPs (behind an
	// authenticating reverse proxy). It REQUIRES a non-empty IPAllowlist.
	AuthDisabled bool
	// IPAllowlist is the set of IPs/CIDRs permitted in auth-disabled mode.
	IPAllowlist []string
	// TrustedProxies are peers whose X-Forwarded-For is honored for the allowlist.
	TrustedProxies []string
}

// router holds the management API's dependencies and resolved config.
type router struct {
	auth     *auth.Service
	registry *registry.Registry
	loader   *loader.Loader
	appsync  *appsync.Service
	announce *announce.Service
	sessions *scs.SessionManager
	dlToken  *secrets.Keyring
	basePath string
	cache    *registry.SearchCache
	cfg      Config
	log      zerolog.Logger
	logLevel *LogLevelStore

	allowlist      []*net.IPNet
	trustedProxies []*net.IPNet

	defsOnce sync.Once
	defs     []definitionSummary
	defsErr  error
}

// NewRouter builds the management API handler. It fails closed: auth-disabled mode
// without an IP allowlist is rejected rather than serving an open instance.
func NewRouter(deps Deps, cfg Config) (http.Handler, error) {
	allow, err := parseCIDRs(cfg.IPAllowlist)
	if err != nil {
		return nil, fmt.Errorf("api: ip_allowlist: %w", err)
	}
	proxies, err := parseCIDRs(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("api: trusted_proxies: %w", err)
	}
	if cfg.AuthDisabled && len(allow) == 0 {
		return nil, errors.New("api: auth_disabled requires a non-empty ip_allowlist (refusing to serve an open instance)")
	}

	rt := &router{
		auth: deps.Auth, registry: deps.Registry, loader: deps.Loader, appsync: deps.AppSync,
		announce: deps.Announce, sessions: deps.Sessions, dlToken: deps.DLToken, basePath: deps.BasePath,
		cache: deps.Cache, cfg: cfg, log: deps.Logger, logLevel: deps.LogLevel,
		allowlist: allow, trustedProxies: proxies,
	}
	return rt.routes(), nil
}

// routes registers the chi route tree. Paths are flat (not nested Route groups)
// so chi's patterns exactly match the OpenAPI paths the drift test compares.
func (rt *router) routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", rt.healthz) // liveness; no session

	r.Group(func(r chi.Router) {
		r.Use(rt.sessions.LoadAndSave)

		// Public (pre-session) auth routes.
		r.Get("/api/auth/setup", rt.getSetup)
		r.Post("/api/auth/setup", rt.postSetup)
		r.Post("/api/auth/login", rt.login)
		r.Get("/api/auth/oidc/login", oidcStub)
		r.Get("/api/auth/oidc/callback", oidcStub)

		// Authenticated routes (session or X-API-Key; auth-disabled mode allowed).
		r.Group(func(r chi.Router) {
			r.Use(rt.resolveAuth, rt.requireAuth)

			r.Get("/api/auth/me", rt.me)
			r.Post("/api/auth/logout", rt.logout)
			r.Post("/api/auth/change-password", rt.changePassword)
			r.Get("/api/definitions", rt.listDefinitions)
			r.Get("/api/definitions/{id}", rt.getDefinition)

			r.Get("/api/apikeys", rt.listAPIKeys)
			r.Post("/api/apikeys", rt.mintAPIKey)
			r.Delete("/api/apikeys/{id}", rt.deleteAPIKey)

			r.Get("/api/indexers", rt.listIndexers)
			r.Post("/api/indexers", rt.addIndexer)
			r.Get("/api/indexers/{slug}", rt.getIndexer)
			r.Patch("/api/indexers/{slug}", rt.updateIndexer)
			r.Delete("/api/indexers/{slug}", rt.deleteIndexer)
			r.Post("/api/indexers/{slug}/enable", rt.enableIndexer)
			r.Post("/api/indexers/{slug}/disable", rt.disableIndexer)
			r.Post("/api/indexers/{slug}/test", rt.testIndexer)
			r.Get("/api/indexers/{slug}/status", rt.indexerStatus)
			r.Get("/api/indexers/{slug}/search", rt.searchIndexer)
			r.Get("/api/indexers/{slug}/capabilities", rt.indexerCapabilities)
			r.Get("/api/indexers/{slug}/crossseed-snippet", rt.crossSeedSnippet)

			r.Get("/api/app-connections", rt.listConnections)
			r.Post("/api/app-connections", rt.createConnection)
			r.Post("/api/app-connections/sync", rt.syncAllConnections)
			r.Get("/api/app-connections/{id}", rt.getConnection)
			r.Patch("/api/app-connections/{id}", rt.updateConnection)
			r.Delete("/api/app-connections/{id}", rt.deleteConnection)
			r.Post("/api/app-connections/{id}/enable", rt.enableConnection)
			r.Post("/api/app-connections/{id}/disable", rt.disableConnection)
			r.Post("/api/app-connections/{id}/test", rt.testConnection)
			r.Post("/api/app-connections/{id}/sync", rt.syncConnection)
			r.Get("/api/app-connections/{id}/status", rt.connectionStatus)
			r.Put("/api/app-connections/{id}/indexers", rt.setConnectionIndexers)

			r.Get("/api/announce-connections", rt.listAnnounceConnections)
			r.Post("/api/announce-connections", rt.createAnnounceConnection)
			r.Get("/api/announce-connections/{id}", rt.getAnnounceConnection)
			r.Delete("/api/announce-connections/{id}", rt.deleteAnnounceConnection)
			r.Post("/api/announce-connections/{id}/enable", rt.enableAnnounceConnection)
			r.Post("/api/announce-connections/{id}/disable", rt.disableAnnounceConnection)

			r.Get("/api/cache/stats", rt.cacheStats)
			r.Post("/api/cache/flush", rt.cacheFlush)
			r.Get("/api/cache/config", rt.cacheConfigGet)
			r.Put("/api/cache/config", rt.cacheConfigPut)

			r.Get("/api/config/log-level", rt.getLogLevel)
			r.Put("/api/config/log-level", rt.putLogLevel)
		})
	})
	return r
}

// healthResponse is the liveness-probe body: a fixed status plus the build identity,
// so an operator can read the running version/commit without shell access.
type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// healthz is the liveness probe. It also surfaces the build version/commit.
func (rt *router) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:  "ok",
		Version: version.Version,
		Commit:  version.Commit,
	})
}

// oidcStub answers the deferred OIDC endpoints with 501 (see the end-of-phase
// report; OIDC is a later-phase item).
func oidcStub(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "OIDC is not implemented yet")
}
