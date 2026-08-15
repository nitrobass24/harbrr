package api

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// brokenDropin is malformed YAML (an unclosed flow sequence) — the simplest
// reliable parse failure, standing in for an operator's typo.
const brokenDropin = "id: %s\nlinks: [unclosed\n"

// TestLoadDefinitionSummariesSurfacesSkips is the defect this endpoint had: a
// drop-in that fails to parse used to vanish from /api/definitions entirely, and
// because drop-ins take precedence over the vendored snapshot WITHOUT falling
// back, a typo'd drop-in for a vendored id took the working tracker with it —
// the operator saw a tracker disappear with no error anywhere
// (autobrr/harbrr#390).
//
// Each case writes one broken drop-in and asserts the id is still in the list,
// marked failed and attributed to the drop-in. Loading behaviour is unchanged:
// the failed entry carries an error rather than a loadable definition, so it is
// visibly broken and NOT silently served from the vendored copy.
func TestLoadDefinitionSummariesSurfacesSkips(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
		// vendored marks an id that also exists in the vendored snapshot, i.e.
		// the drop-in shadows a working definition.
		vendored bool
	}{
		{name: "drop-in for a brand new id", id: "zzz-u1f5-api-broken"},
		{name: "drop-in shadowing a vendored id", id: "torrentleech", vendored: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, tt.id+".yml")
			if err := os.WriteFile(path, fmt.Appendf(nil, brokenDropin, tt.id), 0o600); err != nil {
				t.Fatalf("writing drop-in: %v", err)
			}

			entries, err := loadDefinitionSummaries(loader.New(dir), nil)
			if err != nil {
				t.Fatalf("loadDefinitionSummaries error = %v, want nil", err)
			}

			entry, ok := findEntry(entries, tt.id)
			if !ok {
				t.Fatalf("%q missing from the definitions list; a broken drop-in must be reported, not silently dropped", tt.id)
			}
			if entry.Error == "" {
				t.Errorf("%q has no error; a failed definition must say why", tt.id)
			}
			if entry.Origin != string(loader.OriginDropin) {
				t.Errorf("%q origin = %q, want %q (which file the operator must fix)", tt.id, entry.Origin, loader.OriginDropin)
			}
			if entry.Name == "" {
				t.Errorf("%q has an empty name; a failed entry must still be renderable", tt.id)
			}
			if tt.vendored && countEntries(entries, tt.id) != 1 {
				t.Errorf("%q appears %d times; the failure must replace the shadowed vendored entry, not duplicate it",
					tt.id, countEntries(entries, tt.id))
			}
		})
	}
}

// TestLoadDefinitionSummariesCleanCorpus is the negative control: with no
// drop-ins the whole vendored corpus loads and nothing is marked failed, so the
// assertions above cannot pass by accident on a broken catalog.
func TestLoadDefinitionSummariesCleanCorpus(t *testing.T) {
	t.Parallel()

	entries, err := loadDefinitionSummaries(loader.New(t.TempDir()), nil)
	if err != nil {
		t.Fatalf("loadDefinitionSummaries error = %v, want nil", err)
	}
	if len(entries) == 0 {
		t.Fatal("no definitions loaded")
	}
	for _, e := range entries {
		if e.Error != "" {
			t.Errorf("%q reported as failed on a clean corpus: %s", e.ID, e.Error)
		}
	}
}

func findEntry(entries []definitionEntry, id string) (definitionEntry, bool) {
	for _, e := range entries {
		if e.ID == id {
			return e, true
		}
	}
	return definitionEntry{}, false
}

func countEntries(entries []definitionEntry, id string) int {
	n := 0
	for _, e := range entries {
		if e.ID == id {
			n++
		}
	}
	return n
}
