package app

import (
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/definitions"
)

// TestDefsFingerprints_Deterministic proves the same inputs (the fixed embedded
// vendor snapshot + an unchanged dropin dir) hash to the same per-definition map
// across two computations.
func TestDefsFingerprints_Deterministic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fp1, err := defsFingerprints(dir)
	if err != nil {
		t.Fatalf("defsFingerprints: %v", err)
	}
	fp2, err := defsFingerprints(dir)
	if err != nil {
		t.Fatalf("defsFingerprints: %v", err)
	}
	if len(fp1) == 0 {
		t.Fatal("no vendored definitions hashed")
	}
	if !maps.Equal(fp1, fp2) {
		t.Error("fingerprints not deterministic across two computations")
	}
}

// TestDefsFingerprints_MissingDropinDirContributesNothing proves a nonexistent
// dropin dir yields the same map as an existing-but-empty one — both contribute
// nothing beyond the embedded vendor snapshot.
func TestDefsFingerprints_MissingDropinDirContributesNothing(t *testing.T) {
	t.Parallel()
	present, err := defsFingerprints(t.TempDir())
	if err != nil {
		t.Fatalf("defsFingerprints(present empty dir): %v", err)
	}
	missing, err := defsFingerprints(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("defsFingerprints(missing dir): %v", err)
	}
	if !maps.Equal(present, missing) {
		t.Error("missing dropin dir hashed differently from an empty one; both must contribute nothing")
	}
}

// TestDefsFingerprints_OnlyTheTouchedDefinitionChanges is the whole point of the
// per-definition map (autobrr/harbrr#388): a dropin add, content edit and remove
// each move ONLY that definition's entry, leaving every other id byte-identical.
func TestDefsFingerprints_OnlyTheTouchedDefinitionChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base, err := defsFingerprints(dir)
	if err != nil {
		t.Fatalf("defsFingerprints base: %v", err)
	}

	file := filepath.Join(dir, "custom.yml")
	if err := os.WriteFile(file, []byte("id: custom\n"), 0o600); err != nil {
		t.Fatalf("write dropin file: %v", err)
	}
	added, err := defsFingerprints(dir)
	if err != nil {
		t.Fatalf("defsFingerprints added: %v", err)
	}
	if _, ok := added["custom"]; !ok {
		t.Error("adding a dropin definition did not add its fingerprint")
	}
	assertOnlyChanged(t, base, added, "custom")

	if err := os.WriteFile(file, []byte("id: custom\nname: edited\n"), 0o600); err != nil {
		t.Fatalf("edit dropin file: %v", err)
	}
	edited, err := defsFingerprints(dir)
	if err != nil {
		t.Fatalf("defsFingerprints edited: %v", err)
	}
	if edited["custom"] == added["custom"] {
		t.Error("editing a dropin definition's content did not change its fingerprint")
	}
	assertOnlyChanged(t, added, edited, "custom")

	if err := os.Remove(file); err != nil {
		t.Fatalf("remove dropin file: %v", err)
	}
	removed, err := defsFingerprints(dir)
	if err != nil {
		t.Fatalf("defsFingerprints removed: %v", err)
	}
	if !maps.Equal(removed, base) {
		t.Error("removing the dropin definition did not restore the base map")
	}
}

// TestDefsFingerprints_DropinShadowsVendored proves dropin precedence: a dropin
// file replaces the vendored definition of the same id (one entry, the dropin's
// hash), and a dropin byte-identical to the vendored file it shadows is not a
// change at all.
func TestDefsFingerprints_DropinShadowsVendored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base, err := defsFingerprints(dir)
	if err != nil {
		t.Fatalf("defsFingerprints base: %v", err)
	}
	id, vendored := anyVendoredDefinition(t)
	if _, ok := base[id]; !ok {
		t.Fatalf("vendored definition %q missing from the base map", id)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".yml"), vendored, 0o600); err != nil {
		t.Fatalf("write identical dropin: %v", err)
	}
	identical, err := defsFingerprints(dir)
	if err != nil {
		t.Fatalf("defsFingerprints identical dropin: %v", err)
	}
	if !maps.Equal(identical, base) {
		t.Error("a dropin byte-identical to the definition it shadows must not read as a change")
	}

	if err := os.WriteFile(filepath.Join(dir, id+".yml"), append(vendored, '\n'), 0o600); err != nil {
		t.Fatalf("write differing dropin: %v", err)
	}
	shadowed, err := defsFingerprints(dir)
	if err != nil {
		t.Fatalf("defsFingerprints differing dropin: %v", err)
	}
	if shadowed[id] == base[id] {
		t.Error("a dropin overriding a vendored definition must change that definition's fingerprint")
	}
	assertOnlyChanged(t, base, shadowed, id)
}

// assertOnlyChanged fails unless before and after differ in exactly the given id.
func assertOnlyChanged(t *testing.T, before, after map[string]string, id string) {
	t.Helper()
	for k, v := range after {
		if k != id && before[k] != v {
			t.Errorf("definition %q changed, want only %q to change", k, id)
		}
	}
	for k := range before {
		if k == id {
			continue
		}
		if _, ok := after[k]; !ok {
			t.Errorf("definition %q disappeared, want only %q to change", k, id)
		}
	}
}

// anyVendoredDefinition returns the id and bytes of the first vendored definition
// in the embedded snapshot — the test needs a real vendored file to shadow, and
// naming a specific tracker would break whenever that one is retired.
func anyVendoredDefinition(t *testing.T) (id string, data []byte) {
	t.Helper()
	entries, err := definitions.Vendored.ReadDir(vendorDefsDir)
	if err != nil {
		t.Fatalf("read vendored definitions: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		data, err := definitions.Vendored.ReadFile(path.Join(vendorDefsDir, e.Name()))
		if err != nil {
			t.Fatalf("read vendored definition %q: %v", e.Name(), err)
		}
		return strings.TrimSuffix(e.Name(), ".yml"), data
	}
	t.Fatal("no vendored definitions embedded")
	return "", nil
}
