package cardigann

import (
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// configTrue is Jackett's ".True" sentinel: a checked checkbox resolves its
// .Config value to "True", which the template truthiness treats as set. An
// unchecked box resolves to "" (Jackett's ".False" = null), which is falsy.
const configTrue = "True"

// DefaultConfig resolves a definition's settings into their default .Config
// values, reproducing Jackett's ConfigurationData seeding + GetBaseTemplateVariables:
// the .Config.<name> a request/login template reads BEFORE the user enters
// anything is the setting's Default (a checked checkbox -> "True", an unchecked
// one -> ""). NewEngine applies these defaults under any explicit WithConfig, so
// a search renders identically to a freshly-configured Jackett indexer (e.g.
// .Config.sort -> the select default; .Config.apikey -> "" until set).
func DefaultConfig(def *loader.Definition) map[string]string {
	if def == nil {
		return map[string]string{}
	}
	cfg := make(map[string]string, len(def.Settings))
	for _, s := range def.Settings {
		cfg[s.Name] = settingDefault(s)
	}
	return cfg
}

// settingDefault returns the default .Config value for one setting, by type.
func settingDefault(s loader.SettingsField) string {
	switch s.Type {
	case "checkbox":
		if defaultString(s) == "true" {
			return configTrue
		}
		return ""
	case "multi-select":
		// Jackett stores the Defaults list; templates that consume a multi-select
		// iterate it. harbrr's string config flattens it to a comma-joined value
		// (no vendored request template depends on the list shape).
		return strings.Join(s.Defaults, ",")
	case "text", "password", "select", "info", "":
		return defaultString(s)
	default:
		// info_category_8000 / info_cookie / info_flaresolverr / info_useragent are
		// fixed display-only settings Jackett never substitutes into a request or
		// login template, so their resolved value is irrelevant; keep it empty.
		return ""
	}
}

// CanonicalizeCheckboxes rewrites, in place, every checkbox setting's value in cfg to
// its canonical .Config form — configTrue ("True") when the stored value is truthy, ""
// otherwise — and returns cfg. Persisted config can carry a checkbox as the literal
// "false"/"0" (a stored form value for the unchecked state), which is NON-empty, so both
// template truthiness ({{ if .Config.x }}) and the freeleech decorator's `!= ""` view
// would read it as CHECKED. Canonicalizing on the way into the engine/decorator repairs
// that for already-stored rows without a migration. Non-checkbox settings are untouched.
func CanonicalizeCheckboxes(def *loader.Definition, cfg map[string]string) map[string]string {
	if def == nil {
		return cfg
	}
	for _, s := range def.Settings {
		if s.Type != "checkbox" {
			continue
		}
		if v, ok := cfg[s.Name]; ok {
			cfg[s.Name] = canonicalCheckbox(v)
		}
	}
	return cfg
}

// canonicalCheckbox maps a stored checkbox value to configTrue (checked) or "" (unchecked).
// Truthy is "true"/"1"/"on"/"yes" (case-insensitive); everything else — including "false",
// "0", and "" — is unchecked, matching settingDefault's checkbox mapping.
func canonicalCheckbox(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "on", "yes":
		return configTrue
	default:
		return ""
	}
}

// defaultString returns the setting's Default scalar as a string, or "" when
// absent.
func defaultString(s loader.SettingsField) string {
	if s.Default == nil {
		return ""
	}
	return s.Default.String()
}
