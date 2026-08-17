package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
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

// TestProseTrackerCountsAreCurrent pins the hand-written tracker counts in the README
// and the features overview to the same numbers the coverage generator derives, so a
// vendor refresh or a new native driver fails here until every count-bearing doc is
// updated together (the wording is pinned too — if a phrase must change, change it
// here and in the doc in the same commit).
func TestProseTrackerCountsAreCurrent(t *testing.T) {
	t.Parallel()

	defs, _, err := loader.New("").LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	corpus, built, planned := len(defs), len(nativeBuilt), len(nativePlanned)

	tests := []struct {
		file    string
		phrases []string
	}{
		{file: "../../README.md", phrases: []string{
			fmt.Sprintf("**%d trackers** — %d from the embedded Cardigann corpus plus %d native drivers (with **%d more native drivers planned**)",
				corpus+built, corpus, built, planned),
		}},
		{file: "../../website/docs/features/overview.md", phrases: []string{
			fmt.Sprintf("**%d trackers** — %d Cardigann definitions + %d native Go drivers",
				corpus+built+planned, corpus, built),
			fmt.Sprintf("all %d trackers", corpus+built+planned),
		}},
	}
	collapse := regexp.MustCompile(`\s+`)
	for _, tt := range tests {
		raw, err := os.ReadFile(tt.file)
		if err != nil {
			t.Fatalf("read %s: %v", tt.file, err)
		}
		// Markdown wraps sentences across lines; compare with whitespace collapsed.
		content := collapse.ReplaceAllString(string(raw), " ")
		for _, phrase := range tt.phrases {
			if !strings.Contains(content, phrase) {
				t.Errorf("%s tracker counts are stale — it must contain (modulo line wrapping):\n  %s", tt.file, phrase)
			}
		}
	}
}
