package cardigann

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestEngineConstructionCensus is the headline corpus gate: every vendored
// definition must build an Engine without error, proving all nine stage seams
// wire across the whole corpus (mapper caps/category map, dateparse parser,
// filter registry date/language seams, selector eval binding, normalizer
// base-URL/type/category map, login client/base-URL/config). A construction
// failure is surfaced loudly (never silently skipped) with the offending def id.
//
// This does NOT execute searches — it asserts assembly. Per-def-vs-Jackett
// output is the parity corpus's job, not this census.
func TestEngineConstructionCensus(t *testing.T) {
	t.Parallel()

	defs, skipped, err := loader.New("").LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("LoadAll returned no definitions")
	}

	var failures []string
	for _, def := range defs {
		if _, err := NewEngine(def, WithClock(fixedClock())); err != nil {
			failures = append(failures, def.ID+": "+err.Error())
		}
	}

	t.Logf("engine construction census: %d definitions constructed, %d loader-skipped", len(defs), len(skipped))

	// A loader-skipped def never reaches NewEngine, so "0 failures" would be a
	// false gate if any def silently failed to load. Enforce the whole corpus is
	// covered: the construction census is only meaningful over every vendored def.
	if len(skipped) > 0 {
		for _, s := range skipped {
			t.Errorf("loader skipped %s: %s", s.ID, s.Reason)
		}
		t.Fatalf("%d definitions were loader-skipped and never reached NewEngine", len(skipped))
	}

	if len(failures) > 0 {
		for _, f := range failures {
			t.Errorf("NewEngine failed: %s", f)
		}
		t.Fatalf("%d definitions failed to construct an Engine", len(failures))
	}
}
