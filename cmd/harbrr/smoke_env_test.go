package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

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
		"export SMOKE_HARBRR_URL=http://harbrr:7478\n" +
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
		"SMOKE_HARBRR_URL":    "http://harbrr:7478",
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
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{
			name: "full required set",
			env: map[string]string{
				"SMOKE_HARBRR_URL": "u", "SMOKE_HARBRR_APIKEY": "k",
				"SMOKE_PROWLARR_URL": "u", "SMOKE_PROWLARR_APIKEY": "k",
			},
		},
		{
			name: "no prowlarr",
			env:  map[string]string{"SMOKE_HARBRR_URL": "u", "SMOKE_HARBRR_APIKEY": "k"},
			want: []string{"SMOKE_PROWLARR_URL", "SMOKE_PROWLARR_APIKEY"},
		},
		{
			name: "blank key is missing",
			env: map[string]string{
				"SMOKE_HARBRR_URL": "u", "SMOKE_HARBRR_APIKEY": "  ",
				"SMOKE_PROWLARR_URL": "u", "SMOKE_PROWLARR_APIKEY": "k",
			},
			want: []string{"SMOKE_HARBRR_APIKEY"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := missingRequired(func(k string) string { return tt.env[k] })
			if !slices.Equal(got, tt.want) {
				t.Errorf("missingRequired = %v, want %v", got, tt.want)
			}
		})
	}
}
