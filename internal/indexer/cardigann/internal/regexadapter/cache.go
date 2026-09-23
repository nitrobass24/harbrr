package regexadapter

import (
	"sync"
	"sync/atomic"
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
// The key space is bounded by the definitions on disk: a filter's pattern
// argument is a template (search/fields.go renderFilterArgs), but no definition
// interpolates row- or query-derived text into a pattern (0 of 1890 vendored
// filter arg-blocks), so nothing evicts and a plain sync.Map suffices. Two
// concurrent first compiles of the same key may both compile; last write wins
// and either entry is equally valid.
//
// compileCacheCap bounds the map anyway: a dropin that templates row- or
// query-derived text into a pattern would otherwise grow it by one entry per
// distinct search for the life of the process. Clearing everything past the cap
// is deliberately crude (the whole corpus recompiles once, ~6µs a pattern); it
// is a leak guard, not an eviction policy.
const compileCacheCap = 4096

var (
	compileCache    sync.Map // compileKey -> *Regexp
	compileCacheLen atomic.Int64
)

// storeCompiled memoizes r under key, clearing the whole cache first when it
// has passed compileCacheCap entries. The check and the store are deliberately
// not serialized: concurrent first compiles can overshoot the cap by their own
// count and skew compileCacheLen by as much until the next clear resets it.
// A leak guard tolerates that; a mutex here would serialize every cache miss
// on the search path to make an approximate number exact.
func storeCompiled(key compileKey, r *Regexp) {
	if compileCacheLen.Load() >= compileCacheCap {
		compileCache.Clear()
		compileCacheLen.Store(0)
	}
	if _, loaded := compileCache.LoadOrStore(key, r); !loaded {
		compileCacheLen.Add(1)
	}
}

// compileKey identifies a compiled pattern. It keys on the ROUTING DECISION
// rather than on RouteOptions, because that is all the routing inputs
// contribute to the result: every Latin-script language collapses onto one
// entry instead of one per language code.
type compileKey struct {
	pattern string
	regexp2 bool
}
