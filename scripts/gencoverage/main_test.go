package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native/catalog"
	"github.com/autobrr/harbrr/internal/indexer/native/newznab"
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
		t.Fatal("website/docs/coverage.md is stale — regenerate it with `make coverage-docs` (go run ./scripts/gencoverage -write), which also updates the README/overview tracker counts")
	}
}

// TestNativeCatalogHasCoverageRows fails when a native driver ships without a
// coverage row: every definition id the catalog exposes must appear in the
// curated nativeBuilt/nativePlanned lists. Newznab presets are the one
// exception — the standing convention folds the whole family into the single
// "Usenet (Newznab)" row, so any id from newznab.Families() counts as covered
// (a new preset needs no row). Writing a new site driver therefore fails
// `go test` until its row exists, and `make coverage-docs` propagates the row
// into every count-bearing doc.
func TestNativeCatalogHasCoverageRows(t *testing.T) {
	t.Parallel()

	covered := map[string]bool{}
	for _, n := range nativeBuilt {
		covered[n.id] = true
	}
	for _, n := range nativePlanned {
		covered[n.id] = true
	}
	for _, f := range newznab.Families() {
		covered[f.Definition.ID] = true
	}

	var missing []string
	for id := range catalog.All() {
		if !covered[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("native drivers with no coverage row: %v — add each to nativeBuilt in scripts/gencoverage/main.go, then run `make coverage-docs`", missing)
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
				corpus+built, corpus, built),
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
				t.Errorf("%s tracker counts are stale — run `make coverage-docs`; expected (modulo line wrapping):\n  %s", tt.file, phrase)
			}
		}
	}
}
