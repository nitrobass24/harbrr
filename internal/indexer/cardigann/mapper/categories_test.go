package mapper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestGetByNameSpotCheck spot-checks the name->id table across all eight
// category families plus a few subcategory edge cases.
func TestGetByNameSpotCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		wantID int
	}{
		{"Console", 1000},
		{"Console/PS4", 1180},
		{"Movies", 2000},
		{"Movies/WEB-DL", 2080},
		{"Audio", 3000},
		{"Audio/Foreign", 3060},
		{"PC", 4000},
		{"PC/Mobile-Android", 4070},
		{"TV", 5000},
		{"TV/UHD", 5045},
		{"XXX", 6000},
		{"XXX/WEB-DL", 6090},
		{"Books", 7000},
		{"Books/Foreign", 7060},
		{"Other", 8000},
		{"Other/Hashed", 8020},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, ok := GetByName(tt.name)
			if !ok {
				t.Fatalf("GetByName(%q) not found", tt.name)
			}
			if c.ID != tt.wantID {
				t.Errorf("GetByName(%q).ID = %d, want %d", tt.name, c.ID, tt.wantID)
			}
			back, ok := GetByID(tt.wantID)
			if !ok || back.Name != tt.name {
				t.Errorf("GetByID(%d) = (%v,%v), want name %q", tt.wantID, back, ok, tt.name)
			}
		})
	}
}

// TestTableMatchesSchemaEnum is the machine-checked parity guarantee: every name
// in the IndexerCategories enum in vendor/schema.json must resolve via
// GetByName, and the table must contain no extra names. Any gap fails the test.
func TestTableMatchesSchemaEnum(t *testing.T) {
	t.Parallel()

	enum := readSchemaEnum(t)
	if len(enum) == 0 {
		t.Fatal("IndexerCategories enum is empty")
	}

	enumSet := make(map[string]struct{}, len(enum))
	for _, name := range enum {
		enumSet[name] = struct{}{}
		if _, ok := GetByName(name); !ok {
			t.Errorf("schema enum name %q has no entry in the standard category table", name)
		}
	}

	for _, c := range StandardCategories() {
		if _, ok := enumSet[c.Name]; !ok {
			t.Errorf("table category %q (id %d) is not in the schema IndexerCategories enum", c.Name, c.ID)
		}
	}

	if len(enum) != len(StandardCategories()) {
		t.Errorf("table size %d != enum size %d", len(StandardCategories()), len(enum))
	}
}

func TestParentAndCustom(t *testing.T) {
	t.Parallel()

	hd, _ := GetByName("Movies/HD")
	if hd.IsParent() {
		t.Error("Movies/HD should not be a parent")
	}
	if hd.Parent() != "Movies" {
		t.Errorf("Movies/HD parent = %q, want Movies", hd.Parent())
	}
	movies, _ := GetByName("Movies")
	if !movies.IsParent() || movies.Parent() != "Movies" {
		t.Errorf("Movies should be its own parent")
	}
	if movies.IsCustom() {
		t.Error("a standard category must not report IsCustom")
	}
	custom := Category{ID: customCategoryID("nonnumeric"), Name: "Custom"}
	if !custom.IsCustom() {
		t.Error("a synthesised custom category must report IsCustom")
	}
}

// TestIsAdultCategory pins the XXX-family bounds behind the operator's
// hide-adult-categories setting: 6000 and its children in, the neighbouring
// families and a synthesised custom id out.
func TestIsAdultCategory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id   int
		want bool
	}{
		{5080, false}, // TV/Documentary — the family below
		{6000, true},  // XXX (family root)
		{6045, true},  // XXX/UHD
		{6090, true},  // XXX/WEB-DL (highest in the table)
		{6999, true},  // upper bound of the half-open range
		{7000, false}, // Books — the family above
		{100006, false},
	}
	for _, tt := range tests {
		if got := IsAdultCategory(tt.id); got != tt.want {
			t.Errorf("IsAdultCategory(%d) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

// TestAdultCategoryIDs proves the hide set covers both halves of the taxonomy:
// the standard XXX ids AND the synthesised custom (1:1) category whose tracker
// category maps into the XXX family (it carries the tracker's own adult label,
// so leaving it advertised would defeat the setting). Non-adult standard and
// custom categories are untouched.
func TestAdultCategoryIDs(t *testing.T) {
	t.Parallel()

	caps := &Capabilities{
		Categories: []Category{
			{ID: 2000, Name: "Movies"},
			{ID: 6000, Name: "XXX"},
			{ID: 6010, Name: "XXX/DVD"},
			{ID: 100001, Name: "Films"},
			{ID: 100006, Name: "Adult Films"},
		},
		CategoryMap: &CategoryMap{},
	}
	caps.CategoryMap.add("1", "Films", 2000)
	caps.CategoryMap.add("1", "Films", 100001)
	caps.CategoryMap.add("6", "Adult Films", 6000)
	caps.CategoryMap.add("6", "Adult Films", 100006)

	got := caps.AdultCategoryIDs()
	want := map[int]struct{}{6000: {}, 6010: {}, 100006: {}}
	if len(got) != len(want) {
		t.Fatalf("AdultCategoryIDs() = %v, want %v", got, want)
	}
	for id := range want {
		if _, ok := got[id]; !ok {
			t.Errorf("AdultCategoryIDs() is missing %d: %v", id, got)
		}
	}
}

// TestAdultCategoryIDsWithoutCategoryMap covers a capabilities value with no
// mapping table (a native driver): the standard XXX ids are still hidden and
// nothing panics.
func TestAdultCategoryIDsWithoutCategoryMap(t *testing.T) {
	t.Parallel()

	caps := &Capabilities{Categories: []Category{{ID: 2000, Name: "Movies"}, {ID: 6000, Name: "XXX"}}}
	got := caps.AdultCategoryIDs()
	if _, ok := got[6000]; !ok || len(got) != 1 {
		t.Errorf("AdultCategoryIDs() = %v, want just 6000", got)
	}
}

// schemaEnumDoc is the minimal shape needed to extract the IndexerCategories
// enum from the JSON-Schema document.
type schemaEnumDoc struct {
	Definitions map[string]struct {
		Title string   `json:"title"`
		Enum  []string `json:"enum"`
	} `json:"definitions"`
}

func readSchemaEnum(t *testing.T) []string {
	t.Helper()
	path := filepath.Join("..", "..", "definitions", "vendor", "schema.json")
	data, err := os.ReadFile(path) //nolint:gosec // fixed in-repo test path.
	if err != nil {
		t.Fatalf("reading schema.json: %v", err)
	}
	var doc schemaEnumDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing schema.json: %v", err)
	}
	def, ok := doc.Definitions["IndexerCategories"]
	if !ok {
		t.Fatal("schema.json has no IndexerCategories definition")
	}
	return def.Enum
}
