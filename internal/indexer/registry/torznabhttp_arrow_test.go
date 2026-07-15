package registry_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// TestNoTorznabHTTPImport pins the #175 fix: registry (the producer of indexers) must
// never import web/torznabhttp (an HTTP-serving package) to name its central
// abstraction. The indexer serving contract (Indexer/Provider/IndexerInfo/CacheInfo
// + the shared SearchReleases read pipeline) lives in internal/indexer/core instead,
// which both registry and web/torznabhttp depend on — the wrong-direction
// registry -> web/torznabhttp edge must stay gone.
//
// "go list -deps" without -test reports only the production import graph (it ignores
// this package's own _test.go files), so this test fails the moment a torznabhttp
// import lands in a non-test file of package registry.
func TestNoTorznabHTTPImport(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available on PATH; skipping import-arrow check")
	}

	out, err := exec.CommandContext(context.Background(), "go", "list", "-deps", "github.com/autobrr/harbrr/internal/indexer/registry").Output()
	if err != nil {
		t.Skipf("go list -deps failed (%v); skipping import-arrow check", err)
	}

	const forbidden = "internal/web/torznabhttp"
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, dep := range deps {
		if strings.HasSuffix(dep, forbidden) {
			t.Errorf("internal/indexer/registry (production) depends on %q; the indexer serving contract lives in internal/indexer/core, which registry must depend on instead — see ADR-0002", dep)
		}
	}
}
