package regexadapter

import (
	"time"

	"github.com/autobrr/go-cache/ttlcache"
)

// Compile is called once per ROW per FIELD on the search path (search/fields.go
// applies a field's filters inside the per-row loop), so a definition with N
// regex filters recompiles the same N def-authored patterns on every one of a
// result page's rows. Across the vendored corpus that is a mean of 5.6 regex
// filters per definition and a maximum of 57, at ~6µs and ~6KB of garbage per
// compile — a few MB of pure churn on an ordinary hundred-row search.
//
// The compiled result is a pure function of (normalized pattern, engine
// choice), so memoizing it is free of behavior change. Both backends document
// their compiled form as safe for concurrent use, and nothing mutates a
// *Regexp after compileRegexp2 returns it, so entries are shared across
// concurrent searches as-is.
//
// TTL rather than an unbounded map: a filter's pattern argument is a template
// (search/fields.go renderFilterArgs), so a definition MAY interpolate row- or
// query-derived text into the pattern itself and make the key space unbounded.
// No vendored definition does today (0 of 1890 filter arg-blocks), but a dropin
// or a future vendor refresh can, and an eviction policy costs nothing here.
// The 15-minute sliding window matches go-cache's own regexcache: a pattern in
// active use never expires, and a definition that stops being searched lets its
// patterns go.
var compileCache = ttlcache.New[compileKey, *Regexp](
	ttlcache.SetDefaultTTL(15*time.Minute),
	ttlcache.SetTimerResolution(5*time.Minute),
)

// compileKey identifies a compiled pattern. It keys on the ROUTING DECISION
// rather than on RouteOptions, because that is all the routing inputs
// contribute to the result: every Latin-script language collapses onto one
// entry instead of one per language code.
type compileKey struct {
	pattern string
	regexp2 bool
}
