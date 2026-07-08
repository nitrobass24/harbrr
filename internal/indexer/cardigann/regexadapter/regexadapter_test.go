package regexadapter

import (
	"errors"
	"strings"
	"testing"

	"github.com/dlclark/regexp2"
)

// --- Differential suite: the parity gate ------------------------------------
//
// RE2-safe patterns must produce IDENTICAL match/capture/replace results on both
// engines (so routing one to RE2 is observably safe). .NET-only patterns must be
// rejected by RE2 (so they are NOT routed to it) while regexp2 produces the
// expected .NET result.

func TestDifferential_RE2SafePatternsAgree(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		pattern string
		input   string
		repl    string
	}{
		{"digits capture", `(\d+)`, "abc123def", "[$1]"},
		{"word replace", `\bfoo\b`, "foo foobar foo", "X"},
		{"alternation", `cat|dog`, "a dog and a cat", "pet"},
		{"anchored", `^id-(\w+)$`, "id-abc123", "$1"},
		{"two groups dollar-letter", `(\d+)-(\d+)`, "12-34", "$2x$1"},
		{"non-greedy", `<(.+?)>`, "<a><b>", "{$1}"},
		{"char class", `[A-Za-z]+`, "abc 123 def", "_"},
		{"no match", `zzz`, "abcdef", "X"},
		// Non-participating groups must yield the SAME submatch length + "" on both
		// engines, since filterRegexp's len(m)<2 / group-1 contract depends on it.
		{"alternation groups", `(a)|(b)`, "b", "<$1$2>"},
		{"optional group present", `x(\d+)?y`, "x42y", "[$1]"},
		{"optional group absent", `x(\d+)?y`, "xy", "[$1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			re2, err := newRE2(tc.pattern)
			if err != nil {
				t.Fatalf("RE2 compile: %v", err)
			}
			rx2, err := newRegexp2(tc.pattern)
			if err != nil {
				t.Fatalf("regexp2 compile: %v", err)
			}

			assertEngine(t, re2, EngineRE2)
			assertEngine(t, rx2, EngineRegexp2)

			assertSameMatch(t, re2, rx2, tc.input)
			assertSameSubmatch(t, re2, rx2, tc.input)
			assertSameReplace(t, re2, rx2, tc.input, tc.repl)
		})
	}
}

func TestDifferential_DotNetOnlyPatterns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		pattern     string
		input       string
		wantMatch   bool
		wantGroup1  string
		repl        string
		wantReplace string
	}{
		{
			name: "negative lookahead", pattern: `foo(?!bar)`, input: "foobaz",
			wantMatch: true, repl: "X", wantReplace: "Xbaz",
		},
		{
			name: "negative lookahead blocks", pattern: `foo(?!bar)`, input: "foobar",
			wantMatch: false, repl: "X", wantReplace: "foobar",
		},
		{
			name: "positive lookahead", pattern: `\d+(?= dollars)`, input: "100 dollars",
			wantMatch: true, repl: "N", wantReplace: "N dollars",
		},
		{
			name: "lookbehind", pattern: `(?<=\$)\d+`, input: "$50", wantMatch: true,
			repl: "N", wantReplace: "$N",
		},
		{
			name: "backreference", pattern: `(\w)\1`, input: "letter", wantMatch: true,
			wantGroup1: "t", repl: "X", wantReplace: "leXer",
		},
		{
			name: "named group dotnet", pattern: `(?<year>\d{4})`, input: "year 2026",
			wantMatch: true, wantGroup1: "2026", repl: "Y", wantReplace: "year Y",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// RE2 must reject (or not be chosen for) these .NET-only patterns.
			if _, err := newRE2(tc.pattern); err == nil {
				t.Fatalf("RE2 unexpectedly accepted .NET-only pattern %q", tc.pattern)
			}
			// Routing must NOT choose RE2.
			routed, err := Compile(tc.pattern, RouteOptions{})
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			assertEngine(t, routed, EngineRegexp2)

			ok, err := routed.MatchString(tc.input)
			if err != nil {
				t.Fatalf("MatchString: %v", err)
			}
			if ok != tc.wantMatch {
				t.Fatalf("MatchString(%q)=%v want %v", tc.input, ok, tc.wantMatch)
			}
			if tc.wantGroup1 != "" {
				sub, err := routed.FindStringSubmatch(tc.input)
				if err != nil {
					t.Fatalf("FindStringSubmatch: %v", err)
				}
				if len(sub) < 2 || sub[1] != tc.wantGroup1 {
					t.Fatalf("group1=%v want %q", sub, tc.wantGroup1)
				}
			}
			got, err := routed.ReplaceAllString(tc.input, tc.repl)
			if err != nil {
				t.Fatalf("ReplaceAllString: %v", err)
			}
			if got != tc.wantReplace {
				t.Fatalf("Replace(%q,%q)=%q want %q", tc.input, tc.repl, got, tc.wantReplace)
			}
		})
	}
}

// --- Routing unit tests: each trigger in isolation --------------------------

func TestRouting_Triggers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		pattern string
		opts    RouteOptions
		want    Engine
	}{
		{"plain latin -> RE2", `(\d+)-(\w+)`, RouteOptions{}, EngineRE2},
		{"latin language -> RE2", `\d+`, RouteOptions{Language: "en-US"}, EngineRE2},
		{"opt-in -> regexp2", `\d+`, RouteOptions{OptIn: true}, EngineRegexp2},
		{"non-latin zh -> regexp2", `\d+`, RouteOptions{Language: "zh-CN"}, EngineRegexp2},
		{"non-latin ru -> regexp2", `\d+`, RouteOptions{Language: "ru-RU"}, EngineRegexp2},
		{"non-latin el -> regexp2", `\d+`, RouteOptions{Language: "el-GR"}, EngineRegexp2},
		{"non-latin he -> regexp2", `\d+`, RouteOptions{Language: "he-IL"}, EngineRegexp2},
		{"neg-lookahead -> regexp2", `foo(?!bar)`, RouteOptions{}, EngineRegexp2},
		{"pos-lookahead -> regexp2", `foo(?=bar)`, RouteOptions{}, EngineRegexp2},
		{"lookbehind -> regexp2", `(?<=x)y`, RouteOptions{}, EngineRegexp2},
		{"neg-lookbehind -> regexp2", `(?<!x)y`, RouteOptions{}, EngineRegexp2},
		{"named group dotnet -> regexp2", `(?<n>\d+)`, RouteOptions{}, EngineRegexp2},
		{"backref -> regexp2", `(\w)\1`, RouteOptions{}, EngineRegexp2},
		{"named backref -> regexp2", `(?<a>\w)\k<a>`, RouteOptions{}, EngineRegexp2},
		{"atomic group -> regexp2", `(?>\d+)`, RouteOptions{}, EngineRegexp2},
		{"conditional -> regexp2", `(a)(?(1)b|c)`, RouteOptions{}, EngineRegexp2},
		// Go-native named group "(?P<name>)" is RE2-expressible -> stays RE2.
		{"go named group -> RE2", `(?P<n>\d+)`, RouteOptions{}, EngineRE2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			re, err := Compile(tc.pattern, tc.opts)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.pattern, err)
			}
			if re.Engine() != tc.want {
				t.Fatalf("engine=%s want %s", re.Engine(), tc.want)
			}
		})
	}
}

// TestRouting_RE2CompileFailureFallsBack covers trigger (c): a pattern RE2
// cannot parse but regexp2 accepts must fall back to regexp2, not error.
func TestRouting_RE2CompileFailureFallsBack(t *testing.T) {
	t.Parallel()
	// A bare lookahead is .NET grammar RE2 rejects; even without the construct
	// detector it must fall back. Verify the fallback path directly with a
	// pattern that the detector also catches, plus an unbalanced construct that
	// only fails at compile time would need regexp2 — use lookbehind which RE2
	// flatly rejects.
	re, err := Compile(`(?<=USD)\d+`, RouteOptions{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if re.Engine() != EngineRegexp2 {
		t.Fatalf("engine=%s want regexp2", re.Engine())
	}
}

// TestRouting_BothEnginesReject covers the error case: a pattern neither engine
// accepts must return an error referencing both engines (and not the value).
func TestRouting_BothEnginesReject(t *testing.T) {
	t.Parallel()
	_, err := Compile(`(unclosed`, RouteOptions{})
	if err == nil {
		t.Fatal("expected error for pattern neither engine accepts")
	}
	msg := err.Error()
	for _, want := range []string{"RE2", "regexp2", "(unclosed"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}

// --- .NET Unicode block normalization ---------------------------------------

// TestUnicodeBlockNormalization pins the engine-absorbed translation of .NET
// Unicode block names (\p{IsCyrillic}) to the Go script names both engines
// accept (\p{Cyrillic}). The corpus uses \p{IsCyrillic} and
// \p{IsCJKUnifiedIdeographs}, which regexp2 rejects verbatim.
func TestUnicodeBlockNormalization(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		pattern    string
		matchInput string
		wantMatch  bool
	}{
		{"cyrillic block", `([\p{IsCyrillic}]+)`, "Привет", true},
		{"cyrillic block no match", `^[\p{IsCyrillic}]+$`, "hello", false},
		{"cjk block", `([\p{IsCJKUnifiedIdeographs}]+)`, "漢字", true},
		{"negated block", `\P{IsCyrillic}`, "a", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// A .NET block name forces regexp2 and must compile (post-normalize).
			re, err := Compile(tc.pattern, RouteOptions{})
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.pattern, err)
			}
			if re.Engine() != EngineRegexp2 {
				t.Fatalf("engine=%s want regexp2", re.Engine())
			}
			ok, err := re.MatchString(tc.matchInput)
			if err != nil {
				t.Fatalf("MatchString: %v", err)
			}
			if ok != tc.wantMatch {
				t.Fatalf("MatchString(%q)=%v want %v", tc.matchInput, ok, tc.wantMatch)
			}
		})
	}
}

func TestNormalizePattern(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{`[\p{IsCyrillic}]+`, `[\p{Cyrillic}]+`},
		{`\p{IsCJKUnifiedIdeographs}`, `\p{Han}`},
		{`\P{IsCyrillic}`, `\P{Cyrillic}`},
		{`\d+`, `\d+`},                                     // no block: untouched
		{`\p{IsUnknownBlockXYZ}`, `\p{IsUnknownBlockXYZ}`}, // unknown: untouched (loud later)
		{`\p{S}\p{P}`, `\p{S}\p{P}`},                       // general categories: untouched
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := normalizePattern(tc.in); got != tc.want {
				t.Fatalf("normalizePattern(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

// --- ReDoS guard ------------------------------------------------------------

// TestReDoS_Timeout asserts a catastrophic-backtracking pattern on the regexp2
// path TIMES OUT (bounded MatchTimeout) rather than hanging the test.
func TestReDoS_Timeout(t *testing.T) {
	t.Parallel()
	// Classic catastrophic backtracking: (a+)+$ against a long non-matching
	// input. Force the regexp2 path via OptIn (RE2 would run this in linear time
	// and never exhibit the pathology — the timeout guard exists for regexp2).
	re, err := Compile(`(a+)+$`, RouteOptions{OptIn: true})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if re.Engine() != EngineRegexp2 {
		t.Fatalf("engine=%s want regexp2", re.Engine())
	}
	input := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaX"
	_, err = re.MatchString(input)
	if err == nil {
		t.Fatal("expected a timeout error from the regexp2 ReDoS guard")
	}
	if !errors.Is(err, ErrMatchTimeout) {
		t.Fatalf("expected ErrMatchTimeout, got %v", err)
	}
	// The sanitized error must NOT leak the matched input (passkey-safety).
	if strings.Contains(err.Error(), input) {
		t.Fatalf("timeout error leaked matched input: %q", err.Error())
	}
}

// TestReDoS_TimeoutOnLaterMatchInReplace guards the regexp2.Replace swallow bug:
// the library discards a FindNextMatch error AFTER the first match and returns
// ("", nil), which would silently truncate a field to "" under a ReDoS attack.
// Our hand-driven replace loop must instead surface ErrMatchTimeout. The pattern
// matches a cheap first token ("X") so the timeout fires on a *later* match.
func TestReDoS_TimeoutOnLaterMatchInReplace(t *testing.T) {
	t.Parallel()
	re, err := Compile(`X|(a+)+$`, RouteOptions{OptIn: true})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	input := "X" + strings.Repeat("a", 44) + "Y"
	out, err := re.ReplaceAllString(input, "Z")
	if err == nil {
		t.Fatalf("expected ErrMatchTimeout, got out=%q err=nil (swallowed timeout)", out)
	}
	if !errors.Is(err, ErrMatchTimeout) {
		t.Fatalf("expected ErrMatchTimeout, got %v", err)
	}
	if strings.Contains(err.Error(), input) {
		t.Fatalf("timeout error leaked matched input: %q", err.Error())
	}
}

// TestMixedNamedNumberedGroupOrdering documents a KNOWN, narrow parity hole.
// RE2 returns capture groups in positional (left-to-right) order; regexp2/.NET
// returns numbered groups first, then named groups. For a pattern that mixes a
// Go-style named group "(?P<...)" (which routes to RE2) with an unnamed group,
// FindStringSubmatch's group 1 therefore differs between the engines — and the
// regexp2/.NET order is what Jackett returns. This is a latent divergence, not a
// bug we can fix without per-engine group renumbering; the corpus has a single
// named group total, so it is pinned here rather than papered over. Pure-named
// and pure-numbered patterns agree (covered by the differential suite).
func TestMixedNamedNumberedGroupOrdering(t *testing.T) {
	t.Parallel()
	// (?P<year>...) is Go-named -> RE2; positional order puts the named group at 1.
	re2, err := newRE2(`(?P<year>\d{4})-(\d{2})`)
	if err != nil {
		t.Fatalf("RE2 compile: %v", err)
	}
	sub, err := re2.FindStringSubmatch("2026-06")
	if err != nil {
		t.Fatalf("FindStringSubmatch: %v", err)
	}
	// RE2 positional: group 1 is the (declared-first) named group, "2026".
	if len(sub) != 3 || sub[1] != "2026" || sub[2] != "06" {
		t.Fatalf("RE2 submatch=%v want [2026-06 2026 06] (positional order)", sub)
	}
	// .NET (the parity target) would put the NUMBERED group at index 1 ("06").
	// The .NET-spelled equivalent routes to regexp2 and demonstrates that order.
	rx2, err := Compile(`(?<year>\d{4})-(\d{2})`, RouteOptions{})
	if err != nil {
		t.Fatalf("Compile dotnet-named: %v", err)
	}
	assertEngine(t, rx2, EngineRegexp2)
	subN, err := rx2.FindStringSubmatch("2026-06")
	if err != nil {
		t.Fatalf("FindStringSubmatch regexp2: %v", err)
	}
	if len(subN) != 3 || subN[1] != "06" {
		t.Fatalf("regexp2 submatch=%v want group1=06 (.NET numbered-first order)", subN)
	}
}

// --- Replacement-token parity ------------------------------------------------

// TestReplacementTokenParity pins the .NET replacement-token normalization on
// the RE2 path: "$1x" must mean group1 + "x" (.NET), not the group named "1x".
func TestReplacementTokenParity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		pattern string
		input   string
		repl    string
		want    string
	}{
		{"dollar-digit then letter", `(\d+)`, "12abc", "$1x", "12xabc"},
		{
			"two-digit group boundary", `(.)(.)(.)(.)(.)(.)(.)(.)(.)(.)(.)(.)`,
			"abcdefghijkl", "$12", "l",
		},
		{"escaped dollar literal", `(\d)`, "5", "$$1", "$1"},
		{"braced passthrough", `(\d+)`, "12abc", "${1}x", "12xabc"},
		{"trailing dollar", `(\d+)`, "12", "$1$", "12$"},
		// $NN overflow: .NET consumes the whole digit run as one group number; if
		// invalid, the entire "$NN" is literal (NOT shrunk to $1 + "2").
		{"two-digit overflow one group", `(\d)`, "5", "$12", "$12"},
		{"two-digit overflow with zero", `(\d)`, "5", "$10", "$10"},
		{"two-group two-digit overflow", `(\d)(\d)`, "56", "$12", "$12"},
		// .NET special replacement tokens.
		{"whole-match token", `\d+`, "ab12cd", "<$&>", "ab<12>cd"},
		{"left-portion token", `(\d+)`, "ab12", "[$`]", "ab[ab]"},
		{"right-portion token", `(\d+)`, "12cd", "[$']", "[cd]cd"},
		{"entire-input token", `\d+`, "a1b", "[$_]", "a[a1b]b"},
		// $+ is the LAST-NUMBERED group unconditionally (verified against native
		// regexp2.Replace, the .NET oracle), NOT the highest group that captured.
		{"last-group token", `(\d)(\d)`, "56", "$+", "6"}, // last-numbered == captured
		// Alternation: only group 1 captures, but $+ names group 2 (last-numbered),
		// which is empty -> "<>", not "<foo>".
		{"last-group token alternation empty", `(foo)|(bar)`, "foo", "<$+>", "<>"},
		// No explicit groups: $+ falls back to group 0 (the whole match).
		{"last-group token no groups", `\d+`, "ab12cd", "<$+>", "ab<12>cd"},
		{"unknown braced literal", `(\d)`, "5", "${nope}", "${nope}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			re2, err := newRE2(tc.pattern)
			if err != nil {
				t.Fatalf("RE2 compile: %v", err)
			}
			rx2, err := newRegexp2(tc.pattern)
			if err != nil {
				t.Fatalf("regexp2 compile: %v", err)
			}
			gotRE2, err := re2.ReplaceAllString(tc.input, tc.repl)
			if err != nil {
				t.Fatalf("RE2 replace: %v", err)
			}
			if gotRE2 != tc.want {
				t.Fatalf("RE2 replace=%q want %q", gotRE2, tc.want)
			}
			// .NET (regexp2) is the source of truth: RE2 normalized result must
			// equal regexp2's raw result for the same repl token.
			gotRx2, err := rx2.ReplaceAllString(tc.input, tc.repl)
			if err != nil {
				t.Fatalf("regexp2 replace: %v", err)
			}
			if gotRE2 != gotRx2 {
				t.Fatalf("divergence: RE2=%q regexp2=%q (repl %q)", gotRE2, gotRx2, tc.repl)
			}
			// Ground the parity claim against regexp2's NATIVE Replace (the real
			// .NET implementation). Both adapter engines share our token expander,
			// so they trivially agree; this asserts the expander itself matches
			// .NET semantics rather than just agreeing with itself.
			nativeRx2 := regexp2.MustCompile(tc.pattern, regexp2.None)
			native, err := nativeRx2.Replace(tc.input, tc.repl, -1, -1)
			if err != nil {
				t.Fatalf("native regexp2 replace: %v", err)
			}
			if gotRE2 != native {
				t.Fatalf("expander diverges from native .NET: got %q native %q (repl %q)",
					gotRE2, native, tc.repl)
			}
		})
	}
}

// --- helpers ----------------------------------------------------------------

func newRE2(pattern string) (*Regexp, error) {
	re, err := Compile(pattern, RouteOptions{})
	if err != nil {
		return nil, err
	}
	if re.Engine() != EngineRE2 {
		return nil, errors.New("pattern did not route to RE2")
	}
	return re, nil
}

func newRegexp2(pattern string) (*Regexp, error) {
	return Compile(pattern, RouteOptions{OptIn: true})
}

func assertEngine(t *testing.T, re *Regexp, want Engine) {
	t.Helper()
	if re.Engine() != want {
		t.Fatalf("engine=%s want %s", re.Engine(), want)
	}
}

func assertSameMatch(t *testing.T, a, b *Regexp, input string) {
	t.Helper()
	ma, err := a.MatchString(input)
	if err != nil {
		t.Fatalf("a.MatchString: %v", err)
	}
	mb, err := b.MatchString(input)
	if err != nil {
		t.Fatalf("b.MatchString: %v", err)
	}
	if ma != mb {
		t.Fatalf("MatchString divergence: RE2=%v regexp2=%v", ma, mb)
	}
}

func assertSameSubmatch(t *testing.T, a, b *Regexp, input string) {
	t.Helper()
	sa, err := a.FindStringSubmatch(input)
	if err != nil {
		t.Fatalf("a.FindStringSubmatch: %v", err)
	}
	sb, err := b.FindStringSubmatch(input)
	if err != nil {
		t.Fatalf("b.FindStringSubmatch: %v", err)
	}
	// regexp2 always reports group 0 plus declared groups; RE2 reports group 0
	// plus declared groups too. For the RE2-safe corpus the leftmost match and
	// group 1 must agree.
	if len(sa) != len(sb) {
		t.Fatalf("submatch len divergence: RE2=%v regexp2=%v", sa, sb)
	}
	for i := range sa {
		if sa[i] != sb[i] {
			t.Fatalf("submatch[%d] divergence: RE2=%q regexp2=%q", i, sa[i], sb[i])
		}
	}
}

func assertSameReplace(t *testing.T, a, b *Regexp, input, repl string) {
	t.Helper()
	ra, err := a.ReplaceAllString(input, repl)
	if err != nil {
		t.Fatalf("a.ReplaceAllString: %v", err)
	}
	rb, err := b.ReplaceAllString(input, repl)
	if err != nil {
		t.Fatalf("b.ReplaceAllString: %v", err)
	}
	if ra != rb {
		t.Fatalf("replace divergence: RE2=%q regexp2=%q", ra, rb)
	}
}
