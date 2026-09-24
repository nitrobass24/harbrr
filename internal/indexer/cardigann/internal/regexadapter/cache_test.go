package regexadapter

import (
	"strconv"
	"sync"
	"testing"
)

// TestCompileCacheReuse pins the two properties the memo must hold: a repeat
// Compile of the same pattern returns the very same *Regexp, and the routing
// decision is part of the key — the identical pattern compiled under a route
// that forces regexp2 must NOT come back as the RE2 entry.
func TestCompileCacheReuse(t *testing.T) {
	const pattern = `(?i)^(.*?)\s+\[(\d{4})\]$`

	first, err := Compile(pattern, RouteOptions{})
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	second, err := Compile(pattern, RouteOptions{})
	if err != nil {
		t.Fatalf("second compile: %v", err)
	}
	if first != second {
		t.Errorf("repeat compile returned a new *Regexp; the memo is not being hit")
	}
	if first.Engine() != EngineRE2 {
		t.Errorf("engine = %v, want RE2", first.Engine())
	}

	forced, err := Compile(pattern, RouteOptions{Language: "ru-RU"})
	if err != nil {
		t.Fatalf("forced-regexp2 compile: %v", err)
	}
	if forced == first {
		t.Errorf("forced-regexp2 compile reused the RE2 entry; routing is not part of the cache key")
	}
	if forced.Engine() != EngineRegexp2 {
		t.Errorf("forced-regexp2 engine = %v, want regexp2", forced.Engine())
	}
}

// TestCompileCacheEntriesStillMatch guards the risk the memo introduces: a
// shared compiled pattern used concurrently by several searches must keep
// matching correctly, on both engines.
func TestCompileCacheEntriesStillMatch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  RouteOptions
		input string
		want  string
	}{
		{"re2", RouteOptions{}, "Some Movie [2019]", "2019"},
		{"regexp2", RouteOptions{Language: "ru-RU"}, "Some Movie [2019]", "2019"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					re, err := Compile(`\[(\d{4})\]`, tc.opts)
					if err != nil {
						t.Errorf("compile: %v", err)
						return
					}
					m, err := re.FindStringSubmatch(tc.input)
					if err != nil {
						t.Errorf("match: %v", err)
						return
					}
					if len(m) < 2 || m[1] != tc.want {
						t.Errorf("submatch = %v, want group 1 %q", m, tc.want)
					}
				})
			}
			wg.Wait()
		})
	}
}

// TestCompileCacheSkipsFailures pins the deliberate choice not to cache compile
// failures, so a later dropin fixing the pattern is not shadowed by a cached
// miss. Compile has TWO failure paths and they must both leave the cache clean:
// a regexp2-routed pattern fails inside the want2 branch and returns early,
// while an RE2-routed one falls through to the both-engines-reject branch.
// TestRouting_BothEnginesReject covers the error text itself.
func TestCompileCacheSkipsFailures(t *testing.T) {
	const bad = `(unclosed`

	for _, tc := range []struct {
		name string
		opts RouteOptions
	}{
		{"re2 route falls through to both-engines-reject", RouteOptions{}},
		{"regexp2 route fails inside the regexp2 branch", RouteOptions{Language: "ru-RU"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Compile(bad, tc.opts); err == nil {
				t.Fatal("expected an error for a pattern neither engine accepts")
			}
			// Ask the routing predicate for the key rather than hardcoding it,
			// so the assertion follows Compile's own choice of cache slot.
			key := compileKey{pattern: bad, regexp2: wantRegexp2(bad, tc.opts)}
			if _, ok := compileCache.Load(key); ok {
				t.Errorf("%+v was cached; failures must not be stored", key)
			}
			// Still an error on the repeat call — not a cached nil sneaking through.
			if _, err := Compile(bad, tc.opts); err == nil {
				t.Fatal("expected an error on the repeat compile too")
			}
		})
	}
}

// TestCompileCacheCapClears pins the leak guard: once the memo holds
// compileCacheCap entries the next store clears it, so an unbounded key space
// (a dropin templating query text into a pattern) cannot grow it for the life
// of the process. Drives storeCompiled directly so the test does not compile
// thousands of real patterns.
func TestCompileCacheCapClears(t *testing.T) {
	compileCache.Clear()
	compileCacheLen.Store(0)
	t.Cleanup(func() { compileCache.Clear(); compileCacheLen.Store(0) })

	r := &Regexp{}
	for i := range compileCacheCap {
		storeCompiled(compileKey{pattern: strconv.Itoa(i)}, r)
	}
	if got := compileCacheLen.Load(); got != compileCacheCap {
		t.Fatalf("len after filling to cap = %d, want %d", got, compileCacheCap)
	}
	storeCompiled(compileKey{pattern: "one-past-cap"}, r)
	if got := compileCacheLen.Load(); got != 1 {
		t.Errorf("len after overflow = %d, want 1 (cache cleared, new entry stored)", got)
	}
	if _, ok := compileCache.Load(compileKey{pattern: "0"}); ok {
		t.Errorf("entry from before the clear survived")
	}
	if _, ok := compileCache.Load(compileKey{pattern: "one-past-cap"}); !ok {
		t.Errorf("entry stored on overflow is missing")
	}
}
