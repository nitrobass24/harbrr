package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/autobrr/harbrr/internal/config"
)

// newTestFlags mirrors the persistent flags registered by cmd/harbrr so the
// loader's flag binding can be exercised offline.
func newTestFlags() *pflag.FlagSet {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	d := config.Defaults()
	fs.String("host", d.Server.Host, "")
	fs.Int("port", d.Server.Port, "")
	fs.String("log-level", d.Log.Level, "")
	fs.String("log-format", d.Log.Format, "")
	fs.String("data-dir", d.DataDir, "")
	fs.String("db-path", d.Database.Path, "")
	return fs
}

func TestDefaults(t *testing.T) {
	t.Parallel()

	d := config.Defaults()
	if d.Server.Host != "127.0.0.1" || d.Server.Port != 7474 {
		t.Errorf("unexpected server defaults: %+v", d.Server)
	}
	if d.Log.Level != "info" || d.Log.Format != "console" {
		t.Errorf("unexpected log defaults: %+v", d.Log)
	}
	if got, want := d.DatabasePath(), filepath.Join("./data", "harbrr.db"); got != want {
		t.Errorf("DatabasePath() = %q, want %q", got, want)
	}
	if d.HasSecretKey() {
		t.Error("HasSecretKey() = true for empty secrets, want false")
	}
	if err := d.Validate(); err != nil {
		t.Errorf("Defaults().Validate() = %v, want nil", err)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*config.Config)
		wantErr bool
	}{
		{"defaults ok", func(*config.Config) {}, false},
		{"bad level", func(c *config.Config) { c.Log.Level = "loud" }, true},
		{"bad format", func(c *config.Config) { c.Log.Format = "xml" }, true},
		{"port too low", func(c *config.Config) { c.Server.Port = 0 }, true},
		{"port too high", func(c *config.Config) { c.Server.Port = 70000 }, true},
		{"empty data dir", func(c *config.Config) { c.DataDir = "" }, true},
		{"dual secret sources", func(c *config.Config) {
			c.Secrets.EncryptionKey = "k"
			c.Secrets.KeyFile = "/tmp/key"
		}, true},
		{"single secret source ok", func(c *config.Config) { c.Secrets.EncryptionKey = "k" }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Defaults()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestRedacted(t *testing.T) {
	t.Parallel()

	cfg := config.Defaults()
	cfg.Secrets.EncryptionKey = "super-secret-key"

	r := cfg.Redacted()
	if r.Secrets.EncryptionKey == "super-secret-key" {
		t.Error("Redacted() did not mask encryption_key")
	}
	if strings.Contains(cfg.String(), "super-secret-key") {
		t.Errorf("String() leaked secret: %q", cfg.String())
	}
	if cfg.Secrets.EncryptionKey != "super-secret-key" {
		t.Error("Redacted() mutated the original config")
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load("", nil)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Server.Port != config.Defaults().Server.Port {
		t.Errorf("Load() port = %d, want default %d", cfg.Server.Port, config.Defaults().Server.Port)
	}
}

func TestLoadEnvOverride(t *testing.T) {
	t.Setenv("HARBRR_SERVER_PORT", "9999")
	t.Setenv("HARBRR_LOG_LEVEL", "debug")

	cfg, err := config.Load("", nil)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Server.Port != 9999 {
		t.Errorf("env override port = %d, want 9999", cfg.Server.Port)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("env override log.level = %q, want debug", cfg.Log.Level)
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harbrr.yaml")
	body := "server:\n  port: 8123\nlog:\n  level: warn\ndata_dir: /var/lib/harbrr\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(path, nil)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Server.Port != 8123 {
		t.Errorf("file port = %d, want 8123", cfg.Server.Port)
	}
	if cfg.Log.Level != "warn" {
		t.Errorf("file log.level = %q, want warn", cfg.Log.Level)
	}
	if cfg.DataDir != "/var/lib/harbrr" {
		t.Errorf("file data_dir = %q, want /var/lib/harbrr", cfg.DataDir)
	}
}

func TestLoadFilePhase4Fields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harbrr.yaml")
	body := "server:\n" +
		"  base_url: /harbrr\n" +
		"  secure_cookie: true\n" +
		"secrets:\n" +
		"  allow_plaintext: true\n" +
		"auth:\n" +
		"  mode: disabled\n" +
		"  ip_allowlist: [\"10.0.0.0/8\", \"127.0.0.1\"]\n" +
		"  trusted_proxies: [\"172.16.0.0/12\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(path, nil)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Server.BaseURL != "/harbrr" || !cfg.Server.SecureCookie {
		t.Errorf("server = %+v, want base_url=/harbrr secure_cookie=true", cfg.Server)
	}
	if !cfg.Secrets.AllowPlaintext {
		t.Error("secrets.allow_plaintext not loaded")
	}
	if !cfg.Auth.AuthDisabled() || len(cfg.Auth.IPAllowlist) != 2 || len(cfg.Auth.TrustedProxies) != 1 {
		t.Errorf("auth = %+v, want disabled + 2 allowlist + 1 proxy", cfg.Auth)
	}
}

func TestLoadFlagBeatsEnv(t *testing.T) {
	t.Setenv("HARBRR_SERVER_PORT", "9999")

	fs := newTestFlags()
	if err := fs.Set("port", "1234"); err != nil {
		t.Fatalf("set flag: %v", err)
	}

	cfg, err := config.Load("", fs)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Server.Port != 1234 {
		t.Errorf("flag should beat env: port = %d, want 1234", cfg.Server.Port)
	}
}

// TestExampleConfigIsValid keeps the shipped sample config loadable + valid, so it
// never drifts from the config struct.
func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := config.Load(filepath.Join("..", "..", "config.example.yaml"), nil)
	if err != nil {
		t.Fatalf("config.example.yaml failed to load/validate: %v", err)
	}
	if cfg.Server.Port != 7474 {
		t.Errorf("example server.port = %d, want 7474", cfg.Server.Port)
	}
	if cfg.Auth.Mode != "required" {
		t.Errorf("example auth.mode = %q, want required", cfg.Auth.Mode)
	}
}

func TestLoadExplicitMissingFileErrors(t *testing.T) {
	if _, err := config.Load(filepath.Join(t.TempDir(), "absent.yaml"), nil); err == nil {
		t.Fatal("Load() with missing explicit config file = nil, want error")
	}
}
