package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDefsFingerprint_Deterministic proves the same inputs (the fixed embedded
// vendor snapshot + an unchanged dropin dir) hash to the same fingerprint across
// two computations.
func TestDefsFingerprint_Deterministic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fp1, err := defsFingerprint(dir)
	if err != nil {
		t.Fatalf("defsFingerprint: %v", err)
	}
	fp2, err := defsFingerprint(dir)
	if err != nil {
		t.Fatalf("defsFingerprint: %v", err)
	}
	if fp1 != fp2 {
		t.Errorf("fingerprint not deterministic: %q != %q", fp1, fp2)
	}
}

// TestDefsFingerprint_MissingDropinDirContributesNothing proves a nonexistent
// dropin dir hashes identically to an existing-but-empty one — both contribute
// nothing beyond the embedded vendor snapshot.
func TestDefsFingerprint_MissingDropinDirContributesNothing(t *testing.T) {
	t.Parallel()
	present := t.TempDir()
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	fpPresent, err := defsFingerprint(present)
	if err != nil {
		t.Fatalf("defsFingerprint(present empty dir): %v", err)
	}
	fpMissing, err := defsFingerprint(missing)
	if err != nil {
		t.Fatalf("defsFingerprint(missing dir): %v", err)
	}
	if fpPresent != fpMissing {
		t.Errorf("missing dropin dir fp %q != empty dropin dir fp %q, want equal (both contribute nothing)", fpMissing, fpPresent)
	}
}

// TestDefsFingerprint_ChangesOnDropinAddEditRemove proves each of a dropin file
// add, content edit, and remove changes the fingerprint, and removing it restores
// the original (empty-dir) baseline.
func TestDefsFingerprint_ChangesOnDropinAddEditRemove(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base, err := defsFingerprint(dir)
	if err != nil {
		t.Fatalf("defsFingerprint base: %v", err)
	}

	path := filepath.Join(dir, "custom.yml")
	if err := os.WriteFile(path, []byte("id: custom\n"), 0o600); err != nil {
		t.Fatalf("write dropin file: %v", err)
	}
	added, err := defsFingerprint(dir)
	if err != nil {
		t.Fatalf("defsFingerprint added: %v", err)
	}
	if added == base {
		t.Error("adding a dropin file did not change the fingerprint")
	}

	if err := os.WriteFile(path, []byte("id: custom\nname: edited\n"), 0o600); err != nil {
		t.Fatalf("edit dropin file: %v", err)
	}
	edited, err := defsFingerprint(dir)
	if err != nil {
		t.Fatalf("defsFingerprint edited: %v", err)
	}
	if edited == added {
		t.Error("editing a dropin file's content did not change the fingerprint")
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove dropin file: %v", err)
	}
	removed, err := defsFingerprint(dir)
	if err != nil {
		t.Fatalf("defsFingerprint removed: %v", err)
	}
	if removed != base {
		t.Errorf("removing the dropin file did not restore the base fingerprint: %q != %q", removed, base)
	}
}

// TestDefsFingerprint_RecordFramingUnambiguous pins the length-prefixed record
// framing: under a bare NUL-separated stream, the two-file tree {a:"", b:"hello"}
// and the one-file tree {a:"b\x00hello"} serialize to the identical byte sequence
// ("a\x00b\x00hello") and would collide without any hash break. Length prefixes
// make the trees hash differently.
func TestDefsFingerprint_RecordFramingUnambiguous(t *testing.T) {
	t.Parallel()

	two := t.TempDir()
	if err := os.WriteFile(filepath.Join(two, "a"), nil, 0o600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(two, "b"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write b: %v", err)
	}

	one := t.TempDir()
	if err := os.WriteFile(filepath.Join(one, "a"), []byte("b\x00hello"), 0o600); err != nil {
		t.Fatalf("write crafted a: %v", err)
	}

	fpTwo, err := defsFingerprint(two)
	if err != nil {
		t.Fatalf("fingerprint two-file tree: %v", err)
	}
	fpOne, err := defsFingerprint(one)
	if err != nil {
		t.Fatalf("fingerprint one-file tree: %v", err)
	}
	if fpTwo == fpOne {
		t.Fatalf("fingerprints collide (%s): record framing is ambiguous", fpTwo)
	}
}
