package registry_test

import (
	"context"
	"iter"
	"os/exec"
	"strings"
	"testing"
)

const nativePkgPrefix = "github.com/autobrr/harbrr/internal/indexer/native/"

// TestNoConcreteNativeDriverImports pins the core->concrete arrow this package must
// never grow back: registry.go resolves native families through an injected
// map[string]native.Family (see registry.New), never by importing a concrete driver
// package (internal/indexer/native/<driver>) itself. The full catalog aggregation
// lives one level up in internal/indexer/native/catalog, which only *_test.go files
// in this package may import (a test-binary dependency is not the core arrow).
//
// "go list -deps" without -test reports only the production import graph (it
// ignores this package's own _test.go files, including this one and the catalog
// import in registry_test.go), so this test fails the moment a driver import lands
// in a non-test file of package registry.
func TestNoConcreteNativeDriverImports(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available on PATH; skipping import-arrow check")
	}

	// Derive the forbidden set from what catalog.All actually aggregates, so a driver
	// added there can never be left silently unguarded by a hand-maintained list.
	// "go list -deps X" includes X itself, so catalog is forbidden for free.
	forbidden := map[string]bool{}
	for dep := range listDeps(t, nativePkgPrefix+"catalog") {
		// Exactly one path segment past .../native/: the bare native package is a
		// legitimate production dependency (registry consumes native.Family).
		if name, ok := strings.CutPrefix(dep, nativePkgPrefix); ok && !strings.Contains(name, "/") {
			forbidden[dep] = true
		}
	}

	// A derivation that quietly yielded nothing would pass forever. nebulance and
	// torznab are the two the previous hand-written list had already missed.
	for _, want := range []string{"gazelle", "nebulance", "torznab"} {
		if !forbidden[nativePkgPrefix+want] {
			t.Fatalf("derived forbidden set (%d entries) is missing %q; the derivation from catalog's dependency graph is broken", len(forbidden), want)
		}
	}

	for dep := range listDeps(t, "github.com/autobrr/harbrr/internal/indexer/registry") {
		if forbidden[dep] {
			t.Errorf("internal/indexer/registry (production) depends on %q; native families must be injected via registry.New's families parameter, never imported directly (see internal/indexer/native/catalog and cmd/harbrr/serve.go)", dep)
		}
	}
}

// listDeps returns pkg's production (non-test) dependency graph, pkg itself
// included. A toolchain failure skips rather than fails: this is a structural
// guard, not a build check.
func listDeps(t *testing.T, pkg string) iter.Seq[string] {
	t.Helper()

	out, err := exec.CommandContext(context.Background(), "go", "list", "-deps", pkg).Output()
	if err != nil {
		t.Skipf("go list -deps %s failed (%v); skipping import-arrow check", pkg, err)
	}
	return strings.SplitSeq(strings.TrimSpace(string(out)), "\n")
}
