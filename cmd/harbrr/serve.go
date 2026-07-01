package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/autobrr/harbrr/internal/announce"
	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/config"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/definitions"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/logger"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/server"
	"github.com/autobrr/harbrr/internal/version"
	"github.com/autobrr/harbrr/internal/web/api"
	"github.com/autobrr/harbrr/internal/web/swagger"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// canaryKeyID / canaryBlob are the app_meta keys for the startup secrets canary.
const (
	canaryBlobKey = "secrets_canary"
	canaryIDKey   = "secrets_key_id"
)

// newServeCmd runs the harbrr daemon.
func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the harbrr server",
		Args:  cobra.NoArgs,
		RunE:  runServe,
	}
}

func runServe(cmd *cobra.Command, _ []string) error {
	cfgFile, err := cmd.Flags().GetString("config")
	if err != nil {
		return fmt.Errorf("read --config flag: %w", err)
	}
	cfg, err := config.Load(cfgFile, cmd.Flags())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logger.New(cfg.Log, cmd.OutOrStdout())
	// Seed the process-global level from config; a persisted DB override (if any) is
	// applied later, once the database is open (see serve).
	if err := logger.SetLevel(cfg.Log.Level); err != nil {
		return fmt.Errorf("init logger: %w", err)
	}

	// Derive from the command context so tests can drive shutdown; production has
	// no parent context and relies on the signal handler.
	base := cmd.Context()
	if base == nil {
		base = context.Background()
	}
	ctx, stop := signal.NotifyContext(base, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return serve(ctx, cfg, log)
}

// serve wires every subsystem and runs the server until ctx is cancelled.
func serve(ctx context.Context, cfg *config.Config, log zerolog.Logger) error {
	db, err := openDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	keyring, err := secrets.OpenKeyring(keyringOptions(cfg), log)
	if err != nil {
		return fmt.Errorf("secrets: %w", err)
	}
	if err := verifyCanary(ctx, db, keyring); err != nil {
		return err
	}

	store := database.NewSessionStore(db)
	sessions := sessionManager(store, cfg)
	authSvc := auth.NewService(db)

	searchCache := buildSearchCache(ctx, db, cfg, log)
	regOpts := []registry.Option{registry.WithLogger(log), registry.WithSearchCache(searchCache)}
	reg := registry.New(db, loader.New(dropinDir(cfg)), keyring, regOpts...)
	if err := reg.RehydrateStats(ctx); err != nil {
		log.Warn().Err(err).Msg("loading indexer stat counters failed; counters start at zero this session")
	}
	appSync := appsync.NewService(db, registrySource{reg: reg}, authSvc, keyring, appSyncClient(), log)
	announceSvc := announce.NewService(db, authSvc, keyring,
		announce.DefaultTargetFactory(appSyncClient(), nil, nil), log)
	// Wire the cross-seed announce source: new releases on an RSS cache fill are pushed to
	// enabled announce targets (best-effort, async — see newAnnounceSink).
	searchCache.SetAnnounceSink(newAnnounceSink(announceSvc, db, keyring, cfg.Server.BaseURL, log))

	// A persisted DB override (set via the management API) beats the config-file/env/flag
	// seed; apply it now that the DB is open. A read error or stale value is non-fatal —
	// the seed stays in effect.
	logLevel := api.NewLogLevelStore(db, time.Now)
	if applied, err := logLevel.ApplyPersisted(ctx); err != nil {
		log.Warn().Err(err).Msg("serve: applying persisted log level failed; using configured level")
	} else if applied {
		log.Info().Str("level", logger.Level()).Msg("serve: applied persisted log-level override")
	}

	mgmt, err := api.NewRouter(api.Deps{
		Auth: authSvc, Registry: reg, Loader: loader.New(dropinDir(cfg)), AppSync: appSync,
		Announce: announceSvc, Sessions: sessions,
		DLToken: keyring, BasePath: cfg.Server.BaseURL, Cache: searchCache, Logger: log,
		LogLevel: logLevel,
	}, api.Config{
		AuthDisabled: cfg.Auth.AuthDisabled(), IPAllowlist: cfg.Auth.IPAllowlist, TrustedProxies: cfg.Auth.TrustedProxies,
	})
	if err != nil {
		return fmt.Errorf("management api: %w", err)
	}

	tz := torznabhttp.NewHandler(
		reg,
		torznabhttp.WithAPIKeyValidator(apiKeyValidator(authSvc)),
		torznabhttp.WithBasePath(cfg.Server.BaseURL),
		torznabhttp.WithLogger(log),
		torznabhttp.WithDLToken(keyring),
	)

	srv := server.New(server.Deps{Management: mgmt, Torznab: tz, Spec: swagger.Spec(), DocsUI: swagger.UI(), Logger: log},
		server.Config{Addr: listenAddr(cfg), BasePath: cfg.Server.BaseURL})

	// The session + search-cache reapers write to the DB on shutdown (the cache flushes
	// its buffered touches and stat counters). Bind them to a context we can cancel and
	// JOIN before the deferred db.Close() runs — otherwise that final flush races (or is
	// lost to) the closing DB on every shutdown. bgCancel also unblocks the reapers if
	// srv.Run returns on a listen error (ctx not yet cancelled), so bg.Wait can't hang.
	bgCtx, bgCancel := context.WithCancel(ctx)
	var bg sync.WaitGroup
	defer func() {
		bgCancel()
		bg.Wait()
	}()

	startSessionCleanup(bgCtx, &bg, store, log)
	startSearchCacheCleanup(bgCtx, &bg, searchCache, log)
	startIndexerStatsFlush(bgCtx, &bg, reg)

	// Confirm the port is actually bindable before logging "listening": srv.Run binds
	// asynchronously, so a fatal listen error (e.g. address in use) would otherwise
	// surface only after we'd already told the operator the server was up.
	if err := preflightBind(ctx, listenAddr(cfg)); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	logStartup(log, cfg, keyring)
	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// preflightBind verifies the resolved address can be bound, then releases it so
// srv.Run can re-bind the same addr. This narrow window is acceptable for
// single-user self-hosted use; the point is to fail loud on an in-use port instead
// of falsely logging that the server is listening.
func preflightBind(ctx context.Context, addr string) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	if err := ln.Close(); err != nil {
		return fmt.Errorf("release preflight listener %s: %w", addr, err)
	}
	return nil
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

// sessionManager builds the SCS session manager with the family cookie hardening.
func sessionManager(store *database.SessionStore, cfg *config.Config) *scs.SessionManager {
	sm := scs.New()
	sm.Store = store
	sm.Lifetime = 30 * 24 * time.Hour
	sm.Cookie.Name = "harbrr_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Persist = false
	sm.Cookie.Secure = cfg.Server.SecureCookie
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

// apiKeyValidator wires the Torznab apikey check to the auth service so any minted
// key (stored only as a hash) authorizes the feed.
func apiKeyValidator(authSvc *auth.Service) func(string) bool {
	return func(key string) bool {
		_, err := authSvc.ValidateAPIKey(context.Background(), key)
		return err == nil
	}
}

// dropinDir is the on-disk drop-in definitions directory under the data dir.
func dropinDir(cfg *config.Config) string {
	return filepath.Join(cfg.DataDir, definitions.DropInDir)
}

// listenAddr is the host:port the server binds.
func listenAddr(cfg *config.Config) string {
	return net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))
}

// startSessionCleanup reaps expired sessions hourly until ctx is cancelled. It joins
// wg so serve() can wait for an in-flight reap to finish before closing the DB.
func startSessionCleanup(ctx context.Context, wg *sync.WaitGroup, store *database.SessionStore, log zerolog.Logger) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := store.DeleteExpired(ctx); err != nil && !errors.Is(err, context.Canceled) {
					log.Warn().Err(err).Msg("session cleanup failed")
				}
			}
		}
	}()
}

// buildSearchCache constructs the registry-wide search-results cache. It is ALWAYS
// installed (so cache.enabled is a runtime toggle the decorator self-gates on, not
// a boot-time wiring decision); the tuning is seeded from the config file, then any
// persisted app_settings overrides are overlaid. An overrides-load failure is
// logged and non-fatal (the config-file seed stands).
func buildSearchCache(ctx context.Context, db *database.DB, cfg *config.Config, log zerolog.Logger) *registry.SearchCache {
	sc := registry.NewSearchCacheWithParams(db, registry.SearchCacheParams{
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

// startSearchCacheCleanup reaps expired cache entries until ctx is cancelled,
// mirroring startSessionCleanup. The interval is re-read from the cache's live config
// each cycle (via sc.CleanupInterval), so a runtime cleanup_interval change applies
// without a restart — eventually, on the next cycle (a change made mid-cycle waits out
// the current timer rather than interrupting it). A failed purge is logged (redacted)
// and never fails anything. It joins wg so serve() waits for the shutdown flush (the
// FlushTouches/FlushCounters on ctx.Done()) to commit before the DB is closed.
func startSearchCacheCleanup(ctx context.Context, wg *sync.WaitGroup, sc *registry.SearchCache, log zerolog.Logger) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTimer(cleanupTickInterval(sc))
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				// Final flush of buffered hit bumps and stat counters on shutdown, with a
				// fresh bounded context since ctx is already canceled.
				fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				sc.FlushTouches(fctx)
				sc.FlushCounters(fctx)
				cancel()
				return
			case <-t.C:
				sc.FlushTouches(ctx)
				sc.FlushCounters(ctx)
				if _, err := sc.CleanupExpired(ctx); err != nil && !errors.Is(err, context.Canceled) {
					log.Warn().Err(err).Msg("search cache cleanup failed")
				}
				t.Reset(cleanupTickInterval(sc)) // pick up any runtime interval change
			}
		}
	}()
}

// indexerStatsFlushInterval is how often the per-indexer stat counters are flushed to
// the DB. A fixed 60s tick is fine: the counters are observability-only, so losing the
// increments since the last tick on a hard crash is acceptable (same tolerance the
// cache counters accept).
const indexerStatsFlushInterval = 60 * time.Second

// startIndexerStatsFlush periodically flushes the registry's durable per-indexer stat
// counters until ctx is cancelled, mirroring startSearchCacheCleanup. On ctx.Done() it
// runs a final flush with a fresh bounded context so the shutdown counters commit before
// the deferred db.Close() (bg.Wait() joins this goroutine first).
func startIndexerStatsFlush(ctx context.Context, wg *sync.WaitGroup, reg *registry.Registry) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(indexerStatsFlushInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				reg.FlushStats(fctx)
				cancel()
				return
			case <-t.C:
				reg.FlushStats(ctx)
			}
		}
	}()
}

// cleanupTickInterval reads the cache's live cleanup interval and keeps the reap loop
// from spinning: a non-positive value (unset) defaults to 1h, and a positive value
// below registry.MinCleanupInterval is floored to it. Config validation already
// enforces the same floor for API-set values; this also guards a config-file seed,
// which bypasses validation.
func cleanupTickInterval(sc *registry.SearchCache) time.Duration {
	d := sc.CleanupInterval()
	switch {
	case d <= 0:
		return time.Hour
	case d < registry.MinCleanupInterval:
		return registry.MinCleanupInterval
	default:
		return d
	}
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
