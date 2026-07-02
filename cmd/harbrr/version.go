package main

import (
	"github.com/spf13/cobra"

	"github.com/autobrr/harbrr/internal/version"
)

// newVersionCmd prints the build version string. The root command also exposes
// --version; this subcommand is the explicit, scriptable form.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the harbrr version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println(version.String())
			return nil
		},
	}
}
