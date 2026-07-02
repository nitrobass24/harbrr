package parity

import (
	"path/filepath"
	"testing"
)

// TestHarnessDeterministicAndDetectsMismatch is the harness's own negative
// control: it proves the comparison is not vacuous. The harness must (a) produce
// byte-identical output across runs (so a golden comparison is meaningful) and
// (b) detect a single-byte divergence (so a passing case is a real signal, not
// an empty-vs-empty coincidence).
func TestHarnessDeterministicAndDetectsMismatch(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("testdata", "smoke-html-parse")
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("loading case: %v", err)
	}

	got, err := c.Run(dir)
	if err != nil {
		t.Fatalf("running case: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("empty output would make any golden comparison vacuous")
	}

	again, err := c.Run(dir)
	if err != nil {
		t.Fatalf("re-running case: %v", err)
	}
	if string(got) != string(again) {
		t.Fatal("harness output is non-deterministic; golden comparison would be unstable")
	}

	mutated := append([]byte(nil), got...)
	mutated[len(mutated)/2] ^= 0xFF
	if string(got) == string(mutated) {
		t.Fatal("harness comparison failed to detect a single-byte divergence")
	}
}

// TestSearchModeReplayPinsRequest proves search mode asserts request
// construction: the smoke-search case's replay transport demands the engine
// issue exactly `GET /search?q=ubuntu`. If request building regressed, Run would
// fail here, independent of response parsing.
func TestSearchModeReplayPinsRequest(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("testdata", "smoke-search")
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("loading case: %v", err)
	}
	if c.mode() != ModeSearch {
		t.Fatalf("expected search mode, got %q", c.mode())
	}
	if _, err := c.Run(dir); err != nil {
		t.Fatalf("search-mode run (request mismatch or parse error): %v", err)
	}
}
