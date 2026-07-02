package cardigann

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestDefaultConfig pins the settings -> .Config default resolution against
// Jackett's ConfigurationData seeding: a checkbox is "True"/"" by its default, a
// select/text uses its Default verbatim, and a setting with no default resolves
// to "".
func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	scalar := func(s string) *loader.Scalar { return &loader.Scalar{Value: s, Set: true} }

	def := &loader.Definition{
		Settings: []loader.SettingsField{
			{Name: "apikey", Type: "text"},                                // no default -> ""
			{Name: "sort", Type: "select", Default: scalar("created_at")}, // select default
			{Name: "freeleech", Type: "checkbox", Default: scalar("false")},
			{Name: "internal", Type: "checkbox", Default: scalar("true")},
			{Name: "info_flaresolverr", Type: "info_flaresolverr"}, // display-only -> ""
		},
	}

	cfg := DefaultConfig(def)

	want := map[string]string{
		"apikey":            "",
		"sort":              "created_at",
		"freeleech":         "",     // unchecked -> "" (.False)
		"internal":          "True", // checked -> "True" (.True)
		"info_flaresolverr": "",
	}
	for k, w := range want {
		if got := cfg[k]; got != w {
			t.Errorf(".Config.%s = %q, want %q", k, got, w)
		}
	}
	if len(cfg) != len(want) {
		t.Errorf("config has %d keys, want %d (%v)", len(cfg), len(want), cfg)
	}
}

// TestMergeConfigOverrides proves an explicit WithConfig value wins over the
// settings default (Jackett: a user-configured value replaces the Default).
func TestMergeConfigOverrides(t *testing.T) {
	t.Parallel()

	base := map[string]string{"sort": "created_at", "apikey": ""}
	over := map[string]string{"apikey": "SECRET", "extra": "x"}

	merged := mergeConfig(base, over)

	if merged["sort"] != "created_at" {
		t.Errorf("sort = %q, want created_at (default kept)", merged["sort"])
	}
	if merged["apikey"] != "SECRET" {
		t.Errorf("apikey = %q, want SECRET (override wins)", merged["apikey"])
	}
	if merged["extra"] != "x" {
		t.Errorf("extra = %q, want x (override-only key present)", merged["extra"])
	}
	// Inputs are not mutated.
	if base["apikey"] != "" {
		t.Errorf("mergeConfig mutated base: apikey = %q", base["apikey"])
	}
}
