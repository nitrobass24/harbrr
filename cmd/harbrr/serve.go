package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/autobrr/harbrr/internal/app"
	"github.com/autobrr/harbrr/internal/config"
	"github.com/autobrr/harbrr/internal/logger"
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
