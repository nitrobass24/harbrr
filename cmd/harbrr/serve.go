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
	"syscall"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/config"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/definitions"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/logger"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/server"
	"github.com/autobrr/harbrr/internal/web/api"
	"github.com/autobrr/harbrr/internal/web/swagger"
	"github.com/autobrr/harbrr/internal/web/torznab"
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
	log, err := logger.New(cfg.Log, cmd.OutOrStdout())
	if err != nil {
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
	reg := registry.New(db, loader.New(dropinDir(cfg)), keyring, registry.WithLogger(log))

	mgmt, err := api.NewRouter(api.Deps{
		Auth: authSvc, Registry: reg, Loader: loader.New(dropinDir(cfg)), Sessions: sessions,
		DLToken: keyring, BasePath: cfg.Server.BaseURL, Logger: log,
	}, api.Config{
		AuthDisabled: cfg.Auth.AuthDisabled(), IPAllowlist: cfg.Auth.IPAllowlist, TrustedProxies: cfg.Auth.TrustedProxies,
	})
	if err != nil {
		return fmt.Errorf("management api: %w", err)
	}

	tz := torznab.NewHandler(
		reg,
		torznab.WithAPIKeyValidator(apiKeyValidator(authSvc)),
		torznab.WithBasePath(cfg.Server.BaseURL),
		torznab.WithLogger(log),
		torznab.WithDLToken(keyring),
	)

	srv := server.New(server.Deps{Management: mgmt, Torznab: tz, Spec: swagger.Spec(), DocsUI: swagger.UI(), Logger: log},
		server.Config{Addr: listenAddr(cfg), BasePath: cfg.Server.BaseURL})

	startSessionCleanup(ctx, store, log)
	logStartup(log, cfg)
	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
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

// startSessionCleanup reaps expired sessions hourly until ctx is cancelled.
func startSessionCleanup(ctx context.Context, store *database.SessionStore, log zerolog.Logger) {
	go func() {
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

// logStartup logs the resolved listen/serving parameters.
func logStartup(log zerolog.Logger, cfg *config.Config) {
	log.Info().
		Str("addr", listenAddr(cfg)).
		Str("base_url", cfg.Server.BaseURL).
		Str("data_dir", cfg.DataDir).
		Bool("auth_disabled", cfg.Auth.AuthDisabled()).
		Msg("harbrr listening")
}
