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

// defaultString returns the setting's Default scalar as a string, or "" when
// absent.
func defaultString(s loader.SettingsField) string {
	if s.Default == nil {
		return ""
	}
	return s.Default.String()
}

// mergeConfig overlays over onto a copy of base (over wins per key), so explicit
// WithConfig values override the settings defaults — matching Jackett, where a
// user's configured value replaces the setting Default.
func mergeConfig(base, over map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range over {
		merged[k] = v
	}
	return merged
}
