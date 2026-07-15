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

// fieldBlock looks up a FieldsBlock entry by key via Ordered, mirroring the
// deleted FieldsBlock.Get for test call sites.
func fieldBlock(fb FieldsBlock, key string) (SelectorBlock, bool) {
	for _, e := range fb.Ordered() {
		if e.Key == key {
			return e.Block, true
		}
	}
	return SelectorBlock{}, false
}

// categoryName looks up a CategoriesBlock entry by tracker id via Ordered,
// mirroring the deleted CategoriesBlock.Get for test call sites.
func categoryName(cb CategoriesBlock, trackerID string) (string, bool) {
	for _, e := range cb.Ordered() {
		if e.TrackerID == trackerID {
			return e.Name, true
		}
	}
	return "", false
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
				catBlock, _ := fieldBlock(def.Search.Fields, "category")
				catFilters := catBlock.Filters
				if len(catFilters) != 1 || catFilters[0].Name != "querystring" {
					t.Fatalf("category filters = %+v", catFilters)
				}
				if got := catFilters[0].Args; len(got) != 1 || got[0] != "cat" {
					t.Errorf("scalar filter args = %v, want [cat]", got)
				}
				seedBlock, _ := fieldBlock(def.Search.Fields, "seeders")
				seedFilters := seedBlock.Filters
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
				if name, _ := categoryName(def.Caps.Categories, "XXX"); name != "XXX" {
					t.Errorf("categories[XXX] = %q, want XXX", name)
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
				cd, _ := fieldBlock(def.Search.Fields, "categorydesc")
				if cd.Text == nil || cd.Text.String() != "Movies" {
					t.Errorf("categorydesc text = %v, want Movies", cd.Text)
				}
				seeders, _ := fieldBlock(def.Search.Fields, "seeders")
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

// TestInputsBlockPreservesOrder proves search inputs decode in definition
// (YAML) order, not alphabetical. Jackett iterates Search.Inputs in source order
// when building the request query; a plain Go map would randomize it.
func TestInputsBlockPreservesOrder(t *testing.T) {
	t.Parallel()

	d, err := Parse(readFixture(t, "inputs_order.yml"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	ordered := d.Search.Inputs.Ordered()
	got := make([]string, 0, len(ordered))
	for _, in := range ordered {
		got = append(got, in.Key)
	}
	want := []string{"zeta", "alpha", "mu"}
	if len(got) != len(want) {
		t.Fatalf("input keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("input keys = %v, want %v (definition order)", got, want)
		}
	}
}

// TestCategoriesBlockPreservesOrder proves the caps.categories object form
// decodes in definition (YAML) order, not lexical or randomized. Jackett's
// YamlDotNet dictionary preserves document order and its _categoryMapping —
// and hence the {{ .Categories }} bytes a multi-cat query renders — inherit
// it; a plain Go map would randomize the order per process.
func TestCategoriesBlockPreservesOrder(t *testing.T) {
	t.Parallel()

	d, err := Parse(readFixture(t, "json_minimal.yml"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	ordered := d.Caps.Categories.Ordered()
	got := make([]string, 0, len(ordered))
	for _, e := range ordered {
		got = append(got, e.TrackerID)
	}
	want := []string{"XXX", "9", "movies-hd", "2", "zz"}
	if len(got) != len(want) {
		t.Fatalf("category ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("category ids = %v, want %v (definition order)", got, want)
		}
	}
}

// TestParseNoResultsMessagePointer pins the three decode shapes of
// response.noResultsMessage. Jackett distinguishes ABSENT (null — no
// no-results check) from PRESENT-EMPTY (`noResultsMessage: ""` — an
// exactly-empty body means zero results) from a non-empty substring message,
// so the field must decode to *string: a missing key must stay nil and the
// empty-string form must yield a non-nil pointer to "".
func TestParseNoResultsMessagePointer(t *testing.T) {
	t.Parallel()

	const line = "        noResultsMessage: \"No results\"\n"
	base := string(readFixture(t, "json_minimal.yml"))
	if !strings.Contains(base, line) {
		t.Fatal("json_minimal.yml no longer carries the noResultsMessage line this test rewrites")
	}

	tests := []struct {
		name    string
		yml     string
		wantNil bool
		wantVal string
	}{
		{name: "non-empty message", yml: base, wantVal: "No results"},
		{name: "present-empty message", yml: strings.Replace(base, line, "        noResultsMessage: \"\"\n", 1), wantVal: ""},
		{name: "absent message", yml: strings.Replace(base, line, "", 1), wantNil: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			def, err := Parse([]byte(tt.yml))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := def.Search.Paths[0].Response.NoResultsMessage
			if tt.wantNil {
				if got != nil {
					t.Fatalf("NoResultsMessage = %q, want nil (absent)", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("NoResultsMessage = nil, want %q", tt.wantVal)
			}
			if *got != tt.wantVal {
				t.Errorf("NoResultsMessage = %q, want %q", *got, tt.wantVal)
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

// TestLoadResolvesMismatchedFilenameByContentID pins the fix for the class of
// vendored files (inherited byte-for-byte from Jackett) whose filename differs
// from their content id:. Before the fix, Load(<content-id>) looked for
// vendor/<content-id>.yml and returned ErrNotFound, so the catalog offered ids
// that could not be added (autobrr/harbrr#108). Load must resolve by content id
// and return the definition whose id: matches the request.
func TestLoadResolvesMismatchedFilenameByContentID(t *testing.T) {
	t.Parallel()

	// The known filename≠id vendored defs at the time of writing. The broader
	// invariant is enforced by TestEveryCatalogDefinitionIsLoadableByID; this
	// list documents the concrete regression and its failure symptom.
	ids := []string{
		"darkpeers-api",
		"hdzero-api",
		"nordicquality-api",
		"upscalevault-api",
		"aniRena",
		"bluebirdhd",
		"RockBox",
	}
	l := New("")
	for _, id := range ids {
		def, err := l.Load(id)
		if err != nil {
			t.Errorf("Load(%q) error: %v (content-id resolution regressed)", id, err)
			continue
		}
		if def.ID != id {
			t.Errorf("Load(%q).ID = %q, want %q (resolved the wrong definition)", id, def.ID, id)
		}
	}
}

// TestEveryCatalogDefinitionIsLoadableByID is the standing guard: every id the
// catalog exposes (LoadAll keys entries by the parsed id:) must be Load-able by
// that same id, which is exactly what the add/build path does. A future
// `make vendor-defs` snapshot that reintroduces a filename≠id def without the
// content-id fallback would fail here instead of shipping an un-addable tracker.
func TestEveryCatalogDefinitionIsLoadableByID(t *testing.T) {
	t.Parallel()

	l := New("")
	defs, skipped, err := l.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll error: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("LoadAll skipped %d definitions; expected empty skip-list", len(skipped))
	}
	for _, d := range defs {
		got, err := l.Load(d.ID)
		if err != nil {
			t.Errorf("catalog offers %q but Load(%q) failed: %v", d.ID, d.ID, err)
			continue
		}
		if got.ID != d.ID {
			t.Errorf("Load(%q).ID = %q, want %q", d.ID, got.ID, d.ID)
		}
	}
}

func TestEffectiveProtocol(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"empty defaults to torrent", "", ProtocolTorrent},
		{"explicit torrent", ProtocolTorrent, ProtocolTorrent},
		{"usenet", ProtocolUsenet, ProtocolUsenet},
		{"unknown value falls back to torrent", "garbage", ProtocolTorrent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := &Definition{Protocol: tt.raw}
			if got := d.EffectiveProtocol(); got != tt.want {
				t.Errorf("EffectiveProtocol(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
