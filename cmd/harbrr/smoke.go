package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/autobrr/harbrr/internal/smoke"
)

// requiredSmokeEnv are the variables a smoke run cannot start without: the harbrr
// target and the Prowlarr differential oracle. The *arr/qui app targets are optional
// (an unset URL means "that app is not configured").
var requiredSmokeEnv = []string{
	"SMOKE_HARBRR_URL", "SMOKE_HARBRR_APIKEY",
	"SMOKE_PROWLARR_URL", "SMOKE_PROWLARR_APIKEY",
}

// smokeEnvTemplate is the paste-ready env file printed when a required variable is
// missing; the optional apps are commented out.
const smokeEnvTemplate = `# harbrr smoke config — keys are secret; do not commit. Write at mode 0600.
export SMOKE_HARBRR_URL=http://harbrr:7478
export SMOKE_HARBRR_APIKEY=
export SMOKE_PROWLARR_URL=http://prowlarr:9696
export SMOKE_PROWLARR_APIKEY=
#export SMOKE_SONARR_URL=
#export SMOKE_SONARR_APIKEY=
#export SMOKE_RADARR_URL=
#export SMOKE_RADARR_APIKEY=
#export SMOKE_QUI_URL=
#export SMOKE_QUI_APIKEY=
`

// smokeOptions are the resolved flag values for one smoke run.
type smokeOptions struct {
	envFile, reportPath, query, fallbackQuery string
}

// newSmokeCmd builds the `harbrr smoke` subcommand: the operator golden smoke test.
// It reads its config from ./smoke.env (or the process env), runs the parity +
// app-sync + cache suite against a live stack, and writes a secret-scrubbed markdown
// report. It reaches real trackers, so it refuses to run in CI.
func newSmokeCmd() *cobra.Command {
	var opt smokeOptions
	cmd := &cobra.Command{
		Use:   "smoke",
		Short: "Run the operator golden smoke test (parity + app-sync) against a live harbrr stack",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSmoke(cmd, opt)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opt.envFile, "env-file", "./smoke.env", "path to the smoke env file (export SMOKE_*=...)")
	f.StringVar(&opt.reportPath, "report", "./smoke-report.md", "path to write the markdown report")
	f.StringVar(&opt.query, "query", "", "search query (overrides SMOKE_QUERY)")
	f.StringVar(&opt.fallbackQuery, "fallback-query", "", "fallback search query (overrides SMOKE_QUERY_FALLBACK)")
	return cmd
}

// runSmoke orchestrates one smoke run: CI guard, env load, the suite, and the report.
func runSmoke(cmd *cobra.Command, opt smokeOptions) error {
	if os.Getenv("CI") != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "harbrr smoke reaches live trackers and the *arr/Prowlarr apps; it must not run in CI.")
		return errors.New("smoke: refusing to run in CI (CI env var is set)")
	}
	fileEnv, err := parseEnvFile(opt.envFile)
	if err != nil {
		return err
	}
	// Real process env takes precedence over the file (so `SMOKE_X=... harbrr smoke`
	// overrides a saved value), and the file backfills whatever the shell did not source.
	getenv := func(k string) string {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
		return fileEnv[k]
	}
	if missing := missingRequired(getenv); len(missing) > 0 {
		errOut := cmd.ErrOrStderr()
		fmt.Fprintf(errOut, "smoke: missing required env: %s\nWrite %s (or export the variables):\n\n%s\n", strings.Join(missing, ", "), opt.envFile, smokeEnvTemplate)
		return fmt.Errorf("smoke: missing required env: %s", strings.Join(missing, ", "))
	}

	cfg, err := smoke.ParseConfig(getenv)
	if err != nil {
		return fmt.Errorf("smoke: %w", err)
	}
	if opt.query != "" {
		cfg.Query = opt.query
	}
	if opt.fallbackQuery != "" {
		cfg.FallbackQuery = opt.fallbackQuery
	}

	rep, err := smoke.RunSuite(cmd.Context(), cfg)
	if err != nil {
		return fmt.Errorf("smoke: %w", err)
	}
	if err := os.WriteFile(opt.reportPath, []byte(rep.Markdown()), 0o600); err != nil {
		return fmt.Errorf("smoke: write report: %w", err)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, rep.Summary())
	fmt.Fprintf(out, "report written to %s\n", opt.reportPath)
	if rep.HasFailures() {
		return errors.New("smoke: one or more checks FAILED (see the report)")
	}
	return nil
}

// missingRequired returns the required SMOKE_* variables that are unset, in
// requiredSmokeEnv order; nil means the run can start.
func missingRequired(getenv func(string) string) []string {
	var missing []string
	for _, k := range requiredSmokeEnv {
		if strings.TrimSpace(getenv(k)) == "" {
			missing = append(missing, k)
		}
	}
	return missing
}
