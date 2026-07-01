package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/autobrr/harbrr/internal/smoke"
)

// appPrompt describes one app's interactive URL+key prompt pair.
type appPrompt struct {
	name           string
	urlKey, keyKey string
	required       bool
}

// smokePrompts is the fixed first-run prompt order: the required harbrr + Prowlarr
// targets, then the optional *arr/qui apps (a blank URL skips the app).
var smokePrompts = []appPrompt{
	{"harbrr", "SMOKE_HARBRR_URL", "SMOKE_HARBRR_APIKEY", true},
	{"Prowlarr", "SMOKE_PROWLARR_URL", "SMOKE_PROWLARR_APIKEY", true},
	{"Sonarr", "SMOKE_SONARR_URL", "SMOKE_SONARR_APIKEY", false},
	{"Radarr", "SMOKE_RADARR_URL", "SMOKE_RADARR_APIKEY", false},
	{"Qui", "SMOKE_QUI_URL", "SMOKE_QUI_APIKEY", false},
}

// smokeOptions are the resolved flag values for one smoke run.
type smokeOptions struct {
	envFile, reportPath, query, fallbackQuery string
	reconfigure                               bool
}

// newSmokeCmd builds the `harbrr smoke` subcommand: the operator golden smoke test.
// It reads its config from ./smoke.env (or the process env), prompts on first run,
// runs the parity + app-sync + cache suite against a live stack, and writes a
// secret-scrubbed markdown report. It reaches real trackers, so it refuses to run in CI.
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
	f.BoolVar(&opt.reconfigure, "reconfigure", false, "re-prompt for every app URL/key and rewrite the env file")
	f.StringVar(&opt.envFile, "env-file", "./smoke.env", "path to the smoke env file (export SMOKE_*=...)")
	f.StringVar(&opt.reportPath, "report", "./smoke-report.md", "path to write the markdown report")
	f.StringVar(&opt.query, "query", "", "search query (overrides SMOKE_QUERY)")
	f.StringVar(&opt.fallbackQuery, "fallback-query", "", "fallback search query (overrides SMOKE_QUERY_FALLBACK)")
	return cmd
}

// runSmoke orchestrates one smoke run: CI guard, env load, first-run/reconfigure
// prompting, the suite, and the report.
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
	if opt.reconfigure || missingRequired(getenv) {
		if err := runReconfigure(cmd, opt.envFile, fileEnv); err != nil {
			return err
		}
		if fileEnv, err = parseEnvFile(opt.envFile); err != nil {
			return err
		}
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

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	rep, err := smoke.RunSuite(ctx, cfg)
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

// missingRequired reports whether any required (harbrr/Prowlarr) URL or key is unset,
// which forces the first-run prompt.
func missingRequired(getenv func(string) string) bool {
	for _, p := range smokePrompts {
		if !p.required {
			continue
		}
		if strings.TrimSpace(getenv(p.urlKey)) == "" || strings.TrimSpace(getenv(p.keyKey)) == "" {
			return true
		}
	}
	return false
}

// clearURLSentinel, typed at an optional app's URL prompt during --reconfigure, clears a
// previously-saved value (a blank line keeps the shown default instead).
const clearURLSentinel = "-"

// runReconfigure prompts for every app's URL then key, one at a time in order, and
// writes the result to envFile at 0600. URLs echo; keys are read without echo. A blank
// optional-app URL marks that app not-configured (its key is not prompted).
func runReconfigure(cmd *cobra.Command, envFile string, existing map[string]string) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Configuring harbrr smoke. URLs are echoed; API keys are read without echo.")
	fmt.Fprintf(out, "Leave an optional app's URL blank to keep it (or skip if unset); enter %q to clear a saved one.\n", clearURLSentinel)
	in := bufio.NewReader(cmd.InOrStdin())
	values := map[string]string{}
	for _, p := range smokePrompts {
		u := promptLine(out, in, p.name+" URL", existing[p.urlKey])
		if !p.required && u == clearURLSentinel {
			fmt.Fprintf(out, "  %s cleared (not configured)\n", p.name)
			continue
		}
		if u == "" {
			if p.required {
				return fmt.Errorf("smoke: %s URL is required", p.name)
			}
			fmt.Fprintf(out, "  %s not configured (skipped)\n", p.name)
			continue
		}
		key, err := promptSecret(out, p.name+" API key")
		if err != nil {
			return err
		}
		if key == "" && p.required {
			return fmt.Errorf("smoke: %s API key is required", p.name)
		}
		values[p.urlKey] = strings.TrimRight(u, "/")
		values[p.keyKey] = key
	}
	if err := writeEnvFile(envFile, values); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s (mode 0600)\n", envFile)
	return nil
}

// promptLine writes a prompt (with any existing value as the default) and reads one
// echoed line, returning the default on empty input.
func promptLine(out io.Writer, in *bufio.Reader, label, def string) string {
	if def != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	line, _ := in.ReadString('\n')
	if line = strings.TrimSpace(line); line == "" {
		return def
	}
	return line
}

// promptSecret reads one line from the terminal without echoing it (so an API key is
// never displayed). Hiding the echo requires a real TTY fd, so it reads os.Stdin directly
// via x/term; when stdin is not a terminal (piped input, `docker exec` without -t) it
// returns a clear, actionable error instead of a raw ioctl failure.
func promptSecret(out io.Writer, label string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("smoke: reading %s needs an interactive terminal; pre-populate the env file or run with `docker exec -it`", label)
	}
	fmt.Fprintf(out, "%s (hidden): ", label)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("smoke: read %s: %w", label, err)
	}
	return strings.TrimSpace(string(b)), nil
}
