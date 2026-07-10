package main

import (
	"github.com/spf13/cobra"

	"github.com/autobrr/harbrr/internal/config"
	"github.com/autobrr/harbrr/internal/version"
)

// newRootCmd builds the harbrr command tree: a root carrying the persistent
// configuration flags, plus the serve and version subcommands.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "harbrr",
		Short:         "Cardigann-compatible Torznab/Newznab search provider for the autobrr family",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	d := config.Defaults()
	pf := root.PersistentFlags()
	pf.String("config", "", "path to a config file (default: <data-dir>/config.toml, created by serve on first run)")
	pf.String("host", d.Server.Host, "HTTP listen host")
	pf.Int("port", d.Server.Port, "HTTP listen port")
	pf.String("base-url", d.Server.BaseURL, "serve under a subpath (e.g. /harbrr); empty serves at root")
	pf.String("log-level", d.Log.Level, "log level (trace|debug|info|warn|error)")
	pf.String("log-format", d.Log.Format, "log format (console|json)")
	pf.String("data-dir", d.DataDir, "data directory")
	pf.String("db-path", d.Database.Path, "SQLite database path (default: <data-dir>/harbrr.db)")

	root.AddCommand(newServeCmd(), newVersionCmd(), newRotateKeyCmd(), newSmokeCmd())
	return root
}
