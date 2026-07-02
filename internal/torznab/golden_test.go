package torznab

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// update regenerates golden files when set (go test -race -count=1 -run X
// -update). Only run it after confirming the produced XML matches the case's
// oracle; hand-derived and jackett-port goldens must never be blindly refreshed.
var update = flag.Bool("update", false, "update golden XML files")

// assertGolden byte-compares got against the golden file at testdata/name,
// writing it instead when -update is set. The comparison is byte-for-byte: these
// goldens are harbrr's own canonical, deterministic output, so any diff is a real
// change.
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
