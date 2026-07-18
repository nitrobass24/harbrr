package api

import (
	"context"
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
	"github.com/autobrr/harbrr/internal/backup"
	"github.com/autobrr/harbrr/internal/download"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/notify"
	"github.com/autobrr/harbrr/internal/proxy"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/solver"
	"github.com/autobrr/harbrr/internal/version"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// Deps are the collaborators the management API drives.
type Deps struct {
	Auth     *auth.Service
	Registry *registry.Registry
	Loader   *loader.Loader
	AppSync  *appsync.Service
	Announce *announce.Service
	Notify   *notify.Service
	Proxy    *proxy.Service
	Download *download.Service
	Solver   *solver.Service
	Backup   *backup.Service
	Sessions *scs.SessionManager
	// DLToken seals a resolver-needing indexer's download link behind the /dl proxy
	// for the JSON search response, exactly as the Torznab feed does, so a passkey
	// never reaches the client. Nil disables the proxy (then resolver links are
	// withheld from the JSON response rather than served in the clear).
	DLToken *secrets.Keyring
	// URLConfig is the shared input for building absolute /dl and feed URLs: the
	// externally-visible base path (the server strips it before routing, so it must be
	// re-added), the operator-configured external origin (authoritative when set), and
	// the trusted-proxy check gating X-Forwarded-Proto in the request-derived fallback.
	URLConfig torznabhttp.URLConfig
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
	// Port is the live effective listening port (config.ServerConfig.Port), surfaced
	// via /api/server-info so the frontend can flag app-sync connections whose stored
	// HarbrrURL was baked in against a since-changed port.
	Port int
	// OIDC configures OpenID Connect / SSO login (autobrr/harbrr#9). A zero value
	// disables it; discovery failure at NewRouter time also disables it for the
	// run (logged, not fatal) rather than refusing to serve.
	OIDC OIDCConfig
}

// router holds the management API's dependencies and resolved config.
type router struct {
	auth     *auth.Service
	registry *registry.Registry
	loader   *loader.Loader
	appsync  *appsync.Service
	announce *announce.Service
	notify   *notify.Service
	proxy    *proxy.Service
	download *download.Service
	solver   *solver.Service
	backup   *backup.Service
	sessions *scs.SessionManager
	dlToken  *secrets.Keyring
	urlCfg   torznabhttp.URLConfig
	cache    *registry.SearchCache
	cfg      Config
	log      zerolog.Logger
	logLevel *LogLevelStore
	// oidc is nil when OIDC is disabled or its provider discovery failed at
	// startup; every OIDC handler treats a nil oidc as "answer as disabled".
	oidc *oidcHandler

	allowlist      []*net.IPNet
	trustedProxies apphttp.TrustedProxies

	// loadDefs summarizes the addable definitions; injectable so a test can drive a
	// fail-then-succeed load. Defaults (in NewRouter) to the real loader closure.
	loadDefs func() ([]definitionSummary, error)
	// The definition summary list is memoized on SUCCESS ONLY: defsLoaded gates the
	// cache and is set solely after a nil-error load, so a transient first-call
	// failure lets the next call retry rather than wedging the endpoint at 500.
	defsMu     sync.Mutex
	defs       []definitionSummary
	defsLoaded bool
}

// NewRouter builds the management API handler. It fails closed: auth-disabled mode
// without an IP allowlist is rejected rather than serving an open instance.
func NewRouter(deps Deps, cfg Config) (http.Handler, error) {
	allow, err := apphttp.ParseCIDRs(cfg.IPAllowlist)
	if err != nil {
		return nil, fmt.Errorf("api: ip_allowlist: %w", err)
	}
	proxies, err := apphttp.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("api: trusted_proxies: %w", err)
	}
	if cfg.AuthDisabled && len(allow) == 0 {
		return nil, errors.New("api: auth_disabled requires a non-empty ip_allowlist (refusing to serve an open instance)")
	}

	rt := &router{
		auth: deps.Auth, registry: deps.Registry, loader: deps.Loader, appsync: deps.AppSync,
		announce: deps.Announce, notify: deps.Notify, proxy: deps.Proxy, download: deps.Download, solver: deps.Solver,
		backup:   deps.Backup,
		sessions: deps.Sessions, dlToken: deps.DLToken, urlCfg: deps.URLConfig,
		cache: deps.Cache, cfg: cfg, log: deps.Logger, logLevel: deps.LogLevel,
		allowlist: allow, trustedProxies: proxies,
	}
	rt.loadDefs = func() ([]definitionSummary, error) {
		return loadDefinitionSummaries(rt.loader, rt.registry.NativeDefinitions())
	}
	rt.initOIDC()
	return rt.routes(), nil
}

// initOIDC discovers the OIDC provider (retrying — see discoverOIDCProvider)
// when configured. Discovery failure is logged and non-fatal: the instance
// still serves, OIDC just answers as disabled for this run rather than
// refusing to start over a slow or unreachable IdP.
func (rt *router) initOIDC() {
	if !rt.cfg.OIDC.Enabled {
		return
	}
	h, err := newOIDCHandler(context.Background(), rt.cfg.OIDC)
	if err != nil {
		rt.log.Warn().Str("error", apphttp.RedactError(err)).Msg("api: oidc initialization failed; SSO login is disabled this run")
		return
	}
	rt.oidc = h
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
		r.Get("/api/auth/oidc/config", rt.oidcConfig)
		r.Get("/api/auth/oidc/callback", rt.oidcCallback)

		// Authenticated routes (session or X-API-Key; auth-disabled mode allowed).
		r.Group(func(r chi.Router) {
			r.Use(rt.resolveAuth, rt.requireAuth, rt.csrf)

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
			// The static "stats"/"status" segments are registered so chi prioritizes
			// them over the {slug} param at the same level: GET /api/indexers/stats and
			// /api/indexers/status resolve to allIndexerStats/allIndexerStatus, not
			// getIndexer.
			r.Get("/api/indexers/stats", rt.allIndexerStats)
			r.Get("/api/indexers/status", rt.allIndexerStatus)
			r.Get("/api/indexers/{slug}", rt.getIndexer)
			r.Patch("/api/indexers/{slug}", rt.updateIndexer)
			r.Delete("/api/indexers/{slug}", rt.deleteIndexer)
			r.Post("/api/indexers/{slug}/enable", rt.enableIndexer)
			r.Post("/api/indexers/{slug}/disable", rt.disableIndexer)
			r.Post("/api/indexers/{slug}/test", rt.testIndexer)
			r.Get("/api/indexers/{slug}/status", rt.indexerStatus)
			r.Get("/api/indexers/{slug}/stats", rt.indexerStats)
			r.Get("/api/indexers/{slug}/search", rt.searchIndexer)
			// Session-authed download of a search result (the web UI's cookie-auth sibling
			// of the feed's apikey /dl proxy); the token is sealed into the JSON search
			// response link. GET, so exempt from CSRF.
			r.Get("/api/indexers/{slug}/download/{token}", rt.downloadRelease)
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
			r.Post("/api/app-connections/{id}/announce-target", rt.createAnnounceTargetFromAppConnection)

			r.Get("/api/announce-connections", rt.listAnnounceConnections)
			r.Post("/api/announce-connections", rt.createAnnounceConnection)
			r.Get("/api/announce-connections/{id}", rt.getAnnounceConnection)
			r.Patch("/api/announce-connections/{id}", rt.updateAnnounceConnection)
			r.Delete("/api/announce-connections/{id}", rt.deleteAnnounceConnection)
			r.Post("/api/announce-connections/{id}/enable", rt.enableAnnounceConnection)
			r.Post("/api/announce-connections/{id}/disable", rt.disableAnnounceConnection)
			r.Post("/api/announce-connections/{id}/test", rt.testAnnounceConnection)

			rt.mountResourceRoutes(r)
			rt.mountSystemRoutes(r)
		})
	})
	return r
}

// mountSystemRoutes registers the cache/config/logging/server-info endpoints —
// split out of routes() (which trips funlen otherwise) alongside the existing
// mountResourceRoutes split.
func (rt *router) mountSystemRoutes(r chi.Router) {
	r.Get("/api/cache/stats", rt.cacheStats)
	r.Post("/api/cache/flush", rt.cacheFlush)
	r.Get("/api/cache/config", rt.cacheConfigGet)
	r.Put("/api/cache/config", rt.cacheConfigPut)

	r.Get("/api/config/log-level", rt.getLogLevel)
	r.Put("/api/config/log-level", rt.putLogLevel)

	r.Get("/api/config/rate-limit", rt.rateLimitGet)
	r.Put("/api/config/rate-limit", rt.rateLimitPut)

	r.Post("/api/logs/frontend", rt.postFrontendLog)

	r.Get("/api/server-info", rt.serverInfo)
}

// mountResourceRoutes registers the CRUD routes for the global proxy + anti-bot-solver
// resources an indexer references by id, plus notifications, download clients, sync
// profiles, and the config/DB backup export/import routes. Split out of routes() to
// keep that function under the funlen gate.
func (rt *router) mountResourceRoutes(r chi.Router) {
	r.Post("/api/export", rt.exportBackup)
	r.Post("/api/import", rt.importBackup)

	r.Get("/api/notifications", rt.listNotifications)
	r.Post("/api/notifications", rt.createNotification)
	r.Get("/api/notifications/{id}", rt.getNotification)
	r.Patch("/api/notifications/{id}", rt.updateNotification)
	r.Delete("/api/notifications/{id}", rt.deleteNotification)
	r.Post("/api/notifications/{id}/enable", rt.enableNotification)
	r.Post("/api/notifications/{id}/disable", rt.disableNotification)
	r.Post("/api/notifications/{id}/test", rt.testNotification)

	r.Get("/api/proxies", rt.listProxies)
	r.Post("/api/proxies", rt.createProxy)
	r.Get("/api/proxies/{id}", rt.getProxy)
	r.Patch("/api/proxies/{id}", rt.updateProxy)
	r.Delete("/api/proxies/{id}", rt.deleteProxy)

	r.Get("/api/download-clients", rt.listDownloadClients)
	r.Post("/api/download-clients", rt.createDownloadClient)
	r.Get("/api/download-clients/{id}", rt.getDownloadClient)
	r.Patch("/api/download-clients/{id}", rt.updateDownloadClient)
	r.Delete("/api/download-clients/{id}", rt.deleteDownloadClient)
	r.Post("/api/download-clients/{id}/test", rt.testDownloadClient)
	r.Post("/api/download-clients/{id}/enable", rt.enableDownloadClient)
	r.Post("/api/download-clients/{id}/disable", rt.disableDownloadClient)

	r.Get("/api/solvers", rt.listSolvers)
	r.Post("/api/solvers", rt.createSolver)
	r.Get("/api/solvers/{id}", rt.getSolver)
	r.Patch("/api/solvers/{id}", rt.updateSolver)
	r.Delete("/api/solvers/{id}", rt.deleteSolver)

	r.Get("/api/sync-profiles", rt.listSyncProfiles)
	r.Post("/api/sync-profiles", rt.createSyncProfile)
	r.Get("/api/sync-profiles/{id}", rt.getSyncProfile)
	r.Patch("/api/sync-profiles/{id}", rt.updateSyncProfile)
	r.Delete("/api/sync-profiles/{id}", rt.deleteSyncProfile)
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
