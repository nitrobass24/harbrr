package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/autobrr/harbrr/internal/app"
	"github.com/autobrr/harbrr/internal/config"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/logger"
)

// canaryBlobKey / canaryIDKey are the app_meta keys for the startup secrets canary.
//
// This is a deliberate small duplicate of internal/app's copy: rotate-key (below)
// needs both these consts and openDatabase, and rewiring rotate-key onto
// internal/app is out of scope for the #144 composition-root extraction (see the
// PR notes) — a follow-up can fold rotate-key's DB/canary access onto the shared
// copy once its own wiring is revisited.
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
	// Materialize <data-dir>/config.toml on first run (never overwriting an
	// edited one), so the port and friends have an obvious editable home
	// beside the database. An explicit --config path opts out.
	if cfgFile == "" {
		if _, err := config.EnsureConfigFile(cmd.Flags()); err != nil {
			return fmt.Errorf("ensure config file: %w", err)
		}
	}
	cfg, err := config.Load(cfgFile, cmd.Flags())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log := logger.New(cfg.Log, cmd.OutOrStdout())
	// Seed the process-global level from config; a persisted DB override (if any) is
	// applied later, once the database is open (see internal/app.New).
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

	a, err := app.New(ctx, app.Deps{Config: cfg, Logger: log})
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	if err := a.Run(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// openDatabase opens and migrates the SQLite database. Kept here (duplicating
// internal/app's copy) only because rotate-key needs it — see the canary
// consts' comment above.
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
