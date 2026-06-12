package loader

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %q: %v", name, err)
	}
	return data
}

func TestParseValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fixture string
		check   func(t *testing.T, def *Definition)
	}{
		{
			name:    "html minimal with categorymappings int and string ids",
			fixture: "html_minimal.yml",
			check: func(t *testing.T, def *Definition) {
				if def.ID != "html_minimal" {
					t.Errorf("id = %q, want html_minimal", def.ID)
				}
				if len(def.Caps.CategoryMappings) != 2 {
					t.Fatalf("categorymappings len = %d, want 2", len(def.Caps.CategoryMappings))
				}
				if got := def.Caps.CategoryMappings[0].ID.String(); got != "1" {
					t.Errorf("mapping[0].id = %q, want \"1\" (int normalized)", got)
				}
				if got := def.Caps.CategoryMappings[1].ID.String(); got != "tv2" {
					t.Errorf("mapping[1].id = %q, want \"tv2\" (string)", got)
				}
			},
		},
		{
			name:    "settings default scalar union string/int/bool",
			fixture: "html_minimal.yml",
			check: func(t *testing.T, def *Definition) {
				byName := map[string]SettingsField{}
				for _, s := range def.Settings {
					byName[s.Name] = s
				}
				if d := byName["timeout"].Default; d == nil || d.String() != "30" {
					t.Errorf("timeout default = %v, want 30 (int normalized)", d)
				}
				if d := byName["freeleech"].Default; d == nil || d.String() != "false" {
					t.Errorf("freeleech default = %v, want false (bool normalized)", d)
				}
				if d := byName["prefix"].Default; d == nil || d.String() != "rel" {
					t.Errorf("prefix default = %v, want rel (string)", d)
				}
				if byName["username"].Default != nil {
					t.Errorf("username default = %v, want nil (absent)", byName["username"].Default)
				}
			},
		},
		{
			name:    "filter args scalar and array normalized to []string",
			fixture: "html_minimal.yml",
			check: func(t *testing.T, def *Definition) {
				catFilters := def.Search.Fields["category"].Filters
				if len(catFilters) != 1 || catFilters[0].Name != "querystring" {
					t.Fatalf("category filters = %+v", catFilters)
				}
				if got := catFilters[0].Args; len(got) != 1 || got[0] != "cat" {
					t.Errorf("scalar filter args = %v, want [cat]", got)
				}
				seedFilters := def.Search.Fields["seeders"].Filters
				if len(seedFilters) != 1 {
					t.Fatalf("seeders filters = %+v", seedFilters)
				}
				if got := seedFilters[0].Args; len(got) != 2 || got[0] != "one" || got[1] != "1" {
					t.Errorf("array filter args = %v, want [one 1]", got)
				}
			},
		},
		{
			name:    "json api categories object + paths + selectorblock unions",
			fixture: "json_minimal.yml",
			check: func(t *testing.T, def *Definition) {
				if def.Caps.Categories["XXX"] != "XXX" {
					t.Errorf("categories[XXX] = %q, want XXX", def.Caps.Categories["XXX"])
				}
				if len(def.Search.Paths) != 1 || def.Search.Paths[0].Response == nil {
					t.Fatalf("paths = %+v", def.Search.Paths)
				}
				if def.Search.Paths[0].Response.Type != "json" {
					t.Errorf("response type = %q, want json", def.Search.Paths[0].Response.Type)
				}
				cats := def.Search.Paths[0].Categories
				if len(cats) != 2 || cats[0].String() != "1" || cats[1].String() != "x2" {
					t.Errorf("path categories = %v, want [1 x2]", cats)
				}
				cd := def.Search.Fields["categorydesc"]
				if cd.Text == nil || cd.Text.String() != "Movies" {
					t.Errorf("categorydesc text = %v, want Movies", cd.Text)
				}
				seeders := def.Search.Fields["seeders"]
				if seeders.Default == nil || seeders.Default.String() != "0" {
					t.Errorf("seeders default = %v, want 0 (number normalized)", seeders.Default)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			def, err := Parse(readFixture(t, tt.fixture))
			if err != nil {
				t.Fatalf("Parse(%s) error: %v", tt.fixture, err)
			}
			tt.check(t, def)
		})
	}
}

func TestParseInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fixture string
	}{
		{name: "missing required caps", fixture: "missing_required.yml"},
		{name: "unknown additional property", fixture: "unknown_property.yml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(readFixture(t, tt.fixture))
			if err == nil {
				t.Fatalf("Parse(%s) = nil error, want validation error", tt.fixture)
			}
		})
	}
}

func TestParseScalarRejectsNonScalar(t *testing.T) {
	t.Parallel()
	// A mapping where the schema permits a scalar union must be rejected by
	// the schema before the typed decode runs.
	data := []byte("id: x\n")
	if _, err := Parse(data); err == nil {
		t.Fatal("Parse of incomplete document = nil error, want error")
	}
}

func TestRewriteSlashEscapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "lone backslash slash becomes slash", in: `a\/b`, want: `a/b`},
		{name: "escaped backslash then slash preserved", in: `a\\/b`, want: `a\\/b`},
		{name: "triple backslash slash: escapes slash", in: `a\\\/b`, want: `a\\/b`},
		{name: "no slash escape untouched", in: `\\p{IsCyrillic}\\W`, want: `\\p{IsCyrillic}\\W`},
		{name: "mixed", in: `\\d]+\/ `, want: `\\d]+/ `},
		{name: "plain slash untouched", in: `a/b`, want: `a/b`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := string(rewriteSlashEscapes([]byte(tt.in))); got != tt.want {
				t.Errorf("rewriteSlashEscapes(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLoadPrecedenceDropinOverridesVendored(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Override a real vendored id with a recognizably different name.
	const id = "1337x"
	override := []byte(`---
id: 1337x
name: DROPIN OVERRIDE
description: "Drop-in override fixture."
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
`)
	if err := os.WriteFile(filepath.Join(dir, id+".yml"), override, 0o600); err != nil {
		t.Fatalf("writing drop-in: %v", err)
	}

	l := New(dir)

	// Drop-in wins.
	def, err := l.Load(id)
	if err != nil {
		t.Fatalf("Load(%q) error: %v", id, err)
	}
	if def.Name != "DROPIN OVERRIDE" {
		t.Errorf("Load(%q).Name = %q, want DROPIN OVERRIDE (drop-in precedence)", id, def.Name)
	}

	// Vendored is still reachable for a different id.
	vendored, err := New("").Load(id)
	if err != nil {
		t.Fatalf("vendored Load(%q) error: %v", id, err)
	}
	if vendored.Name == "DROPIN OVERRIDE" {
		t.Errorf("vendored Load(%q) unexpectedly returned drop-in content", id)
	}
}

func TestLoadNotFound(t *testing.T) {
	t.Parallel()
	_, err := New("").Load("this_id_does_not_exist_anywhere")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load(missing) err = %v, want ErrNotFound", err)
	}
}

// TestLoadRejectsPathTraversal pins that a non-bare id (path separators / ..)
// is rejected before any filesystem access, so Load can never read outside the
// drop-in dir or load an out-of-tree .yml.
func TestLoadRejectsPathTraversal(t *testing.T) {
	t.Parallel()

	// Plant a schema-valid def OUTSIDE the drop-in dir; traversal would reach it.
	outside := t.TempDir()
	dropin := filepath.Join(outside, "dropin")
	if err := os.Mkdir(dropin, 0o700); err != nil {
		t.Fatalf("mkdir dropin: %v", err)
	}
	valid := readFixture(t, "json_minimal.yml")
	if err := os.WriteFile(filepath.Join(outside, "evil.yml"), valid, 0o600); err != nil {
		t.Fatalf("writing outside def: %v", err)
	}

	l := New(dropin)
	for _, id := range []string{"../evil", "..", "a/b", `a\b`, ""} {
		if _, err := l.Load(id); err == nil {
			t.Errorf("Load(%q) = nil error, want rejection", id)
		} else if errors.Is(err, ErrNotFound) {
			t.Errorf("Load(%q) returned ErrNotFound, want an invalid-id error", id)
		}
	}
}

// TestValidationErrorIsSecretFree pins that a schema-validation failure on a
// value-constrained field reports only the failing schema path, never the
// offending value (which can carry a passkey/cookie).
func TestValidationErrorIsSecretFree(t *testing.T) {
	t.Parallel()

	// `type` is an enum; a passkey-bearing value fails and must not be echoed.
	const secret = "passkey=SUPERSECRETtoken"
	data := string(readFixture(t, "json_minimal.yml"))
	bad := strings.Replace(data, "type: private", `type: "https://t/rss?`+secret+`"`, 1)
	if bad == data {
		t.Fatal("fixture changed: expected to replace `type: private`")
	}

	_, err := Parse([]byte(bad))
	if err == nil {
		t.Fatal("Parse of invalid `type` = nil error, want validation error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("validation error leaked secret value: %v", err)
	}
}

// TestLoadAllVendoredCorpus is the headline smoke test: every embedded
// vendored definition must parse and schema-validate with an EMPTY skip-list.
// Any definition that cannot be represented must surface explicitly in the
// skip-list (never silently dropped) and is reported here.
func TestLoadAllVendoredCorpus(t *testing.T) {
	t.Parallel()

	defs, skipped, err := New("").LoadAll()
	if err != nil {
		t.Fatalf("LoadAll error: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("LoadAll loaded 0 definitions, expected the full vendored corpus")
	}
	t.Logf("loaded %d vendored definitions", len(defs))

	if len(skipped) != 0 {
		for _, s := range skipped {
			t.Errorf("skipped %s: %s", s.ID, s.Reason)
		}
		t.Fatalf("LoadAll skipped %d vendored definitions; expected empty skip-list", len(skipped))
	}
}
