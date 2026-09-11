package template

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/internal/regexadapter"
)

// TestReReplaceUsesContextRegexRoute covers the template half of
// autobrr/harbrr#636: expandReReplace used to compile with a zero RouteOptions,
// so a non-Latin definition's {{ re_replace }} pattern went to RE2 while the same
// definition's field-filter patterns went to regexp2. The route now comes from
// the context the caller seeds.
//
// The observable difference is .NET's trailing-newline `$`: .NET (regexp2)
// matches `c$` in "abc\n" just before the final newline, RE2 only at end of text.
func TestReReplaceUsesContextRegexRoute(t *testing.T) {
	t.Parallel()

	const text = `{{ re_replace .Keywords "c$" "X" }}`

	tests := []struct {
		name  string
		route regexadapter.RouteOptions
		want  string
	}{
		{
			name: "latin language keeps the RE2 default",
			want: "abc\n",
		},
		{
			name:  "non-Latin language routes the template pattern to regexp2",
			route: regexadapter.RouteOptions{Language: "ru-RU"},
			want:  "abX\n",
		},
		{
			name:  "explicit opt-in routes to regexp2",
			route: regexadapter.RouteOptions{OptIn: true},
			want:  "abX\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := NewSeeded(Params{Keywords: "abc\n", RegexRoute: tt.route})
			got, err := Eval(text, ctx)
			if err != nil {
				t.Fatalf("Eval: %v", err)
			}
			if got != tt.want {
				t.Errorf("Eval = %q, want %q", got, tt.want)
			}
		})
	}
}
