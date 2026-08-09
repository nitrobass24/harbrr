package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validDropinDef is a schema-valid minimal HTML Cardigann definition used as
// the "good" drop-in. Its id is deliberately non-colliding with any vendored
// id so LoadAll treats it as additive rather than an override.
const validDropinDef = `---
id: zzz-u1f5-valid
name: U1F5 Valid
description: "U1F5 valid drop-in fixture."
language: en-US
type: public
encoding: UTF-8
links:
  - https://example.invalid/

caps:
  categories:
    XXX: XXX
  modes:
    search: [q]

search:
  path: /search
  rows:
    selector: tr
  fields:
    title:
      selector: a
    category:
      selector: a.cat
    download:
      selector: a.dl
    size:
      selector: td.size
    seeders:
      selector: td.seeders
`

// brokenDropinDef is malformed YAML (an unclosed flow sequence), the simplest
// reliable Parse failure. It is used as the "bad" drop-in.
const brokenDropinDef = "id: zzz-u1f5-broken\nlinks: [unclosed\n"

// TestLoadAllSkipsBrokenDropin proves LoadAll routes a per-definition failure
// into the skip-list (visible, with a usable id and reason) instead of
// aborting the whole load or silently dropping the entry. No vendored call site
// exercises a non-empty skip-list — every existing LoadAll test asserts an
// EMPTY one — so a regression in the per-def error routing (returning the error
// instead of skipping, or dropping the entry) would otherwise pass the suite.
func TestLoadAllSkipsBrokenDropin(t *testing.T) {
	t.Parallel()

	const (
		validID  = "zzz-u1f5-valid"
		brokenID = "zzz-u1f5-broken"
	)

	dir := t.TempDir()
	writeDropin(t, dir, validID, validDropinDef)
	writeDropin(t, dir, brokenID, brokenDropinDef)

	// Sanity: Load itself must succeed for the valid def and fail for the
	// broken one, so the skip below is caused by the routing under test and not
	// by an unexpectedly-parseable "broken" fixture.
	l := New(dir)
	if _, err := l.Load(validID); err != nil {
		t.Fatalf("Load(%q) = %v, want nil (valid fixture)", validID, err)
	}
	if _, err := l.Load(brokenID); err == nil {
		t.Fatalf("Load(%q) = nil error, want a parse failure (broken fixture)", brokenID)
	}

	defs, skipped, err := l.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll error = %v, want nil (a per-def failure must not fail LoadAll)", err)
	}

	// (b) the valid def loaded despite the broken sibling.
	if !containsDefID(defs, validID) {
		t.Errorf("LoadAll defs missing %q; a broken sibling must not prevent valid defs from loading", validID)
	}

	// (c) the broken def surfaced in the skip-list with its id and a reason.
	entry, ok := findSkip(skipped, brokenID)
	if !ok {
		t.Fatalf("LoadAll skipped-list missing %q; a broken def must be reported, not silently dropped", brokenID)
	}
	if entry.Reason == "" {
		t.Errorf("skip entry for %q has empty Reason; want the Load error text", brokenID)
	}
}

// schemaInvalidDropinDef is valid YAML that violates the Cardigann schema
// (links must be a sequence), so the skip reason carries the validator's
// instance pointer rather than a YAML syntax message.
const schemaInvalidDropinDef = "id: zzz-u1f5-schema\nname: U1F5 Schema\nlinks: 5\n"

// TestLoadAllSkipEntryOrigin proves a skipped definition says WHERE it came
// from, which is the whole diagnosis for the disappearing-tracker case: a
// drop-in shadows the vendored definition of the same id and never falls back
// to it, so a typo'd drop-in silently removes a working tracker. The skip must
// name the drop-in as the culprit and keep the schema validator's instance
// pointer in the reason.
func TestLoadAllSkipEntryOrigin(t *testing.T) {
	t.Parallel()

	// A vendored id, deliberately shadowed by a broken drop-in below.
	const vendoredID = "torrentleech"

	tests := []struct {
		name         string
		id           string
		body         string
		wantOrigin   Origin
		reasonSubstr string
	}{
		{
			name:         "broken yaml drop-in",
			id:           "zzz-u1f5-broken",
			body:         brokenDropinDef,
			wantOrigin:   OriginDropin,
			reasonSubstr: "drop-in",
		},
		{
			name:         "schema-invalid drop-in keeps the instance pointer",
			id:           "zzz-u1f5-schema",
			body:         schemaInvalidDropinDef,
			wantOrigin:   OriginDropin,
			reasonSubstr: "/links",
		},
		{
			name:         "broken drop-in shadowing a vendored id",
			id:           vendoredID,
			body:         brokenDropinDef,
			wantOrigin:   OriginDropin,
			reasonSubstr: "drop-in",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeDropin(t, dir, tt.id, tt.body)

			defs, skipped, err := New(dir).LoadAll()
			if err != nil {
				t.Fatalf("LoadAll error = %v, want nil", err)
			}
			if containsDefID(defs, tt.id) {
				t.Errorf("%q loaded; a failing drop-in must NOT fall back to the vendored definition", tt.id)
			}
			entry, ok := findSkip(skipped, tt.id)
			if !ok {
				t.Fatalf("LoadAll skipped-list missing %q", tt.id)
			}
			if entry.Origin != tt.wantOrigin {
				t.Errorf("skip origin = %q, want %q", entry.Origin, tt.wantOrigin)
			}
			if !strings.Contains(entry.Reason, tt.reasonSubstr) {
				t.Errorf("skip reason = %q, want it to contain %q", entry.Reason, tt.reasonSubstr)
			}
		})
	}

	// The vendored id really exists, so the case above is a shadowing failure
	// and not a typo'd fixture id that was never in the catalog.
	if _, err := New("").Load(vendoredID); err != nil {
		t.Fatalf("Load(%q) without drop-ins = %v, want nil (fixture id must be a real vendored definition)", vendoredID, err)
	}
	if got := New("").originOf(vendoredID); got != OriginVendored {
		t.Errorf("originOf(%q) with no drop-in dir = %q, want %q", vendoredID, got, OriginVendored)
	}
}

func writeDropin(t *testing.T, dir, id, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, id+".yml"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing drop-in %q: %v", id, err)
	}
}

func containsDefID(defs []*Definition, id string) bool {
	for _, d := range defs {
		if d.ID == id {
			return true
		}
	}
	return false
}

func findSkip(skipped []SkipEntry, id string) (SkipEntry, bool) {
	for _, s := range skipped {
		if s.ID == id {
			return s, true
		}
	}
	return SkipEntry{}, false
}
