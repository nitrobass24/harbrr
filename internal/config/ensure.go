package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/pflag"
)

// ConfigFileName is the config file harbrr reads from — and creates in — the
// data directory when --config doesn't name an explicit path.
const ConfigFileName = "config.toml"

// defaultTemplate is the config.toml written on first run. Its uncommented
// values must match Defaults() — TestEnsureConfigFileMatchesDefaults pins
// that, so a default change that forgets this template fails loudly.
const defaultTemplate = `# harbrr configuration — created on first run with the defaults.
# This file lives beside the database in the data directory. Precedence:
# command-line flags > HARBRR_* environment variables > this file.
# (data_dir itself is flag/env-only — it locates this file, so it can't be set here.)
# Changes take effect on restart.

[server]
host = "127.0.0.1"
port = 7478
# Serve under a subpath behind a reverse proxy, e.g. "/harbrr".
#base_url = ""
# Set true when a reverse proxy terminates TLS in front of harbrr.
#secure_cookie = false
# The externally-visible URL, e.g. "https://harbrr.example.com". When set it is
# authoritative for every absolute link harbrr serves and implies secure_cookie
# for an https scheme. See docs/reverse-proxy.md.
#external_url = ""

[log]
# trace | debug | info | warn | error
level = "info"
# console | json
#format = "console"

#[auth]
# "required" (default) or "disabled". Disabled serves a synthetic admin to
# allowlisted IPs (for use behind an authenticating reverse proxy) and
# REQUIRES a non-empty ip_allowlist.
#mode = "required"
#ip_allowlist = []
#trusted_proxies = []

#[auth.oidc]
# OpenID Connect / SSO login (coexists with the password login above; see
# docs). Requires issuer/client_id/client_secret/redirect_url when enabled.
#enabled = false
#issuer = ""
#client_id = ""
#client_secret = ""
#redirect_url = ""
#disable_built_in_login = false

#[secrets]
# Key for encrypting tracker credentials at rest; alternatively point key_file
# at a file holding it, or set HARBRR_SECRETS_ENCRYPTION_KEY. Without any of
# these, a keyfile is generated beside the database.
#encryption_key = ""
#key_file = ""
`

// EnsureConfigFile makes sure <data-dir>/config.toml exists, writing the
// commented default template on first run (never overwriting an existing
// file). The data dir is resolved from flags/env/defaults exactly as Load
// resolves it. Returns the file's path.
func EnsureConfigFile(flags *pflag.FlagSet) (string, error) {
	v, err := newViper(flags)
	if err != nil {
		return "", err
	}
	dir := v.GetString("data_dir")
	path := filepath.Join(dir, ConfigFileName)

	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("config: stat %s: %w", path, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("config: create data dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(defaultTemplate), 0o600); err != nil {
		return "", fmt.Errorf("config: write %s: %w", path, err)
	}
	return path, nil
}
