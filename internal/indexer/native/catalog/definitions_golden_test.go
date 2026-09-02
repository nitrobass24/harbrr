package catalog

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// update regenerates the definitions golden:
// `go test ./internal/indexer/native/catalog -update`. Regenerate only for a
// deliberate change to what a family declares — a refactor must leave the
// golden byte-identical.
var update = flag.Bool("update", false, "update the definitions golden file")

// definitionSnapshot is the golden's view of one family's Definition: every
// field a native family declares. The loader structs are marshaled directly
// (all relevant fields are exported); the one exception is Caps.Categories
// (CategoriesBlock), whose fields are unexported so it always marshals as {} —
// harmless, since no native family populates it (they use CategoryMappings).
type definitionSnapshot struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Language     string                 `json:"language"`
	Type         string                 `json:"type"`
	Encoding     string                 `json:"encoding"`
	Links        []string               `json:"links"`
	RequestDelay *float64               `json:"requestDelay"`
	Settings     []loader.SettingsField `json:"settings"`
	Caps         loader.Caps            `json:"caps"`
}

// TestDefinitionsGolden diffs every catalog family's Definition against the
// recorded snapshot, sorted by family id. It is the standing gate that a
// declaration refactor changed no family's declared data.
func TestDefinitionsGolden(t *testing.T) {
	t.Parallel()

	fams := All()
	ids := make([]string, 0, len(fams))
	for id := range fams {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	snaps := make([]definitionSnapshot, 0, len(ids))
	for _, id := range ids {
		d := fams[id].Definition
		snaps = append(snaps, definitionSnapshot{
			ID:           d.ID,
			Name:         d.Name,
			Description:  d.Description,
			Language:     d.Language,
			Type:         d.Type,
			Encoding:     d.Encoding,
			Links:        d.Links,
			RequestDelay: d.RequestDelay,
			Settings:     d.Settings,
			Caps:         d.Caps,
		})
	}

	got, err := json.MarshalIndent(snaps, "", "  ")
	if err != nil {
		t.Fatalf("marshaling definitions: %v", err)
	}
	got = append(got, '\n')

	golden := filepath.Join("testdata", "definitions.golden.json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o700); err != nil {
			t.Fatalf("creating testdata dir: %v", err)
		}
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading golden (run with -update to generate): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("definitions diverge from %s — a declaration changed; diff the file (re-run with -update only for a deliberate change):\n%s", golden, firstDiff(got, want))
	}
}

// firstDiff returns a short context window around the first differing byte, so
// a golden failure points at the family that changed instead of dumping 20
// definitions.
func firstDiff(got, want []byte) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	start := max(i-200, 0)
	end := func(b []byte) int {
		if i+200 < len(b) {
			return i + 200
		}
		return len(b)
	}
	return "got:  …" + string(got[start:end(got)]) + "…\nwant: …" + string(want[start:end(want)]) + "…"
}
