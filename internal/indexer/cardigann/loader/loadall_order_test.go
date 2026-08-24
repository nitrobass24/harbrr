package loader

import (
	"testing"
)

// TestLoadAllOrderIsDeterministic pins an ordering guarantee that LoadAll has
// always provided but nothing asserted.
//
// The order is NOT sorted by Definition.ID. allIDs sorts by the id derived from
// each *filename*, while the returned defs carry the id parsed from each file's
// content, and those differ wherever a definition's `id:` does not match its
// filename (animeworld.yml -> animeworld-api, anirena.yml -> aniRena). What
// LoadAll guarantees is that defs come back in allIDs order, which is stable
// across calls.
//
// That guarantee was free to lose. Shuffling LoadAll's output leaves every other
// test in this repo green: the one consumer that could notice re-sorts into
// byType groups before writing coverage.md, masking the change. It matters now
// that the per-id work runs concurrently, because a worker pool appending as
// results arrive would return definitions in *completion* order instead — a
// different sequence on every call, with nothing failing.
//
// Two calls is the cheapest assertion that catches exactly that: completion
// order varies run to run, allIDs order does not. It deliberately does not pin
// the sequence to a golden, which would fail on every legitimate re-vendor.
func TestLoadAllOrderIsDeterministic(t *testing.T) {
	t.Parallel()

	l := New("")

	first, _, err := l.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll error: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("LoadAll returned 0 definitions; expected the full vendored corpus")
	}

	second, _, err := l.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll (second call) error: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("LoadAll returned %d definitions then %d; the corpus is not stable across calls", len(first), len(second))
	}

	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("LoadAll order differs between calls at index %d: %q then %q", i, first[i].ID, second[i].ID)
		}
	}
}
