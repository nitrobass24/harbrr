package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/pflag"

	"github.com/autobrr/harbrr/internal/config"
)

// dataDirFlags returns a test flag set with data-dir pointed at dir, so
// EnsureConfigFile/Load resolve the same directory a real serve run would.
func dataDirFlags(t *testing.T, dir string) *pflag.FlagSet {
	t.Helper()
	fs := newTestFlags()
	if err := fs.Set("data-dir", dir); err != nil {
		t.Fatalf("set data-dir: %v", err)
	}
	return fs
}

func TestEnsureConfigFileCreatesTemplate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	fs := dataDirFlags(t, dir)

	path, err := config.EnsureConfigFile(fs)
	if err != nil {
		t.Fatalf("EnsureConfigFile: %v", err)
	}
	if want := filepath.Join(dir, config.ConfigFileName); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	// Windows does not expose POSIX permission bits through os.FileMode.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat created file: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("file mode = %o, want 0600", perm)
		}
		dirInfo, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat created dir: %v", err)
		}
		if perm := dirInfo.Mode().Perm(); perm != 0o700 {
			t.Errorf("data dir mode = %o, want 0700", perm)
		}
	}
}

func TestEnsureConfigFileNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	fs := dataDirFlags(t, dir)

	edited := "[server]\nport = 9999\n"
	path := filepath.Join(dir, config.ConfigFileName)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatalf("seed edited config: %v", err)
	}

	got, err := config.EnsureConfigFile(fs)
	if err != nil {
		t.Fatalf("EnsureConfigFile: %v", err)
	}
	if got != path {
		t.Errorf("path = %q, want %q", got, path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(body) != edited {
		t.Error("EnsureConfigFile overwrote an existing (edited) config file")
	}
}

// TestEnsureConfigFileMatchesDefaults pins the template against Defaults():
// loading a freshly generated config.toml must produce exactly the defaults,
// so a default change that forgets the template fails here.
func TestEnsureConfigFileMatchesDefaults(t *testing.T) {
	dir := t.TempDir()
	fs := dataDirFlags(t, dir)

	if _, err := config.EnsureConfigFile(fs); err != nil {
		t.Fatalf("EnsureConfigFile: %v", err)
	}
	cfg, err := config.Load("", fs)
	if err != nil {
		t.Fatalf("Load with generated template: %v", err)
	}

	d := config.Defaults()
	if cfg.Server.Host != d.Server.Host || cfg.Server.Port != d.Server.Port {
		t.Errorf("server = %+v, want defaults %+v", cfg.Server, d.Server)
	}
	if cfg.Log.Level != d.Log.Level || cfg.Log.Format != d.Log.Format {
		t.Errorf("log = %+v, want defaults %+v", cfg.Log, d.Log)
	}
	if cfg.Auth.Mode != d.Auth.Mode {
		t.Errorf("auth.mode = %q, want default %q", cfg.Auth.Mode, d.Auth.Mode)
	}
	if cfg.ConfigFile != filepath.Join(dir, config.ConfigFileName) {
		t.Errorf("ConfigFile = %q, want the generated path", cfg.ConfigFile)
	}
}

// TestLoadReadsPortFromDataDirConfig is the issue-#81 operator flow: edit the
// port in <data-dir>/config.toml, restart, and the server listens there.
func TestLoadReadsPortFromDataDirConfig(t *testing.T) {
	dir := t.TempDir()
	fs := dataDirFlags(t, dir)

	body := "[server]\nport = 9117\n"
	if err := os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	cfg, err := config.Load("", fs)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 9117 {
		t.Errorf("port = %d, want 9117 from the data-dir config.toml", cfg.Server.Port)
	}
}
