// Package app is harbrr's composition root. It owns construction order, the
// dependency graph, process lifecycle, background reaper startup/shutdown,
// cross-package adapter wiring, and the mounted HTTP handler. internal/server
// is not the composition root — it only mounts HTTP handlers onto a listener.
package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/announce"
	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/backup"
	"github.com/autobrr/harbrr/internal/config"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/download"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/definitions"
	"github.com/autobrr/harbrr/internal/indexer/native/catalog"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/logger"
	"github.com/autobrr/harbrr/internal/notify"
	"github.com/autobrr/harbrr/internal/proxy"
	"github.com/autobrr/harbrr/internal/resourcemigrate"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/server"
	"github.com/autobrr/harbrr/internal/solver"
	"github.com/autobrr/harbrr/internal/version"
	"github.com/autobrr/harbrr/internal/web/api"
	"github.com/autobrr/harbrr/internal/web/swagger"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
	"github.com/autobrr/harbrr/internal/web/ui"
	"github.com/autobrr/harbrr/web"
)

// canaryBlobKey / canaryIDKey are the app_meta keys for the startup secrets canary.
//
// cmd/harbrr/serve.go keeps its own copy of these two consts (and of
// openDatabase below) because the rotate-key subcommand also needs them and
// out-of-scope subcommand wiring is a follow-up, not this refactor — see
// AGENTS.md and the #144 PR notes.
const (
	canaryBlobKey = "secrets_canary"
	canaryIDKey   = "secrets_key_id"
)

// App is harbrr's dependency graph: every subsystem serve() used to wire by
// hand, now built once by New (in the fixed order documented there) and run
// by Run. Fields are unexported — callers reach the daemon only through
// Handler (full-mux tests) and Run (production); nothing outside this package
// re-wires them.
type App struct {
	cfg *config.Config
	log zerolog.Logger

	db      *database.DB
	keyring *secrets.Keyring

	sessions     *scs.SessionManager
	sessionStore *database.SessionStore
	auth         *auth.Service

	searchCache *registry.SearchCache
	registry    *registry.Registry

	notify   *notify.Service
	appsync  *appsync.Service
	announce *announce.Service

	proxy    *proxy.Service
	download *download.Service
	solver   *solver.Service
	backup   *backup.Service

	logLevel *api.LogLevelStore

	server *server.Server
	lc     net.ListenConfig
}

// New builds the full dependency graph in the order serve() used to wire it:
// database -> secrets/canary -> sessions/auth -> search cache -> notify
// (built BEFORE the registry so it can be registered as the registry's health
// sink) -> registry -> app-sync -> announce -> the search cache's announce
// sink (wired back after announce exists — see initSyncServices) -> log-level
// store -> proxy/solver -> the mounted HTTP handlers.
func New(ctx context.Context, deps Deps, opts ...Option) (*App, error) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	httpClient := o.httpClient
	if httpClient == nil {
		httpClient = appSyncClient()
	}

	a := &App{cfg: deps.Config, log: deps.Logger}

	db, err := resolveDatabase(ctx, deps.Config, o.db)
	if err != nil {
		return nil, err
	}
	a.db = db
	ownsDB := o.db == nil // New opened it itself, vs. a test injecting an already-open one.

	if err := a.build(ctx, httpClient); err != nil {
		// New opened the DB itself, so New closes it on the way out too — the
		// caller never got an *App to close it through. A WithDatabase-injected
		// DB is left open on error: the injector owns its lifecycle either way
		// (see the WithDatabase doc comment). On success, Run always closes it.
		if ownsDB {
			_ = a.db.Close()
		}
		return nil, err
	}

	return a, nil
}

// build wires everything after the database is open: secrets/canary, sessions/
// auth, the registry graph, app-sync/announce, the log-level store, proxy/
// solver, and finally the mounted HTTP handlers.
func (a *App) build(ctx context.Context, httpClient *http.Client) error {
	if err := a.initSecrets(ctx); err != nil {
		return err
	}
	a.initAuth()
	a.initRegistry(ctx, httpClient)
	a.initSyncServices(httpClient)
	a.initLogLevel(ctx)
	a.proxy = proxy.NewService(a.db, a.keyring)
	a.download = download.NewService(a.db, a.keyring, httpClient, a.log)
	a.solver = solver.NewService(a.db, a.keyring)
	a.backup = backup.NewService(a.db, a.keyring, a.log)

	srv, err := newServer(a)
	if err != nil {
		return err
	}
	a.server = srv
	return nil
}

// resolveDatabase opens+migrates the database from cfg, unless a test injected
// an already-open one via WithDatabase.
func resolveDatabase(ctx context.Context, cfg *config.Config, injected *database.DB) (*database.DB, error) {
	if injected != nil {
		return injected, nil
	}
	return openDatabase(ctx, cfg)
}

// openDatabase opens and migrates the SQLite database.
func openDatabase(ctx context.Context, cfg *config.Config) (*database.DB, error) {
	db, err := database.Open(cfg.DatabasePath())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return db, nil
}

// keyringOptions maps secrets config to the keyring options.
func keyringOptions(cfg *config.Config) secrets.KeyringOptions {
	return secrets.KeyringOptions{
		EncryptionKey:  cfg.Secrets.EncryptionKey,
		KeyFile:        cfg.Secrets.KeyFile,
		AllowPlaintext: cfg.Secrets.AllowPlaintext,
		DataDir:        cfg.DataDir,
	}
}

// initSecrets opens the keyring, verifies (or writes) the startup canary, and
// folds any legacy inline proxy/FlareSolverr settings into global resources.
func (a *App) initSecrets(ctx context.Context) error {
	keyring, err := secrets.OpenKeyring(keyringOptions(a.cfg), a.log)
	if err != nil {
		return fmt.Errorf("secrets: %w", err)
	}
	if err := verifyCanary(ctx, a.db, keyring); err != nil {
		return err
	}
	migrateResources(ctx, a.db, keyring, a.log)
	a.keyring = keyring
	return nil
}

// migrateResources runs the one-time fold of legacy inline proxy/FlareSolverr
// settings into global resources. Non-fatal: the engine keeps the inline settings
// as a fallback, so a failure leaves every indexer working and retries next boot.
func migrateResources(ctx context.Context, db *database.DB, keyring *secrets.Keyring, log zerolog.Logger) {
	if err := resourcemigrate.Run(ctx, db, keyring, time.Now, log); err != nil {
		log.Warn().Err(err).Msg("migrating inline proxy/FlareSolverr settings failed; inline settings remain in effect, will retry next boot")
	}
	if err := resourcemigrate.SplitProxyURLs(ctx, db, keyring, log); err != nil {
		log.Warn().Err(err).Msg("splitting legacy proxy URLs into structured fields failed; will retry next boot")
	}
}

// verifyCanary writes the secrets canary on first run, or verifies it on later
// runs — failing loud (refusing to start) on a wrong/changed key.
func verifyCanary(ctx context.Context, db *database.DB, keyring *secrets.Keyring) error {
	meta := database.AppMeta{}
	blob, haveBlob, err := meta.Get(ctx, db, canaryBlobKey)
	if err != nil {
		return err //nolint:wrapcheck // already "database:"-wrapped.
	}
	keyID, haveID, err := meta.Get(ctx, db, canaryIDKey)
	if err != nil {
		return err //nolint:wrapcheck // already "database:"-wrapped.
	}

	if !haveBlob || !haveID {
		return initCanary(ctx, db, meta, keyring)
	}
	if err := keyring.VerifyCanary(keyID, blob); err != nil {
		return fmt.Errorf("startup: %w", err)
	}
	return nil
}

// initCanary writes the first-run canary.
func initCanary(ctx context.Context, db *database.DB, meta database.AppMeta, keyring *secrets.Keyring) error {
	blob, err := keyring.EncryptCanary()
	if err != nil {
		return fmt.Errorf("startup: write canary: %w", err)
	}
	if err := meta.Set(ctx, db, canaryBlobKey, blob); err != nil {
		return err //nolint:wrapcheck // already "database:"-wrapped.
	}
	if err := meta.Set(ctx, db, canaryIDKey, keyring.KeyID()); err != nil {
		return err //nolint:wrapcheck // already "database:"-wrapped.
	}
	return nil
}

// initAuth builds the session store, the session manager, and the auth
// service. The store is kept as a field (it is stateless over a.db, but Run's
// session reaper needs the same instance the session manager uses).
func (a *App) initAuth() {
	a.sessionStore = database.NewSessionStore(a.db)
	a.sessions = sessionManager(a.sessionStore, a.cfg)
	a.auth = auth.NewService(a.db)
}

// sessionManager builds the SCS session manager with the family cookie hardening.
// Secure is computed once here from config (never mutated per-request): either the
// manual secure_cookie override, or automatically when external_url's scheme is
// https. The CSRF companion cookie (internal/web/api/csrf.go) inherits Secure from
// this same sm.Cookie, so it never needs its own derivation.
func sessionManager(store *database.SessionStore, cfg *config.Config) *scs.SessionManager {
	sm := scs.New()
	sm.Store = store
	sm.Lifetime = 30 * 24 * time.Hour
	sm.Cookie.Name = "harbrr_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Persist = false
	sm.Cookie.Secure = cfg.Server.SecureCookie || cfg.Server.ExternalHTTPS()
	sm.Cookie.Path = cookiePath(cfg.Server.BaseURL)
	return sm
}

// cookiePath scopes the session cookie to the base path (or root).
func cookiePath(baseURL string) string {
	if baseURL == "" {
		return "/"
	}
	return baseURL
}

// initRegistry builds the search cache and the registry. notify.Service is
// constructed BEFORE the registry: it is passed in as registry.WithHealthSink,
// so a recorded indexer failure can fan out (async, best-effort) to configured
// notification targets.
func (a *App) initRegistry(ctx context.Context, httpClient *http.Client) {
	a.searchCache = buildSearchCache(ctx, a.db, a.cfg, a.log)
	a.notify = notify.NewService(a.db, a.keyring, httpClient, a.log)
	a.registry = registry.New(a.db, loader.New(dropinDir(a.cfg)), a.keyring, catalog.All(),
		registry.WithLogger(a.log), registry.WithSearchCache(a.searchCache), registry.WithHealthSink(a.notify))
	if err := a.registry.RehydrateStats(ctx); err != nil {
		a.log.Warn().Err(err).Msg("loading indexer stat counters failed; counters start at zero this session")
	}
}

// buildSearchCache constructs the registry-wide search-results cache. It is ALWAYS
// installed (so cache.enabled is a runtime toggle the decorator self-gates on, not
// a boot-time wiring decision); the tuning is seeded from the config file, then any
// persisted app_settings overrides are overlaid. An overrides-load failure is
// logged and non-fatal (the config-file seed stands).
func buildSearchCache(ctx context.Context, db *database.DB, cfg *config.Config, log zerolog.Logger) *registry.SearchCache {
	sc := registry.NewSearchCacheFromConfig(db, registry.CacheConfigView{
		Enabled:         cfg.Cache.Enabled,
		RSSTTL:          cfg.Cache.RSSDuration(),
		KeywordTTL:      cfg.Cache.KeywordDuration(),
		ThinTTL:         cfg.Cache.ThinDuration(),
		ThinThreshold:   cfg.Cache.ThinThreshold,
		RefreshAheadPct: cfg.Cache.RefreshAheadPct,
		NegativeTTL:     cfg.Cache.NegativeDuration(),
		CleanupInterval: cfg.Cache.CleanupDuration(),
	}, time.Now, log)
	if err := sc.LoadOverrides(ctx); err != nil {
		log.Warn().Err(err).Msg("loading cache config overrides failed; using config-file defaults")
	}
	if err := sc.RehydrateCounters(ctx); err != nil {
		log.Warn().Err(err).Msg("loading cache stat counters failed; counters start at zero this session")
	}
	return sc
}

// initSyncServices builds app-sync and announce, then wires the search cache's
// announce sink back to the freshly built announce service. This two-step
// (the cache is built without an announce sink in initRegistry; the sink is
// attached here) is the cache<->announce dependency cycle: it is intentional
// and stays explicit at the composition root rather than folded into either
// constructor.
func (a *App) initSyncServices(httpClient *http.Client) {
	a.appsync = appsync.NewService(a.db, registrySource{reg: a.registry}, a.auth, a.keyring, httpClient, a.log)
	a.announce = announce.NewService(a.db, a.auth, a.keyring, announce.DefaultTargetFactory(httpClient, nil, nil), a.log)
	a.searchCache.SetAnnounceSink(newAnnounceSink(a.announce, a.db, a.keyring, a.cfg.Server.BaseURL, a.cfg.Server.ExternalOrigin(), a.log))
}

// initLogLevel builds the persisted log-level store and applies any override.
func (a *App) initLogLevel(ctx context.Context) {
	a.logLevel = api.NewLogLevelStore(a.db, time.Now)
	applyPersistedLogLevel(ctx, a.logLevel, a.log)
}

// applyPersistedLogLevel applies the DB log-level override (set via the management
// API), which beats the config-file/env/flag seed. A read error or stale value is
// non-fatal — the seed stays in effect.
func applyPersistedLogLevel(ctx context.Context, logLevel *api.LogLevelStore, log zerolog.Logger) {
	if applied, err := logLevel.ApplyPersisted(ctx); err != nil {
		log.Warn().Err(err).Msg("serve: applying persisted log level failed; using configured level")
	} else if applied {
		log.Info().Str("level", logger.Level()).Msg("serve: applied persisted log-level override")
	}
}

// newServer builds the management API router, the Torznab feed handler, and
// the embedded UI handler, then mounts them on internal/server.
func newServer(a *App) (*server.Server, error) {
	urlCfg, err := feedURLConfig(a.cfg)
	if err != nil {
		return nil, err
	}

	mgmt, err := api.NewRouter(api.Deps{
		Auth: a.auth, Registry: a.registry, Loader: loader.New(dropinDir(a.cfg)), AppSync: a.appsync,
		Announce: a.announce, Notify: a.notify, Proxy: a.proxy, Download: a.download, Solver: a.solver, Backup: a.backup, Sessions: a.sessions,
		DLToken: a.keyring, URLConfig: urlCfg, Cache: a.searchCache, Logger: a.log, LogLevel: a.logLevel,
	}, api.Config{
		AuthDisabled: a.cfg.Auth.AuthDisabled(), IPAllowlist: a.cfg.Auth.IPAllowlist, TrustedProxies: a.cfg.Auth.TrustedProxies,
		Port: a.cfg.Server.Port,
	})
	if err != nil {
		return nil, fmt.Errorf("management api: %w", err)
	}

	tz := torznabhttp.NewHandler(
		a.registry,
		torznabhttp.WithAPIKeyValidator(apiKeyValidator(a.auth)),
		torznabhttp.WithBasePath(urlCfg.BasePath),
		torznabhttp.WithExternalURL(urlCfg.ExternalOrigin),
		torznabhttp.WithTrustedProxies(urlCfg.TrustedProxies),
		torznabhttp.WithLogger(a.log),
		torznabhttp.WithDLToken(a.keyring),
	)

	uiHandler, err := buildUIHandler(a.cfg)
	if err != nil {
		return nil, err
	}

	return server.New(server.Deps{Management: mgmt, Torznab: tz, UI: uiHandler, Spec: swagger.Spec(), DocsUI: swagger.UI(), Logger: a.log},
		server.Config{Addr: listenAddr(a.cfg), BasePath: a.cfg.Server.BaseURL}), nil
}

// feedURLConfig builds the shared input for every absolute feed/dl URL the Torznab
// handler and the management API's crossseed/download routes build: the base path,
// the operator-configured external origin (authoritative when set), and the
// trusted-proxy check gating X-Forwarded-Proto in the request-derived fallback
// (auth.trusted_proxies — the same peers already trusted for X-Forwarded-For).
func feedURLConfig(cfg *config.Config) (torznabhttp.URLConfig, error) {
	trusted, err := apphttp.ParseTrustedProxies(cfg.Auth.TrustedProxies)
	if err != nil {
		return torznabhttp.URLConfig{}, fmt.Errorf("auth.trusted_proxies: %w", err)
	}
	return torznabhttp.URLConfig{
		BasePath:       cfg.Server.BaseURL,
		ExternalOrigin: cfg.Server.ExternalOrigin(),
		TrustedProxies: trusted,
	}, nil
}

// buildUIHandler serves the embedded SPA bundle (web/dist) with the base path,
// version, and configured external_url injected for the client (internal/web/ui).
func buildUIHandler(cfg *config.Config) (http.Handler, error) {
	distFS, err := web.Dist()
	if err != nil {
		return nil, fmt.Errorf("web ui bundle: %w", err)
	}
	return ui.NewHandler(distFS, cfg.Server.BaseURL, version.String(), cfg.Server.ExternalURL), nil
}

// dropinDir is the on-disk drop-in definitions directory under the data dir.
func dropinDir(cfg *config.Config) string {
	return filepath.Join(cfg.DataDir, definitions.DropInDir)
}

// listenAddr is the host:port the server binds.
func listenAddr(cfg *config.Config) string {
	return net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))
}

// Handler returns the fully mounted daemon handler (management API, Torznab
// feed, embedded UI, OpenAPI/Swagger) for httptest-based end-to-end tests. It
// does NOT start the background reapers — Run owns those — so exercising
// Handler alone never touches the periodic DB writes.
func (a *App) Handler() http.Handler { return a.server.Handler() }

// Run binds the listener, starts the background reapers, serves until ctx is
// cancelled, then shuts down in the fixed order: reapers stop and flush, then
// in-flight notification dispatches drain, then the database closes last. The
// reapers write to the DB on shutdown (the search cache flushes its buffered
// touches and stat counters); closing the database first would race or lose
// that flush. bgCancel also unblocks the reapers if serveUntilDone returns
// early on a listen error (ctx not yet cancelled), so bg.Wait can't hang.
func (a *App) Run(ctx context.Context) error {
	bgCtx, bgCancel := context.WithCancel(ctx)
	var bg sync.WaitGroup
	startReapers(bgCtx, &bg, a.db, a.sessionStore, a.searchCache, a.registry, a.log)

	runErr := a.serveUntilDone(ctx)

	bgCancel()
	bg.Wait()
	drainNotify(ctx, a.notify)
	if err := a.db.Close(); err != nil {
		a.log.Warn().Err(err).Msg("closing database failed")
	}

	return runErr
}

// serveUntilDone confirms the port is bindable, logs startup, then serves
// until ctx is cancelled or a fatal listen error occurs.
func (a *App) serveUntilDone(ctx context.Context) error {
	// Confirm the port is actually bindable before logging "listening": server.Run
	// binds asynchronously, so a fatal listen error (e.g. address in use) would
	// otherwise surface only after we'd already told the operator the server was up.
	if err := a.preflightBind(ctx, listenAddr(a.cfg)); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	logStartup(a.log, a.cfg, a.keyring)
	if err := a.server.Run(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// preflightBind verifies the resolved address can be bound, then releases it so
// server.Run can re-bind the same addr. This narrow window is acceptable for
// single-user self-hosted use; the point is to fail loud on an in-use port instead
// of falsely logging that the server is listening.
func (a *App) preflightBind(ctx context.Context, addr string) error {
	ln, err := a.lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	if err := ln.Close(); err != nil {
		return fmt.Errorf("release preflight listener %s: %w", addr, err)
	}
	return nil
}

// drainNotify joins in-flight notification dispatch goroutines (which read the DB)
// before the database closes, bounded so a hanging webhook can't stall shutdown.
func drainNotify(ctx context.Context, svc *notify.Service) {
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	svc.Drain(drainCtx)
}

// logStartup logs a one-line snapshot of the build and the resolved serving/config
// parameters, so an operator (or a shared bug report) can confirm the version, commit,
// and how the instance is running at a glance. The secrets mode is a status word
// ("encrypted"/"plaintext"), never key material.
func logStartup(log zerolog.Logger, cfg *config.Config, keyring *secrets.Keyring) {
	log.Info().
		Str("version", version.Version).
		Str("commit", version.Commit).
		Str("addr", listenAddr(cfg)).
		Str("base_url", cfg.Server.BaseURL).
		Str("config_file", cfg.ConfigFile).
		Str("data_dir", cfg.DataDir).
		Str("log_level", logger.Level()).
		Str("log_format", cfg.Log.Format).
		Str("secrets", secretsMode(keyring)).
		Bool("auth_disabled", cfg.Auth.AuthDisabled()).
		Msg("harbrr listening")
}

// secretsMode reports the at-rest storage mode as a status word for the startup log.
func secretsMode(keyring *secrets.Keyring) string {
	if keyring.Plaintext() {
		return "plaintext"
	}
	return "encrypted"
}
