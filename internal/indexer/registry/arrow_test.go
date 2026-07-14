package registry_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

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

	out, err := exec.CommandContext(context.Background(), "go", "list", "-deps", "github.com/autobrr/harbrr/internal/indexer/registry").Output()
	if err != nil {
		t.Skipf("go list -deps failed (%v); skipping import-arrow check", err)
	}

	// The 14 concrete native driver packages, plus the catalog package that
	// aggregates them: none may appear in the PRODUCTION (non-test) dependency
	// graph of this package.
	forbidden := []string{
		"internal/indexer/native/animebytes",
		"internal/indexer/native/avistaz",
		"internal/indexer/native/beyondhd",
		"internal/indexer/native/broadcastthenet",
		"internal/indexer/native/filelist",
		"internal/indexer/native/gazelle",
		"internal/indexer/native/gazellegames",
		"internal/indexer/native/hdbits",
		"internal/indexer/native/iptorrents",
		"internal/indexer/native/myanonamouse",
		"internal/indexer/native/newznab",
		"internal/indexer/native/nzbindex",
		"internal/indexer/native/passthepopcorn",
		"internal/indexer/native/torrentday",
		"internal/indexer/native/catalog",
	}

	deps := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, dep := range deps {
		for _, f := range forbidden {
			if strings.HasSuffix(dep, f) {
				t.Errorf("internal/indexer/registry (production) depends on %q; native families must be injected via registry.New's families parameter, never imported directly (see internal/indexer/native/catalog and cmd/harbrr/serve.go)", dep)
			}
		}
	}
}
