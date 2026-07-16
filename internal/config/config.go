// Package config holds harbrr's typed runtime configuration and the loader
// that assembles it from defaults, a config file, environment variables, and
// command-line flags. Structured config is always a typed struct — never a
// map[string]any (see AGENTS.md).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// redactedMask is the placeholder substituted for secret values when a Config
// is rendered for logging.
const redactedMask = "***"

// Config is harbrr's complete runtime configuration. It starts intentionally
// small and grows as consumers — the web server, database, and
// secrets store — are wired in.
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Log      LogConfig      `mapstructure:"log"`
	DataDir  string         `mapstructure:"data_dir"`
	Database DatabaseConfig `mapstructure:"database"`
	Secrets  SecretsConfig  `mapstructure:"secrets"`
	Auth     AuthConfig     `mapstructure:"auth"`
	Cache    CacheConfig    `mapstructure:"cache"`

	// ConfigFile is the config file Load actually read ("" when none was found),
	// surfaced in the startup log so operators know which file the port and
	// friends came from. Not itself a config key.
	ConfigFile string `mapstructure:"-"`
}

// CacheConfig tunes the search-results cache. Durations are Go duration strings
// (e.g. "5m", "1h") so they round-trip through viper/env/flags as plain strings,
// mirroring how per-instance "timeout"/"cache_ttl" settings are modeled. When
// Enabled is false, the cache is wired off entirely (zero behavior change).
type CacheConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// RSSTTL is the TTL for an empty/RSS poll; KeywordTTL for a real search.
	RSSTTL     string `mapstructure:"rss_ttl"`
	KeywordTTL string `mapstructure:"keyword_ttl"`
	// ThinTTL is the short clamp for a search returning <= ThinThreshold results.
	ThinTTL       string `mapstructure:"thin_ttl"`
	ThinThreshold int    `mapstructure:"thin_threshold"`
	// RefreshAheadPct is the percentage of a TTL after which a live hit fires one
	// background refresh (stale-while-revalidate).
	RefreshAheadPct int `mapstructure:"refresh_ahead_pct"`
	// CleanupInterval is how often expired entries are reaped.
	CleanupInterval string `mapstructure:"cleanup_interval"`
	// NegativeTTL is the negative-result circuit-breaker window: after a live search
	// to a tracker fails, further misses for that tracker short-circuit to the recorded
	// error for this long instead of re-driving it (kind-to-trackers anti-thundering-
	// herd). "0s" disables the breaker.
	NegativeTTL string `mapstructure:"negative_ttl"`
}

// ServerConfig describes the HTTP listener and reverse-proxy posture.
type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
	// BaseURL serves harbrr under a subpath (e.g. "/harbrr"); empty serves at root.
	BaseURL string `mapstructure:"base_url"`
	// SecureCookie marks the session cookie Secure. Set it when harbrr is reached
	// over HTTPS (typically a TLS-terminating reverse proxy). ExternalURL's https
	// scheme also implies Secure — see ExternalHTTPS.
	SecureCookie bool `mapstructure:"secure_cookie"`
	// ExternalURL is the operator-configured externally-visible "scheme://host[/base]"
	// harbrr is reached at (e.g. "https://harbrr.example.com"), typically behind a
	// TLS-terminating reverse proxy. When set it is authoritative for every absolute
	// link harbrr serves (feed self-URLs, /dl); empty keeps today's request-derived
	// behavior. A path, if present, must equal BaseURL (validateExternalURL).
	ExternalURL string `mapstructure:"external_url"`
}

// ExternalHTTPS reports whether ExternalURL is set and its scheme is https, so the
// session cookie can be marked Secure automatically without a manual secure_cookie
// override. Assumes ExternalURL has already passed Validate.
func (s ServerConfig) ExternalHTTPS() bool {
	u, err := url.Parse(s.ExternalURL)
	return err == nil && u.Scheme == "https"
}

// ExternalOrigin returns ExternalURL's "scheme://host" prefix (no path), or "" when
// ExternalURL is unset or fails to parse. This is the origin the absolute-URL
// builders (torznabhttp.URLConfig.ExternalOrigin) prepend to the base path.
func (s ServerConfig) ExternalOrigin() string {
	u, err := url.Parse(s.ExternalURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// AuthConfig is the management-API authentication posture.
type AuthConfig struct {
	// Mode is "required" (default) or "disabled". Disabled serves a synthetic admin
	// to allowlisted IPs (behind an authenticating reverse proxy) and REQUIRES a
	// non-empty IPAllowlist.
	Mode string `mapstructure:"mode"`
	// TrustedProxies are peers whose X-Forwarded-For is honored for the allowlist.
	TrustedProxies []string `mapstructure:"trusted_proxies"`
	// IPAllowlist is the set of IPs/CIDRs permitted in disabled mode.
	IPAllowlist []string `mapstructure:"ip_allowlist"`
}

// AuthDisabled reports whether auth is disabled (trusted-proxy mode).
func (c AuthConfig) AuthDisabled() bool { return c.Mode == authModeDisabled }

const (
	authModeRequired = "required"
	authModeDisabled = "disabled"
)

// LogConfig drives the zerolog logger built in internal/logger.
type LogConfig struct {
	Level  string `mapstructure:"level"`  // trace|debug|info|warn|error
	Format string `mapstructure:"format"` // console|json
}

// DatabaseConfig points at the SQLite database file. Postgres is deliberately
// not modeled yet (see AGENTS.md / dbinterface).
type DatabaseConfig struct {
	Path string `mapstructure:"path"`
}

// SecretsConfig selects the at-rest encryption key source. At most one of
// EncryptionKey (inline/env) or KeyFile (path) may be set. If neither is set,
// harbrr auto-generates a keyfile (encryption is always on) unless AllowPlaintext
// is set, which opts into UNENCRYPTED storage and fails closed otherwise.
type SecretsConfig struct {
	EncryptionKey  string `mapstructure:"encryption_key"`
	KeyFile        string `mapstructure:"key_file"`
	AllowPlaintext bool   `mapstructure:"allow_plaintext"`
}

// validLogLevels and validLogFormats are the accepted enum values for LogConfig.
var (
	validLogLevels  = map[string]struct{}{"trace": {}, "debug": {}, "info": {}, "warn": {}, "error": {}}
	validLogFormats = map[string]struct{}{"console": {}, "json": {}}
)

// ValidLogLevel reports whether level is one of the accepted log.level enum values.
// It is the single source of truth shared by config validation and the runtime
// log-level API, so the two can never accept different sets.
func ValidLogLevel(level string) bool {
	_, ok := validLogLevels[level]
	return ok
}

// Defaults returns the baseline configuration before any file, environment, or
// flag overrides are applied.
func Defaults() Config {
	return Config{
		Server: ServerConfig{
			Host: "127.0.0.1",
			Port: 7478,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "console",
		},
		DataDir:  "./data",
		Database: DatabaseConfig{Path: ""}, // derived under DataDir when empty
		Secrets:  SecretsConfig{},
		Auth:     AuthConfig{Mode: authModeRequired},
		Cache: CacheConfig{
			Enabled:         true,
			RSSTTL:          "5m",
			KeywordTTL:      "30m",
			ThinTTL:         "2m",
			ThinThreshold:   5,
			RefreshAheadPct: 80,
			CleanupInterval: "1h",
			NegativeTTL:     "1m",
		},
	}
}

// cacheDurationDefaults backs each CacheConfig duration field when its string is
// empty or unparseable, so a partial override never zeroes a TTL.
var cacheDurationDefaults = struct {
	rss, keyword, thin, cleanup, negative time.Duration
}{
	rss:      5 * time.Minute,
	keyword:  30 * time.Minute,
	thin:     2 * time.Minute,
	cleanup:  time.Hour,
	negative: time.Minute,
}

// parseDurationOr parses a Go duration string, falling back to def when empty,
// invalid, or non-positive.
func parseDurationOr(s string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return def
}

// RSSDuration is the resolved RSS-poll TTL.
func (c CacheConfig) RSSDuration() time.Duration {
	return parseDurationOr(c.RSSTTL, cacheDurationDefaults.rss)
}

// KeywordDuration is the resolved keyword-search TTL.
func (c CacheConfig) KeywordDuration() time.Duration {
	return parseDurationOr(c.KeywordTTL, cacheDurationDefaults.keyword)
}

// ThinDuration is the resolved thin-result clamp TTL.
func (c CacheConfig) ThinDuration() time.Duration {
	return parseDurationOr(c.ThinTTL, cacheDurationDefaults.thin)
}

// CleanupDuration is the resolved expired-entry reap interval.
func (c CacheConfig) CleanupDuration() time.Duration {
	return parseDurationOr(c.CleanupInterval, cacheDurationDefaults.cleanup)
}

// NegativeDuration is the resolved negative-result circuit-breaker window. Unlike the
// other TTLs it admits an explicit "0s" (breaker disabled); only an empty or malformed
// value falls back to the default. A negative duration is treated as disabled (0).
func (c CacheConfig) NegativeDuration() time.Duration {
	if d, err := time.ParseDuration(c.NegativeTTL); err == nil {
		if d < 0 {
			return 0
		}
		return d
	}
	return cacheDurationDefaults.negative
}

// DatabasePath returns the configured SQLite path, defaulting to
// "<DataDir>/harbrr.db" when unset.
func (c Config) DatabasePath() string {
	if c.Database.Path != "" {
		return c.Database.Path
	}
	return filepath.Join(c.DataDir, "harbrr.db")
}

// Validate checks the configuration for self-consistency, returning a wrapped
// error describing the first problem found.
func (c Config) Validate() error {
	if _, ok := validLogLevels[c.Log.Level]; !ok {
		return fmt.Errorf("config: invalid log.level %q", c.Log.Level)
	}
	if _, ok := validLogFormats[c.Log.Format]; !ok {
		return fmt.Errorf("config: invalid log.format %q", c.Log.Format)
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("config: server.port %d out of range 1-65535", c.Server.Port)
	}
	if c.DataDir == "" {
		return errors.New("config: data_dir must not be empty")
	}
	if c.Secrets.EncryptionKey != "" && c.Secrets.KeyFile != "" {
		return errors.New("config: set only one of secrets.encryption_key or secrets.key_file")
	}
	if err := c.validateAuth(); err != nil {
		return err
	}
	if err := c.validateBaseURL(); err != nil {
		return err
	}
	return c.validateExternalURL()
}

// validateAuth checks the auth mode and the fail-closed allowlist requirement.
func (c Config) validateAuth() error {
	switch c.Auth.Mode {
	case "", authModeRequired, authModeDisabled:
	default:
		return fmt.Errorf("config: invalid auth.mode %q (want %q or %q)", c.Auth.Mode, authModeRequired, authModeDisabled)
	}
	if c.Auth.AuthDisabled() && len(c.Auth.IPAllowlist) == 0 {
		return errors.New("config: auth.mode=disabled requires a non-empty auth.ip_allowlist (refusing to serve an open instance)")
	}
	return nil
}

// validateBaseURL requires a leading slash and forbids a trailing one, so it can
// be stripped from request paths cleanly. "/" by itself is rejected (it is a
// trailing slash, and equivalent to the empty default); "/harbrr" is valid.
func (c Config) validateBaseURL() error {
	b := c.Server.BaseURL
	if b == "" {
		return nil
	}
	if !strings.HasPrefix(b, "/") {
		return fmt.Errorf("config: server.base_url %q must start with '/'", b)
	}
	if strings.HasSuffix(b, "/") {
		return fmt.Errorf("config: server.base_url %q must not end with '/'", b)
	}
	return nil
}

// validateExternalURL requires an absolute http(s) URL with a host; empty is allowed
// (keeps today's request-derived behavior). A path, if present, must equal
// server.base_url exactly — ExternalOrigin strips it, so a mismatch would silently
// serve links at the wrong subpath.
func (c Config) validateExternalURL() error {
	e := c.Server.ExternalURL
	if e == "" {
		return nil
	}
	u, err := url.Parse(e)
	if err != nil {
		return fmt.Errorf("config: server.external_url %q: %w", e, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("config: server.external_url %q must be an absolute http:// or https:// URL", e)
	}
	if u.Host == "" {
		return fmt.Errorf("config: server.external_url %q must include a host", e)
	}
	if p := strings.TrimRight(u.Path, "/"); p != "" && p != c.Server.BaseURL {
		return fmt.Errorf("config: server.external_url %q path %q must equal server.base_url %q", e, u.Path, c.Server.BaseURL)
	}
	return nil
}

// Redacted returns a copy of the configuration with secret values masked, safe
// for logging. This is the redaction guarantee for the config surface.
func (c Config) Redacted() Config {
	redacted := c
	if redacted.Secrets.EncryptionKey != "" {
		redacted.Secrets.EncryptionKey = redactedMask
	}
	if redacted.Secrets.KeyFile != "" {
		redacted.Secrets.KeyFile = redactedMask
	}
	return redacted
}

// String renders the configuration with secrets redacted, so a Config is safe
// to interpolate into log lines.
func (c Config) String() string {
	r := c.Redacted()
	return fmt.Sprintf(
		"Config{server=%s:%d log=%s/%s data_dir=%s db=%s secrets.encryption_key=%q secrets.key_file=%q}",
		r.Server.Host, r.Server.Port, r.Log.Level, r.Log.Format,
		r.DataDir, r.DatabasePath(), r.Secrets.EncryptionKey, r.Secrets.KeyFile,
	)
}
