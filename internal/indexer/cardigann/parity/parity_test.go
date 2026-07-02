package parity

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// update regenerates golden files: `go test ./... -run TestParity -update`.
// Use it only after confirming the new output matches the case's oracle
// (Jackett's asserted values for jackett-port cases, the hand-derivation in the
// case description for hand-derived cases). Never refresh a jackett-port golden
// blindly — that would let the engine grade its own homework.
var update = flag.Bool("update", false, "update parity golden files")

// TestParity runs every case directory under testdata/ through the real engine
// and diffs the canonical JSON against the recorded golden. It is the gate the
// engine must pass (docs/plan.md, Phase 2 — the offline parity gate).
//
// The suite skips cleanly when there are no cases, so the baseline build stays
// green; add the first testdata/<name>/case.yml to switch it on.
func TestParity(t *testing.T) {
	t.Parallel()

	dirs, err := caseDirs()
	if err != nil {
		t.Fatalf("scanning cases: %v", err)
	}
	if len(dirs) == 0 {
		t.Skip("no parity cases yet — add testdata/<name>/case.yml (see testdata/README.md)")
	}

	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()
			runCase(t, dir)
		})
	}
}

// runCase loads, runs, and golden-compares one case directory.
func runCase(t *testing.T, dir string) {
	t.Helper()

	c, err := Load(dir)
	if err != nil {
		t.Fatalf("loading case: %v", err)
	}

	got, err := c.Run(dir)
	if err != nil {
		t.Fatalf("running case %q: %v", c.Name, err)
	}

	golden := filepath.Join(dir, c.GoldenFile())
	if *update {
		// A jackett-port golden is the engine's homework, not its answer key:
		// its values must be re-derived from Jackett's own assertions, never
		// rewritten from harbrr's current output. Refuse to -update it.
		if c.GoldenSource == SourceJackettPort {
			t.Fatalf("refusing to -update jackett-port golden for %q: re-derive it from Jackett's CardigannIndexer*Tests.cs assertions, not from harbrr output", c.Name)
		}
		if isEmptyGolden(got) {
			t.Fatalf("refusing to write an empty golden for %q: zero releases usually means an extraction failure, which an empty golden would mask", c.Name)
		}
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(golden) //nolint:gosec // golden path under testdata/.
	if err != nil {
		t.Fatalf("reading golden %s (run with -update once the output is verified against the oracle): %v", golden, err)
	}
	// A committed empty golden is a vacuity hole — a real extraction regression to
	// zero releases would pass against it. No fixture should expect no releases.
	if isEmptyGolden(want) {
		t.Errorf("golden for %q is an empty release list; a fixture must produce >=1 release for the comparison to be meaningful", c.Name)
	}
	if string(got) != string(want) {
		t.Errorf("parity mismatch for %q [archetype=%s source=%s]\n--- got ---\n%s\n--- want ---\n%s",
			c.Name, c.Archetype, c.GoldenSource, got, want)
	}
}

// isEmptyGolden reports whether a golden encodes zero releases ("[]"), which the
// harness rejects so a regression that drops every release cannot pass vacuously.
func isEmptyGolden(b []byte) bool {
	return string(bytes.TrimSpace(b)) == "[]"
}

// caseDirs returns every testdata subdirectory holding a case.yml.
func caseDirs() ([]string, error) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join("testdata", e.Name())
		if _, err := os.Stat(filepath.Join(dir, "case.yml")); err == nil {
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}
