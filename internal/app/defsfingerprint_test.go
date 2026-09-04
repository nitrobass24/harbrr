package app

import (
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/definitions"
)

// TestDefsFingerprints_Deterministic proves the same inputs (the fixed embedded
// vendor snapshot + an unchanged dropin dir) hash to the same per-definition map
// across two computations.
func TestDefsFingerprints_VendorWalkIsDeterministic(t *testing.T) {
	t.Parallel()

	// Deliberately calls hashDefs rather than defsFingerprints. The vendored half
	// of defsFingerprints is memoized per process, so calling it twice compares one
	// cached walk against a clone of itself and can only prove that copying a map
	// preserves it. Dropping to the uncached layer keeps the original property under
	// test: that walking the embedded snapshot twice yields identical hashes.
	first := map[string]string{}
	if err := hashDefs(first, definitions.Vendored, vendorDefsDir); err != nil {
		t.Fatalf("hashDefs: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("no vendored definitions hashed")
	}

	second := map[string]string{}
	if err := hashDefs(second, definitions.Vendored, vendorDefsDir); err != nil {
		t.Fatalf("hashDefs (second walk): %v", err)
	}

	if !maps.Equal(first, second) {
		t.Error("fingerprints not deterministic across two walks of the vendored snapshot")
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
	name, id, vendored := anyVendoredDefinition(t)
	if _, ok := base[id]; !ok {
		t.Fatalf("vendored definition %q missing from the base map", id)
	}
	if err := os.WriteFile(filepath.Join(dir, name), vendored, 0o600); err != nil {
		t.Fatalf("write identical dropin: %v", err)
	}
	identical, err := defsFingerprints(dir)
	if err != nil {
		t.Fatalf("defsFingerprints identical dropin: %v", err)
	}
	if !maps.Equal(identical, base) {
		t.Error("a dropin byte-identical to the definition it shadows must not read as a change")
	}

	if err := os.WriteFile(filepath.Join(dir, name), append(vendored, '\n'), 0o600); err != nil {
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

// TestHashDefs_KeysByContentID pins the key the map is built on: the definition's
// declared id:, NOT the filename. Instances store the content id in definition_id
// (that is what the catalog offers), and a handful of Jackett files are named
// differently from their id — darkpeers.yml carries darkpeers-api — so a
// filename-keyed map would never match those instances and would leave them
// serving stale-shape rows after a change. The fallback cases (unparseable YAML,
// no id:) must still produce a stable basename key rather than dropping the file.
func TestHashDefs_KeysByContentID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		file    string
		content string
		wantKey string
	}{
		{name: "filename matches id", file: "plain.yml", content: "---\nid: plain\nname: Plain\n", wantKey: "plain"},
		{name: "id differs from filename", file: "darkpeers.yml", content: "---\nid: darkpeers-api\nname: DarkPeers\n", wantKey: "darkpeers-api"},
		{
			name: "dotnet slash escape still resolves the id",
			file: "escaped.yml", wantKey: "escaped-api",
			content: "---\nid: escaped-api\nname: Escaped\nfilters:\n  - name: re_replace\n    args: [\"cat-(\\\\d+)\\/\", \"$1\"]\n",
		},
		{name: "unparseable falls back to the basename", file: "broken.yml", content: "\tid: [unclosed\n", wantKey: "broken"},
		{name: "missing id falls back to the basename", file: "noid.yml", content: "---\nname: No Id\n", wantKey: "noid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tt.file), []byte(tt.content), 0o600); err != nil {
				t.Fatalf("write %s: %v", tt.file, err)
			}
			got := map[string]string{}
			if err := hashDefs(got, os.DirFS(dir), "."); err != nil {
				t.Fatalf("hashDefs: %v", err)
			}
			if len(got) != 1 || got[tt.wantKey] == "" {
				t.Errorf("hashDefs keys = %v, want exactly %q", slices.Sorted(maps.Keys(got)), tt.wantKey)
			}
		})
	}
}

// TestDefsFingerprints_VendoredIDsAreLoadable proves every key the map produces
// over the real embedded snapshot is a definition id the loader can actually
// resolve — i.e. the same id an instance's definition_id holds. A filename-keyed
// map would fail this for the id-differs-from-filename files.
func TestDefsFingerprints_VendoredIDsAreLoadable(t *testing.T) {
	t.Parallel()
	fps, err := defsFingerprints(t.TempDir())
	if err != nil {
		t.Fatalf("defsFingerprints: %v", err)
	}
	l := loader.New("")
	for id := range fps {
		if _, err := l.Load(id); err != nil {
			t.Errorf("fingerprint key %q is not loadable as a definition id: %v", id, err)
		}
	}
}

// anyVendoredDefinition returns the filename, content id and bytes of the first
// vendored definition in the embedded snapshot — the test needs a real vendored
// file to shadow, and naming a specific tracker would break whenever that one is
// retired. Filename and id are returned separately because they differ for a
// handful of Jackett files.
func anyVendoredDefinition(t *testing.T) (name, id string, data []byte) {
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
		id := loader.ProbeID(data)
		if id == "" {
			id = strings.TrimSuffix(e.Name(), ".yml")
		}
		return e.Name(), id, data
	}
	t.Fatal("no vendored definitions embedded")
	return "", "", nil
}
