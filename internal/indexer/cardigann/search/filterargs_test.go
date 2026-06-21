package search

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestRenderFilterArgs covers template-evaluating filter args before the filter
// runs — the rutor / Russian-tracker pattern where a re_replace replacement or an
// append value is guarded by a `{{ if .Config.x }}` setting. The regex PATTERN arg
// (no `{{`) must be left untouched.
func TestRenderFilterArgs(t *testing.T) {
	t.Parallel()
	const (
		cyrPattern = `(\([\p{IsCyrillic}\W]+\))|([\p{IsCyrillic}]+)`
		cyrRepl    = `{{ if .Config.stripcyrillic }}{{ else }}$1$2{{ end }}`
		rusAppend  = `{{ if .Config.addrussiantotitle }} RUS{{ else }}{{ end }}`
	)
	filters := []loader.FilterBlock{
		{Name: "re_replace", Args: loader.FilterArgs{cyrPattern, cyrRepl}},
		{Name: "append", Args: loader.FilterArgs{rusAppend}},
		{Name: "re_replace", Args: loader.FilterArgs{`\bWEB\sDL\b`, "WEB-DL"}}, // no template — untouched
	}

	tests := []struct {
		name             string
		config           map[string]string
		wantRepl, wantAp string
	}{
		{"both settings off", map[string]string{}, "$1$2", ""},
		{"stripcyrillic on", map[string]string{"stripcyrillic": "true"}, "", ""},
		{"addrussian on", map[string]string{"addrussiantotitle": "true"}, "$1$2", " RUS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deps := Deps{Config: tt.config, BaseURL: "https://t.invalid/"}
			got, err := renderFilterArgs(filters, deps, Query{}, map[string]string{})
			if err != nil {
				t.Fatalf("renderFilterArgs: %v", err)
			}
			// re_replace pattern (Args[0]) is never templated.
			if got[0].Args[0] != cyrPattern {
				t.Errorf("pattern arg mutated: %q", got[0].Args[0])
			}
			if got[0].Args[1] != tt.wantRepl {
				t.Errorf("re_replace replacement = %q, want %q", got[0].Args[1], tt.wantRepl)
			}
			if got[1].Args[0] != tt.wantAp {
				t.Errorf("append arg = %q, want %q", got[1].Args[0], tt.wantAp)
			}
			// The plain (template-free) filter is returned unchanged.
			if got[2].Args[0] != `\bWEB\sDL\b` || got[2].Args[1] != "WEB-DL" {
				t.Errorf("template-free filter mutated: %v", got[2].Args)
			}
		})
	}
}
