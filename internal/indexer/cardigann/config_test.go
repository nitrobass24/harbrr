package cardigann

import (
	"maps"
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

// TestCanonicalizeCheckboxes pins the fix for autobrr/harbrr#119: a checkbox value
// persisted as the literal "false" (non-empty) must canonicalize to "" so it is not
// read as CHECKED by template truthiness or the freeleech `!= ""` view; only checkbox
// settings are touched, and other types keep their stored value.
func TestCanonicalizeCheckboxes(t *testing.T) {
	t.Parallel()

	def := &loader.Definition{
		Settings: []loader.SettingsField{
			{Name: "freeleech", Type: "checkbox"},
			{Name: "internal", Type: "checkbox"},
			{Name: "already_on", Type: "checkbox"},
			{Name: "absent_cb", Type: "checkbox"},
			{Name: "sort", Type: "select"},
			{Name: "apikey", Type: "text"},
		},
	}
	cfg := map[string]string{
		"freeleech":  "false",   // the bug: non-empty -> read as checked
		"internal":   "true",    // checked, non-canonical spelling
		"already_on": "True",    // already canonical
		"sort":       "false",   // NOT a checkbox -> must be left verbatim
		"apikey":     "keep-me", // text -> untouched
		// absent_cb not present -> stays absent
	}

	got := CanonicalizeCheckboxes(def, cfg)

	want := map[string]string{
		"freeleech":  "",         // "false" -> unchecked
		"internal":   configTrue, // "true" -> "True"
		"already_on": configTrue, // stays "True"
		"sort":       "false",    // select value untouched
		"apikey":     "keep-me",
	}
	for k, w := range want {
		if g := got[k]; g != w {
			t.Errorf("cfg[%q] = %q, want %q", k, g, w)
		}
	}
	if _, present := got["absent_cb"]; present {
		t.Errorf("absent checkbox must not be materialized, got %q", got["absent_cb"])
	}
	// The freeleech decorator keys on `!= ""`; "false" must now read as off.
	if got["freeleech"] != "" {
		t.Errorf("freeleech = %q, want \"\" so freeleechOnly is off", got["freeleech"])
	}
	// nil def is a safe no-op.
	if CanonicalizeCheckboxes(nil, map[string]string{"x": "false"})["x"] != "false" {
		t.Errorf("nil def must leave cfg untouched")
	}
}

// TestMergeConfigOverrides proves an explicit WithConfig value wins over the
// settings default (Jackett: a user-configured value replaces the Default), via
// the same maps.Clone+maps.Copy sequence engine.go uses to assemble o.config.
func TestMergeConfigOverrides(t *testing.T) {
	t.Parallel()

	base := map[string]string{"sort": "created_at", "apikey": ""}
	over := map[string]string{"apikey": "SECRET", "extra": "x"}

	merged := maps.Clone(base)
	maps.Copy(merged, over)

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
		t.Errorf("merge mutated base: apikey = %q", base["apikey"])
	}
}

// TestReservedSettingsIgnoreDefinitionDefaults pins the reserved-key contract for
// EVERY reserved engine setting (autobrr/harbrr#394): andmatch_fold_punctuation and
// degenerate_query_gate are per-INSTANCE settings, so a definition (necessarily a
// drop-in) declaring a same-named field with an opting-in default must NOT arm the
// behaviour on an untouched instance — the unconfigured path stays Jackett-identical
// and the parity corpus can never see the difference. An explicitly supplied caller
// value still wins.
func TestReservedSettingsIgnoreDefinitionDefaults(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		key     string
		defType string
		defVal  string
		// optIn is the caller value that arms the behaviour; off is a stored value
		// that must survive the overlay unchanged (unset and explicitly-off are
		// different states).
		optIn string
		off   string
	}{
		{
			name: "andmatch_fold_punctuation", key: "andmatch_fold_punctuation",
			defType: "checkbox", defVal: "true", optIn: "true", off: "false",
		},
		{
			name: "degenerate_query_gate", key: "degenerate_query_gate",
			defType: "select", defVal: "auto", optIn: "auto", off: "off",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			def := &loader.Definition{Settings: []loader.SettingsField{
				{Name: tc.key, Type: tc.defType, Default: &loader.Scalar{Value: tc.defVal, Set: true}},
			}}
			steps := []struct {
				name string
				opts []Option
				want string
			}{
				{"untouched instance ignores the definition default", nil, ""},
				{"explicit caller opt-in wins", []Option{WithConfig(map[string]string{tc.key: tc.optIn})}, tc.optIn},
				{"explicit caller off survives the overlay", []Option{WithConfig(map[string]string{tc.key: tc.off})}, tc.off},
			}
			for _, step := range steps {
				t.Run(step.name, func(t *testing.T) {
					t.Parallel()
					if got := resolveOptions(def, step.opts).config[tc.key]; got != step.want {
						t.Errorf("config value = %q, want %q", got, step.want)
					}
				})
			}
		})
	}
}
