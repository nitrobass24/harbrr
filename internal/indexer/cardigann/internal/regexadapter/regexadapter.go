package regexadapter

import (
	"fmt"
	"regexp"
	"time"

	"github.com/autobrr/go-cache/ttlcache"
	"github.com/dlclark/regexp2"
)

// matchTimeout bounds every regexp2 match operation. regexp2 (.NET semantics)
// uses a backtracking engine vulnerable to catastrophic backtracking, so a
// hard wall-clock timeout is the ReDoS guard the RE2 default would otherwise
// provide for free. RE2-routed patterns carry no timeout (RE2 is linear-time).
//
// This is a SOFT bound, not a hard wall: regexp2 polls a shared fastclock with
// ~100ms granularity (DefaultClockPeriod), so a pathological pattern can run up
// to ~matchTimeout+100ms of CPU before the check fires. It always terminates
// (no hang); it just is not a precise 250ms cutoff. Acceptable for single-user
// self-hosted software.
const matchTimeout = 250 * time.Millisecond

// Engine identifies which regex backend compiled a pattern.
type Engine int

const (
	// EngineRE2 is Go's stdlib regexp (RE2): linear-time, ReDoS-safe, the
	// default. Chosen for every pattern that does not trip a regexp2 trigger
	// and compiles under RE2.
	EngineRE2 Engine = iota
	// EngineRegexp2 is github.com/dlclark/regexp2 (.NET Regex semantics):
	// backtracking, supports lookarounds/backreferences/named groups. Chosen
	// on opt-in, non-Latin language, RE2 compile-failure, or .NET-only
	// constructs. Bounded by matchTimeout.
	EngineRegexp2
)

// String renders the engine name for routing reports and error context.
func (e Engine) String() string {
	switch e {
	case EngineRE2:
		return "RE2"
	case EngineRegexp2:
		return "regexp2"
	default:
		return fmt.Sprintf("Engine(%d)", int(e))
	}
}

// RouteOptions carries the caller-supplied routing inputs. There is no
// regex-engine opt-in FIELD in the Cardigann schema, so opt-in is modelled
// here as a flag the engine sets, and Language is the def's `language:` code
// (used for the non-Latin-script trigger). The zero value (Latin, no opt-in)
// routes purely on the pattern itself.
type RouteOptions struct {
	// Language is the Cardigann def `language:` code (e.g. "en-US", "zh-CN").
	// A non-Latin script forces regexp2. Empty means Latin/unknown.
	Language string
	// OptIn forces regexp2 regardless of pattern or language.
	OptIn bool
}

// Regexp is a compiled pattern bound to a single engine, exposing a uniform
// surface over RE2 and regexp2. Exactly one of re/re2 is non-nil. regexp2
// operations are fallible (compile/match can error or time out), so the
// match-bearing methods return errors on both paths for a uniform API.
type Regexp struct {
	engine Engine
	re     *regexp.Regexp  // set when engine == EngineRE2
	re2    *regexp2.Regexp // set when engine == EngineRegexp2
}

// Engine reports which backend compiled this pattern.
func (r *Regexp) Engine() Engine { return r.engine }

// Compile routes pattern to an engine per RouteOptions, then compiles it.
//
// Routing: regexp2 is chosen when the caller opts in, the language is
// non-Latin-script, or the pattern uses .NET-only constructs. Otherwise RE2 is
// tried first (its linear-time guarantee is the default), and on an RE2 COMPILE
// failure we fall back to regexp2 (which accepts a broader grammar). The result
// is an error only when BOTH engines reject the pattern.
//
// Error strings reference the pattern text and engine only. Patterns are
// definition-authored (not secret); the matched VALUE is never compiled in.
func Compile(pattern string, opts RouteOptions) (*Regexp, error) {
	want2 := wantRegexp2(pattern, opts)
	key := compileKey{pattern: pattern, regexp2: want2}
	if cached, ok := compileCache.Get(key); ok {
		return cached, nil
	}

	// Translate .NET Unicode block names (\p{IsCyrillic}) to the Go script names
	// both engines understand (\p{Cyrillic}). This is an engine-absorbed
	// .NET-vs-Go difference; the def text is never modified on disk.
	normalized := normalizePattern(pattern)

	if want2 {
		r, err := compileRegexp2(normalized)
		if err != nil {
			return nil, err
		}
		compileCache.Set(key, r, ttlcache.DefaultTTL)
		return r, nil
	}

	// Rewrite .NET's Unicode shorthand classes (\w \d \s and their negations) into
	// the explicit property classes RE2 understands, so an accented keyword or an
	// &nbsp;-bearing cell behaves as it does in Jackett (see classes.go).
	// wantRegexp2 has already routed away the shapes that cannot be rewritten, so
	// the unrewritable case — which returns the pattern unchanged — never lands here.
	rewritten, _ := rewriteShorthandClasses(normalized)

	re, err := regexp.Compile(rewritten)
	if err == nil {
		// (e) empty-match trigger, knowable only once RE2 has compiled the pattern:
		// Go's FindAll* family DROPS an empty match that abuts the previous match,
		// while .NET's Regex keeps it — so `a*` on "baaac" replaces 3 times under RE2
		// and 4 times under .NET (#686). Emulating .NET on the RE2 side means slicing
		// the input, which breaks ^/$ and lookaround context, so route instead.
		if re.MatchString("") {
			r, err2 := compileRegexp2(normalized)
			if err2 != nil {
				return nil, err2
			}
			compileCache.Set(key, r, ttlcache.DefaultTTL)
			return r, nil
		}
		r := &Regexp{engine: EngineRE2, re: re}
		compileCache.Set(key, r, ttlcache.DefaultTTL)
		return r, nil
	}

	// RE2 rejected a pattern the caller did not flag for regexp2. This is the
	// (c) trigger: .NET grammar RE2 cannot parse (e.g. an unescaped construct).
	// Fall back to regexp2; surface both errors if it too rejects. Errors quote
	// the ORIGINAL pattern (def-authored, not secret) for actionable context.
	r, re2Err := compileRegexp2(normalized)
	if re2Err != nil {
		// A pattern both engines reject is a definition bug: it recurs on every
		// row, but it is also about to be loud, and caching failures would mean
		// keying on an error too. Left uncached deliberately.
		return nil, fmt.Errorf("pattern %q compiles under neither engine: RE2: %w; regexp2: %w", pattern, err, re2Err)
	}
	compileCache.Set(key, r, ttlcache.DefaultTTL)
	return r, nil
}

// wantRegexp2 reports the (a) opt-in, (b) non-Latin, (d) .NET-construct
// triggers, plus the two class-semantics triggers (\b / \B, and a shorthand
// class RE2 cannot express — see classes.go). The (c) RE2-compile-failure trigger is handled as a fallback in
// Compile, not here, because it can only be known by attempting compilation.
// A .NET Unicode block name is a .NET-construct trigger too — both engines
// accept the normalized script name, but the block spelling signals .NET intent
// and most such defs are non-Latin regardless.
func wantRegexp2(pattern string, opts RouteOptions) bool {
	return opts.OptIn ||
		isNonLatinScript(opts.Language) ||
		hasDotNetConstructs(pattern) ||
		hasDotNetUnicodeBlock(pattern) ||
		// RE2 has no Unicode word boundary (and rejects the backspace \b inside a
		// character class), so \b / \B can only be honoured by regexp2.
		hasWordBoundary(pattern) ||
		// \W / \S inside a character class have no RE2 spelling — see classes.go.
		!canRewriteShorthand(pattern)
}

// compileRegexp2 compiles under regexp2's default (.NET) semantics —
// regexp2.None, matching Jackett's `new Regex(pattern)`. We deliberately do NOT
// pass regexp2.RE2, which would bend regexp2 toward RE2 syntax and defeat the
// .NET-parity purpose of routing here.
func compileRegexp2(pattern string) (*Regexp, error) {
	re2, err := regexp2.Compile(pattern, regexp2.None)
	if err != nil {
		return nil, fmt.Errorf("regexp2 compiling pattern %q: %w", pattern, err)
	}
	re2.MatchTimeout = matchTimeout
	return &Regexp{engine: EngineRegexp2, re2: re2}, nil
}
