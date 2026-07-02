package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvFileRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "smoke.env")
	in := map[string]string{
		"SMOKE_HARBRR_URL":      "http://harbrr:7474",
		"SMOKE_HARBRR_APIKEY":   "key with spaces",
		"SMOKE_PROWLARR_URL":    "http://prowlarr:9696",
		"SMOKE_PROWLARR_APIKEY": `quote"inside`,
		"SMOKE_QUI_URL":         "http://qui:7476",
		"SMOKE_QUI_APIKEY":      "qk",
	}
	if err := writeEnvFile(path, in); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	// Written at 0600 so keys never sit world-readable.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}

	got, err := parseEnvFile(path)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	for k, v := range in {
		if got[k] != v {
			t.Errorf("round-trip %s = %q, want %q", k, got[k], v)
		}
	}
	// Keys not supplied are not written and so not present on read-back.
	if _, ok := got["SMOKE_SONARR_URL"]; ok {
		t.Errorf("unset key should not be written")
	}
}

func TestParseEnvFileMissing(t *testing.T) {
	t.Parallel()
	got, err := parseEnvFile(filepath.Join(t.TempDir(), "does-not-exist.env"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing file should yield an empty map, got %v", got)
	}
}

func TestParseEnvFileFormats(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "hand.env")
	content := "" +
		"# a comment\n" +
		"\n" +
		"export SMOKE_HARBRR_URL=http://harbrr:7474\n" +
		"SMOKE_HARBRR_APIKEY=\"quoted-key\"\n" +
		"export SMOKE_PROWLARR_URL='single-quoted'\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := parseEnvFile(path)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	want := map[string]string{
		"SMOKE_HARBRR_URL":    "http://harbrr:7474",
		"SMOKE_HARBRR_APIKEY": "quoted-key",
		"SMOKE_PROWLARR_URL":  "single-quoted",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestMissingRequired(t *testing.T) {
	t.Parallel()
	full := map[string]string{
		"SMOKE_HARBRR_URL":      "u",
		"SMOKE_HARBRR_APIKEY":   "k",
		"SMOKE_PROWLARR_URL":    "u",
		"SMOKE_PROWLARR_APIKEY": "k",
	}
	getenv := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	if missingRequired(getenv(full)) {
		t.Error("a full required set should not be missing")
	}
	partial := map[string]string{"SMOKE_HARBRR_URL": "u", "SMOKE_HARBRR_APIKEY": "k"}
	if !missingRequired(getenv(partial)) {
		t.Error("a partial set (no Prowlarr) should be missing")
	}
}
