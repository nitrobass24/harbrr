package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// flagToKey maps user-facing flag names to their dotted viper keys, so flag
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

// Load assembles a Config from defaults, an optional YAML config file,
// environment variables (HARBRR_-prefixed), and command-line flags, with
// precedence flag > env > file > default. flags may be nil (e.g. in tests).
func Load(cfgFile string, flags *pflag.FlagSet) (*Config, error) {
	v := viper.New()
	setDefaults(v)

	v.SetEnvPrefix("HARBRR")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := bindFlags(v, flags); err != nil {
		return nil, err
	}
	if err := readConfigFile(v, cfgFile); err != nil {
		return nil, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	d := Defaults()
	v.SetDefault("server.host", d.Server.Host)
	v.SetDefault("server.port", d.Server.Port)
	v.SetDefault("log.level", d.Log.Level)
	v.SetDefault("log.format", d.Log.Format)
	v.SetDefault("data_dir", d.DataDir)
	v.SetDefault("database.path", d.Database.Path)
	v.SetDefault("server.base_url", d.Server.BaseURL)
	v.SetDefault("auth.mode", d.Auth.Mode)
	// Registering these keys lets AutomaticEnv resolve them through Unmarshal
	// (viper only binds env for known keys). The list-valued auth.ip_allowlist /
	// auth.trusted_proxies are set via the config file.
	v.SetDefault("server.secure_cookie", d.Server.SecureCookie)
	v.SetDefault("secrets.allow_plaintext", d.Secrets.AllowPlaintext)
	v.SetDefault("cache.enabled", d.Cache.Enabled)
	v.SetDefault("cache.rss_ttl", d.Cache.RSSTTL)
	v.SetDefault("cache.keyword_ttl", d.Cache.KeywordTTL)
	v.SetDefault("cache.thin_ttl", d.Cache.ThinTTL)
	v.SetDefault("cache.thin_threshold", d.Cache.ThinThreshold)
	v.SetDefault("cache.refresh_ahead_pct", d.Cache.RefreshAheadPct)
	v.SetDefault("cache.cleanup_interval", d.Cache.CleanupInterval)
	v.SetDefault("cache.negative_ttl", d.Cache.NegativeTTL)
}

func bindFlags(v *viper.Viper, flags *pflag.FlagSet) error {
	if flags == nil {
		return nil
	}
	for name, key := range flagToKey {
		f := flags.Lookup(name)
		if f == nil {
			continue
		}
		if err := v.BindPFlag(key, f); err != nil {
			return fmt.Errorf("config: bind flag %q: %w", name, err)
		}
	}
	return nil
}

func readConfigFile(v *viper.Viper, cfgFile string) error {
	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("harbrr")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("./data")
	}

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if cfgFile == "" && errors.As(err, &notFound) {
			return nil // no file on the default search path is fine
		}
		return fmt.Errorf("config: read file: %w", err)
	}
	return nil
}
