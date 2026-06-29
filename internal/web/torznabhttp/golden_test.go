package torznabhttp

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// update regenerates golden files (go test -race -count=1 -run X -update), after
// confirming the produced XML matches the hand-derived oracle.
var update = flag.Bool("update", false, "update golden XML files")

// assertGolden byte-compares got against testdata/name, writing it when -update
// is set. These goldens are harbrr's own deterministic output.
func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating golden dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatalf("writing golden %q: %v", name, err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // fixed test path under testdata/.
	if err != nil {
		t.Fatalf("reading golden %q (run with -update to create): %v", name, err)
	}
	if string(got) != string(want) {
		t.Errorf("golden %q mismatch:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
