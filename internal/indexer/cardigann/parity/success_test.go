package parity

import "testing"

// matrixArchetypes is the offline compatibility matrix (docs/plan.md, Phase 2): the
// gate requires at least one passing fixture per row.
var matrixArchetypes = []string{
	"html-form-login",
	"html-cookie-login",
	"json-api",
	"xml-newznab",
	"non-latin-regexp2",
	"freeleech",
	"multi-category",
	"date-heavy",
	"magnet-only",
	"download-link-prerequest",
	"redirect-follow",
	"redirect-relogin",
	"mixed-response-type",
}

// minFixtures is the Definition-of-done bar: match Jackett on >=25 saved-response
// fixtures spanning the matrix.
const minFixtures = 25

// TestSuccessCriteria_Coverage is the offline parity gate's bookkeeping: every
// compatibility-matrix archetype is exercised by at least one case, the corpus
// has at least 25 fixtures, and every case declares a valid golden_source
// provenance. It does NOT re-run the engine — TestParity already proves each case
// matches its golden; this asserts the corpus is broad and honest, so a passing
// suite is a meaningful gate rather than a handful of fixtures.
func TestSuccessCriteria_Coverage(t *testing.T) {
	t.Parallel()

	dirs, err := caseDirs()
	if err != nil {
		t.Fatalf("scanning cases: %v", err)
	}

	byArchetype := map[string]int{}
	bySource := map[string]int{}
	for _, dir := range dirs {
		// Load validates archetype + golden_source, so a successful load means the
		// case declares both (no silent gaps in the corpus metadata).
		c, err := Load(dir)
		if err != nil {
			t.Fatalf("loading %s: %v", dir, err)
		}
		byArchetype[c.Archetype]++
		bySource[c.GoldenSource]++
	}

	total := len(dirs)
	if total < minFixtures {
		t.Errorf("parity corpus has %d fixtures, want >= %d (Definition of done)", total, minFixtures)
	}

	for _, a := range matrixArchetypes {
		if byArchetype[a] == 0 {
			t.Errorf("compatibility-matrix archetype %q has no passing fixture", a)
		}
	}

	// The oracle anchors (jackett-port) must be present, so the corpus is tied to
	// Jackett's own assertions and not only to hand-derived goldens.
	if bySource[SourceJackettPort] < 2 {
		t.Errorf("jackett-port fixtures = %d, want >= 2 (the HTML + JSON oracles)", bySource[SourceJackettPort])
	}

	t.Logf("parity corpus: %d fixtures across %d archetypes (%d jackett-port, %d hand-derived)",
		total, len(byArchetype), bySource[SourceJackettPort], bySource[SourceHandDerived])
}
