package main

import (
	"os"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestCoverageDocIsCurrent is the drift gate for website/docs/coverage.md: the
// committed page must match what this generator produces from the embedded
// corpus and the curated native/liveTested lists. Without it, editing those
// lists (or refreshing the vendored defs outside the scheduled workflow) leaves
// the published page silently stale until the next vendor refresh regenerates it.
func TestCoverageDocIsCurrent(t *testing.T) {
	t.Parallel()

	defs, skipped, err := loader.New("").LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	want := generate(defs, skipped)

	got, err := os.ReadFile("../../website/docs/coverage.md")
	if err != nil {
		t.Fatalf("read committed coverage.md: %v", err)
	}
	if string(got) != want {
		t.Fatal("website/docs/coverage.md is stale — regenerate it with `make coverage-docs` (go run ./scripts/gencoverage > website/docs/coverage.md)")
	}
}
