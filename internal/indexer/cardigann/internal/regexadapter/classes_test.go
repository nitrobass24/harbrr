package regexadapter

import "testing"

const (
	wantWordClass  = `[\p{L}\p{Mn}\p{Nd}\p{Pc}]`
	wantSpaceClass = `[\f\n\r\t\v\x{85}\p{Z}]`
)

// TestRewriteShorthandClasses pins the rewriter's exact output: .NET's Unicode
// definitions of \w \W \d \D \s \S, expanded to a class outside brackets and to
// bare members inside them, with escape pairing so a literal backslash is never
// misread. \W and \S inside a character class have no RE2 spelling, so they are
// reported unrewritable (and route to regexp2) rather than silently approximated.
func TestRewriteShorthandClasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		want    string
		wantOK  bool
	}{
		{"word", `\w`, wantWordClass, true},
		{"word negated", `\W`, `[^\p{L}\p{Mn}\p{Nd}\p{Pc}]`, true},
		{"digit", `\d`, `\p{Nd}`, true},
		{"digit negated", `\D`, `\P{Nd}`, true},
		{"space", `\s`, wantSpaceClass, true},
		{"space negated", `\S`, `[^\f\n\r\t\v\x{85}\p{Z}]`, true},

		{"word inside class", `[\w-]`, `[\p{L}\p{Mn}\p{Nd}\p{Pc}-]`, true},
		{"word inside negated class", `[^\w]`, `[^\p{L}\p{Mn}\p{Nd}\p{Pc}]`, true},
		{"digit inside class", `[\d]`, `[\p{Nd}]`, true},
		{"digit negated inside class", `[\D]`, `[\P{Nd}]`, true},
		{"mixed members inside class", `[\d\s]`, `[\p{Nd}\f\n\r\t\v\x{85}\p{Z}]`, true},
		{"class closes before shorthand", `[a-z]\w`, `[a-z]` + wantWordClass, true},

		{
			"quantified and grouped", `^(\d+)\s*-\s*(\w+)$`,
			`^(\p{Nd}+)` + wantSpaceClass + `*-` + wantSpaceClass + `*(` + wantWordClass + `+)$`, true,
		},

		// Escape pairing: the backslash in \\w is a literal backslash followed by
		// a literal 'w', not a shorthand class.
		{"escaped backslash then w", `\\w`, `\\w`, true},
		{"escaped backslash then class", `[\\w]`, `[\\w]`, true},

		// Untouched: no backslash at all, escapes both engines already agree on,
		// an explicit property class, and a trailing lone backslash.
		{"no escapes", `[A-Za-z]+`, `[A-Za-z]+`, true},
		{"unrelated escapes", `\n\t\.`, `\n\t\.`, true},
		{"explicit property class", `\p{Lu}+`, `\p{Lu}+`, true},
		{"trailing lone backslash", `foo\`, `foo\`, true},

		// RE2 cannot negate a multi-member set inside brackets: unrewritable,
		// returned unchanged so the caller routes to regexp2.
		{"negated word inside class", `[\W]`, `[\W]`, false},
		{"negated space inside class", `[abc\S]`, `[abc\S]`, false},
		{"negated space inside class among others", `(\d+[\S]+)`, `(\d+[\S]+)`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := rewriteShorthandClasses(tt.pattern)
			if ok != tt.wantOK {
				t.Fatalf("rewriteShorthandClasses(%q) ok = %v, want %v", tt.pattern, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("rewriteShorthandClasses(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
			if canRewriteShorthand(tt.pattern) != tt.wantOK {
				t.Errorf("canRewriteShorthand(%q) disagrees with the rewriter", tt.pattern)
			}
		})
	}
}

// TestDifferential_UnicodeShorthandClassesAgree is the parity gate for
// autobrr/harbrr#636: after the rewrite, the RE2-routed pattern must produce the
// SAME match, submatch, and replacement as regexp2 (.NET semantics) on the very
// inputs where RE2's ASCII-only shorthands used to diverge — accented Latin
// text, the U+00A0 an &nbsp; decodes to, and non-ASCII digits.
func TestDifferential_UnicodeShorthandClassesAgree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		pattern     string
		input       string
		repl        string
		wantReplace string
	}{
		// btarg.yml (es-AR) keywordsfilter: "Amélie" must stay one word.
		{"word class on accented text", `(\w+)`, "Amélie", "+$1", "+Amélie"},
		// nostradamus / lastfiles / party-tracker \W+ -> % keywordsfilters.
		{"negated word class on accented text", `\W+`, "Amélie Poulain", "%", "Amélie%Poulain"},
		{"eszett", `(\w+)`, "straße", "[$1]", "[straße]"},
		{"enye inside a class", `([\w-]+)`, "ñ-año", "<$1>", "<ñ-año>"},
		// A size cell whose &nbsp; decoded to U+00A0.
		{"space class matches nbsp", `(\d+(?:\.\d+)?)\s*(GB)`, "1.5 GB", "$1 $2", "1.5 GB"},
		{"space class still matches ascii space", `(\d+)\s+(MB)`, "700 MB", "$1-$2", "700-MB"},
		{"negated space class", `\S+`, "a b", "X", "X X"},
		// Unicode decimal digits (Arabic-Indic, Devanagari) are \p{Nd} in .NET.
		{"digit class on arabic-indic digits", `\d+`, "١٢٣", "N", "N"},
		{"digit class on devanagari digits", `(\d+)`, "३४ seeders", "[$1]", "[३४] seeders"},
		{"negated digit class", `\D+`, "12ab٣٤", "-", "12-٣٤"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			re2, err := newRE2(tt.pattern)
			if err != nil {
				t.Fatalf("RE2 compile: %v", err)
			}
			rx2, err := newRegexp2(tt.pattern)
			if err != nil {
				t.Fatalf("regexp2 compile: %v", err)
			}

			assertSameMatch(t, re2, rx2, tt.input)
			assertSameSubmatch(t, re2, rx2, tt.input)
			assertSameReplace(t, re2, rx2, tt.input, tt.repl)

			got, err := re2.ReplaceAllString(tt.input, tt.repl)
			if err != nil {
				t.Fatalf("RE2 ReplaceAllString: %v", err)
			}
			if got != tt.wantReplace {
				t.Errorf("RE2 replace = %q, want %q (the .NET result)", got, tt.wantReplace)
			}
		})
	}
}

// TestDifferential_WordBoundaryRoutesToRegexp2 covers the second half of
// autobrr/harbrr#636: \b and \B have no Unicode form in RE2, so their patterns
// must route to regexp2 and produce the .NET result there — including on
// accented text, where an RE2 \b would put a boundary in the middle of a word.
func TestDifferential_WordBoundaryRoutesToRegexp2(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		pattern     string
		input       string
		repl        string
		wantReplace string
	}{
		{"ascii boundary", `\bfoo\b`, "foo foobar foo", "X", "X foobar X"},
		{"boundary on accented word", `\bAmélie\b`, "voir Amélie ce soir", "X", "voir X ce soir"},
		{"non-boundary", `\Bar\B`, "marble", "X", "mXble"},
		{"boundary in a class is a backspace literal", `[\b]`, "a\bb", "X", "aXb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			re, err := Compile(tt.pattern, RouteOptions{})
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			assertEngine(t, re, EngineRegexp2)

			got, err := re.ReplaceAllString(tt.input, tt.repl)
			if err != nil {
				t.Fatalf("ReplaceAllString: %v", err)
			}
			if got != tt.wantReplace {
				t.Errorf("replace = %q, want %q", got, tt.wantReplace)
			}
		})
	}
}

// TestUnrewritableShorthandRoutesToRegexp2 pins the fallback: a pattern whose
// shorthand cannot be expressed in RE2 goes to regexp2 rather than being
// compiled with ASCII-only semantics.
func TestUnrewritableShorthandRoutesToRegexp2(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{`[\W]+`, `[a-z\S]+`} {
		t.Run(pattern, func(t *testing.T) {
			t.Parallel()

			re, err := Compile(pattern, RouteOptions{})
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			assertEngine(t, re, EngineRegexp2)
		})
	}
}
