package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/pflag"
	"go.yaml.in/yaml/v3"
)

// envPrefix is prepended to every derived environment variable name:
// server.port -> HARBRR_SERVER_PORT.
const envPrefix = "HARBRR_"

// flagToKey maps user-facing flag names to their dotted config keys, so flag
// UX (--log-level) stays friendly while the config tree stays structured.
var flagToKey = map[string]string{
	"host":       "server.host",
	"port":       "server.port",
	"base-url":   "server.base_url",
	"log-level":  "log.level",
	"log-format": "log.format",
	"data-dir":   "data_dir",
	"db-path":    "database.path",
}

// Load assembles a Config from defaults, an optional config file (by default
// <data-dir>/config.toml, created by serve on first run), environment variables
// (HARBRR_-prefixed), and command-line flags, with precedence
// flag > env > file > default. flags may be nil (e.g. in tests).
func Load(cfgFile string, flags *pflag.FlagSet) (*Config, error) {
	cfg := Defaults()

	// The default config lives beside the database: <data-dir>/config.toml.
	// The data dir is resolved from flag/env/default before the file is read,
	// so the file itself cannot relocate the data dir it lives in.
	if cfgFile == "" {
		cfgFile = filepath.Join(resolveDataDir(flags), ConfigFileName)
		if _, err := os.Stat(cfgFile); errors.Is(err, os.ErrNotExist) {
			cfgFile = "" // no file on the default path is fine
		}
	}
	if cfgFile != "" {
		if err := readConfigFile(&cfg, cfgFile); err != nil {
			return nil, err
		}
	}
	if err := applyEnv(&cfg); err != nil {
		return nil, err
	}
	if err := applyFlags(&cfg, flags); err != nil {
		return nil, err
	}

	cfg.ConfigFile = cfgFile
	if err := applyOIDCClientSecretFile(&cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// oidcClientSecretFileEnv is the docker-secrets-style env var: when set (and
// auth.oidc.client_secret is not already set some other way), its content is
// read as the client secret. This keeps the secret out of the environment/
// config file for a container deployment that mounts a secrets file instead.
const oidcClientSecretFileEnv = "HARBRR_AUTH_OIDC_CLIENT_SECRET_FILE" //nolint:gosec // G101: an env var NAME, not a credential.

// applyOIDCClientSecretFile resolves oidcClientSecretFileEnv, if set, into
// cfg.Auth.OIDC.ClientSecret. A file path that fails to read is a hard error
// (the operator explicitly asked for it) rather than a silent fallback to an
// unset secret.
func applyOIDCClientSecretFile(cfg *Config) error {
	if cfg.Auth.OIDC.ClientSecret != "" {
		return nil
	}
	path := strings.TrimSpace(os.Getenv(oidcClientSecretFileEnv))
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-configured via env var.
	if err != nil {
		return fmt.Errorf("config: read %s: %w", oidcClientSecretFileEnv, err)
	}
	cfg.Auth.OIDC.ClientSecret = strings.TrimSpace(string(data))
	return nil
}

// resolveDataDir applies the flag > env > default precedence to data_dir alone,
// for the two callers that need the directory before (or without) a config
// file: Load's default-file discovery and EnsureConfigFile.
func resolveDataDir(flags *pflag.FlagSet) string {
	if flags != nil {
		if f := flags.Lookup("data-dir"); f != nil && f.Changed {
			return f.Value.String()
		}
	}
	if v := os.Getenv(envPrefix + "DATA_DIR"); v != "" {
		return v
	}
	return Defaults().DataDir
}

// readConfigFile decodes path over cfg (absent keys keep their current value).
// The extension picks the format; an unknown key is an error rather than a
// silent no-op, so a typo cannot masquerade as a default.
func readConfigFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: read file: %w", err)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		err = toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(cfg)
		if strict, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			err = errors.New(strict.String())
		}
	case ".yaml", ".yml":
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		err = dec.Decode(cfg)
	default:
		return fmt.Errorf("config: %s: unsupported format (want .toml, .yaml or .yml)", path)
	}
	if err != nil {
		return fmt.Errorf("config: parse %s: %w", path, err)
	}
	return nil
}

// applyEnv overlays HARBRR_* environment variables onto cfg. Every scalar key
// is derived from the struct tags (auth.oidc.client_id -> HARBRR_AUTH_OIDC_
// CLIENT_ID), so a new field is env-settable the moment it is added. Lists are
// file-only. A set-but-empty variable is ignored, so a blank line in a compose
// file does not wipe a file value.
func applyEnv(cfg *Config) error {
	for key, field := range scalarKeys(reflect.ValueOf(cfg).Elem(), "") {
		name := envPrefix + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
		val := os.Getenv(name)
		if val == "" {
			continue
		}
		if err := setFromString(field, val); err != nil {
			return fmt.Errorf("config: %s: %w", name, err)
		}
	}
	return nil
}

// applyFlags overlays explicitly set command-line flags onto cfg. A flag left
// at its default is skipped so it cannot shadow an env or file value.
func applyFlags(cfg *Config, flags *pflag.FlagSet) error {
	if flags == nil {
		return nil
	}
	keys := scalarKeys(reflect.ValueOf(cfg).Elem(), "")
	for name, key := range flagToKey {
		f := flags.Lookup(name)
		if f == nil || !f.Changed {
			continue
		}
		if err := setFromString(keys[key], f.Value.String()); err != nil {
			return fmt.Errorf("config: flag --%s: %w", name, err)
		}
	}
	return nil
}

// scalarKeys walks v's `toml` tags and returns every settable scalar field by
// its dotted key. Nested structs recurse; slices and untagged fields are skipped.
func scalarKeys(v reflect.Value, prefix string) map[string]reflect.Value {
	out := map[string]reflect.Value{}
	t := v.Type()
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("toml")
		if tag == "" || tag == "-" {
			continue
		}
		field := v.Field(i)
		switch field.Kind() { //nolint:exhaustive // every other kind is intentionally not env-settable.
		case reflect.Struct:
			maps.Copy(out, scalarKeys(field, prefix+tag+"."))
		case reflect.String, reflect.Int, reflect.Bool:
			out[prefix+tag] = field
		}
	}
	return out
}

func setFromString(field reflect.Value, val string) error {
	switch field.Kind() { //nolint:exhaustive // scalarKeys only yields string, int and bool.
	case reflect.Int:
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("want an integer, got %q", val)
		}
		field.SetInt(int64(n))
	case reflect.Bool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("want true or false, got %q", val)
		}
		field.SetBool(b)
	default:
		field.SetString(val)
	}
	return nil
}
