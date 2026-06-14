package mapper

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

func loadFixture(t *testing.T, name string) *loader.Definition {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // fixed test path.
	if err != nil {
		t.Fatalf("reading fixture %q: %v", name, err)
	}
	def, err := loader.Parse(data)
	if err != nil {
		t.Fatalf("parsing fixture %q: %v", name, err)
	}
	return def
}

func TestBuildNumericMappings(t *testing.T) {
	t.Parallel()

	caps, err := Build(loadFixture(t, "numeric_mappings.yml"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if !caps.AllowRawSearch {
		t.Error("AllowRawSearch = false, want true")
	}

	wantModes := map[string][]string{
		ModeSearch:      {"q"},
		ModeTVSearch:    {"q", "season", "ep", "imdbid"},
		ModeMovieSearch: {"q", "imdbid"},
	}
	if !reflect.DeepEqual(caps.Modes, wantModes) {
		t.Errorf("Modes = %v, want %v", caps.Modes, wantModes)
	}

	// id 1 -> Movies/HD (2040) + custom 100001 ("HD Movies").
	if got := caps.CategoryMap.MapTrackerCatToNewznab("1"); !reflect.DeepEqual(got, []int{2040, 100001}) {
		t.Errorf("MapTrackerCatToNewznab(1) = %v, want [2040 100001]", got)
	}
	// id 2 -> TV/Anime (5070) + custom 100002.
	if got := caps.CategoryMap.MapTrackerCatToNewznab("2"); !reflect.DeepEqual(got, []int{5070, 100002}) {
		t.Errorf("MapTrackerCatToNewznab(2) = %v, want [5070 100002]", got)
	}
	// desc lookup hits the custom cat id.
	if got := caps.CategoryMap.MapTrackerCatDescToNewznab("HD Movies"); !reflect.DeepEqual(got, []int{2040, 100001}) {
		t.Errorf("MapTrackerCatDescToNewznab(HD Movies) = %v, want [2040 100001]", got)
	}
	// case-insensitive desc match.
	if got := caps.CategoryMap.MapTrackerCatDescToNewznab("anime"); !reflect.DeepEqual(got, []int{5070, 100002}) {
		t.Errorf("MapTrackerCatDescToNewznab(anime) = %v, want [5070 100002]", got)
	}

	wantAdvertised := []int{2000, 2040, 5000, 5070, 100001, 100002, 100003}
	if got := ids(caps.Categories); !reflect.DeepEqual(got, wantAdvertised) {
		t.Errorf("advertised ids = %v, want %v", got, wantAdvertised)
	}
}

func TestBuildStringIDMapping(t *testing.T) {
	t.Parallel()

	caps, err := Build(loadFixture(t, "string_id_mapping.yml"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Non-Latin id "影视" -> Movies (2000) + hashed custom 133612.
	if got := caps.CategoryMap.MapTrackerCatToNewznab("影视"); !reflect.DeepEqual(got, []int{2000, 133612}) {
		t.Errorf("MapTrackerCatToNewznab(影视) = %v, want [2000 133612]", got)
	}
	// "tv2" has no desc, so only the standard cat, no custom.
	if got := caps.CategoryMap.MapTrackerCatToNewznab("tv2"); !reflect.DeepEqual(got, []int{5000}) {
		t.Errorf("MapTrackerCatToNewznab(tv2) = %v, want [5000]", got)
	}
	if got := caps.CategoryMap.MapTrackerCatToNewznab("missing"); got != nil {
		t.Errorf("MapTrackerCatToNewznab(missing) = %v, want nil", got)
	}
	if caps.AllowRawSearch {
		t.Error("AllowRawSearch = true, want false (cap absent)")
	}
}

func TestBuildCategoriesObject(t *testing.T) {
	t.Parallel()

	caps, err := Build(loadFixture(t, "categories_object.yml"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	cases := map[string][]int{
		"10": {2040}, // Movies/HD
		"20": {5040}, // TV/HD
		"30": {3010}, // Audio/MP3
	}
	for id, want := range cases {
		if got := caps.CategoryMap.MapTrackerCatToNewznab(id); !reflect.DeepEqual(got, want) {
			t.Errorf("MapTrackerCatToNewznab(%s) = %v, want %v", id, got, want)
		}
	}
	// categories: object has no descs -> no custom cats advertised.
	for _, c := range caps.Categories {
		if c.ID >= CustomCategoryOffset {
			t.Errorf("categories object should not synthesise custom cat, got %d", c.ID)
		}
	}
	// Desc lookups never match (no descs recorded).
	if got := caps.CategoryMap.MapTrackerCatDescToNewznab("Movies/HD"); got != nil {
		t.Errorf("desc lookup should be empty, got %v", got)
	}

	wantModes := map[string][]string{
		ModeSearch:      {"q"},
		ModeMusicSearch: {"q", "artist", "album"},
	}
	if !reflect.DeepEqual(caps.Modes, wantModes) {
		t.Errorf("Modes = %v, want %v", caps.Modes, wantModes)
	}
}

// TestBuildDefaultCategories verifies that caps.categorymappings entries with
// default:true are collected into DefaultCategories (tracker ids, in mapping
// order, no dedup), mirroring Jackett's `if (Categorymapping.Default)
// DefaultCategories.Add(id)`. Absent and explicit-false flags are excluded.
func TestBuildDefaultCategories(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	caps, err := Build(&loader.Definition{
		ID: "d", Links: []string{"https://d.test/"},
		Caps: loader.Caps{CategoryMappings: []loader.CategoryMapping{
			{ID: loader.Scalar{Value: "1", Set: true}, Cat: "Movies", Default: &yes},
			{ID: loader.Scalar{Value: "2", Set: true}, Cat: "TV"},
			{ID: loader.Scalar{Value: "3", Set: true}, Cat: "Audio", Default: &no},
			{ID: loader.Scalar{Value: "4", Set: true}, Cat: "Books", Default: &yes},
		}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := caps.DefaultCategories; !reflect.DeepEqual(got, []string{"1", "4"}) {
		t.Errorf("DefaultCategories = %v, want [1 4]", got)
	}
}

// TestBuildCategoriesObjectHasNoDefaults confirms the object form (caps.categories)
// carries no default flag, so DefaultCategories is empty there.
func TestBuildCategoriesObjectHasNoDefaults(t *testing.T) {
	t.Parallel()
	caps, err := Build(&loader.Definition{
		ID: "o", Links: []string{"https://o.test/"},
		Caps: loader.Caps{Categories: map[string]string{"1": "Movies"}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(caps.DefaultCategories) != 0 {
		t.Errorf("object-form caps.categories should have no defaults, got %v", caps.DefaultCategories)
	}
}

// TestBuildUnknownCategoryIsLoudError exercises the mapper's own guard. The
// validated loader rejects unknown enum names upstream, but the mapper must
// still fail loudly (drop-ins / future schema drift), so the definition is built
// directly in-memory rather than parsed.
func TestBuildUnknownCategoryIsLoudError(t *testing.T) {
	t.Parallel()

	t.Run("categorymapping cat", func(t *testing.T) {
		t.Parallel()
		def := &loader.Definition{ID: "bogus_def"}
		def.Caps.CategoryMappings = []loader.CategoryMapping{
			{ID: loader.Scalar{Value: "1", Set: true}, Cat: "Movies/Quantum", Desc: "Bogus"},
		}
		_, err := Build(def)
		if err == nil {
			t.Fatal("Build should fail on unknown category name")
		}
		if !contains(err.Error(), "Movies/Quantum") || !contains(err.Error(), "bogus_def") {
			t.Errorf("error should name the offending category and definition, got: %v", err)
		}
	})

	t.Run("categories object name", func(t *testing.T) {
		t.Parallel()
		def := &loader.Definition{ID: "bogus_obj"}
		def.Caps.Categories = map[string]string{"7": "Nope/Nope"}
		_, err := Build(def)
		if err == nil {
			t.Fatal("Build should fail on unknown category name")
		}
		if !contains(err.Error(), "Nope/Nope") || !contains(err.Error(), "bogus_obj") {
			t.Errorf("error should name the offending category and definition, got: %v", err)
		}
	})
}

func TestBuildNilDefinition(t *testing.T) {
	t.Parallel()
	if _, err := Build(nil); err == nil {
		t.Fatal("Build(nil) should error")
	}
}

func TestCustomCategoryID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id   string
		want int
	}{
		{"1", 100001},
		{"75", 100075},
		{"影视", 133612}, // BitConverter.ToUInt16(SHA1("影视"),0) == 33612
		{"abc", 139337},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			t.Parallel()
			if got := customCategoryID(tt.id); got != tt.want {
				t.Errorf("customCategoryID(%q) = %d, want %d", tt.id, got, tt.want)
			}
		})
	}
}

// TestCorpusSmoke is the headline parity check: Build every vendored definition
// and assert every category name resolves to a known standard category.
func TestCorpusSmoke(t *testing.T) {
	t.Parallel()

	defs, skipped, err := loader.New("").LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("LoadAll returned no definitions")
	}

	unknown := map[string]int{}
	built := 0
	for _, def := range defs {
		caps, buildErr := Build(def)
		if buildErr != nil {
			t.Errorf("Build(%q) failed: %v", def.ID, buildErr)
			collectUnknownNames(def, unknown)
			continue
		}
		if caps.CategoryMap == nil {
			t.Errorf("Build(%q) returned nil CategoryMap", def.ID)
		}
		built++
	}

	if len(unknown) > 0 {
		names := sortedKeys(unknown)
		for _, n := range names {
			t.Errorf("unknown category name %q referenced by %d definition mapping(s)", n, unknown[n])
		}
	}
	t.Logf("corpus: built %d/%d definitions (skipped at load: %d), unknown category names: %d",
		built, len(defs), len(skipped), len(unknown))
}

func collectUnknownNames(def *loader.Definition, unknown map[string]int) {
	for _, name := range def.Caps.Categories {
		if _, ok := GetByName(name); !ok {
			unknown[name]++
		}
	}
	for _, cm := range def.Caps.CategoryMappings {
		if _, ok := GetByName(cm.Cat); !ok {
			unknown[cm.Cat]++
		}
	}
}

func ids(cats []Category) []int {
	out := make([]int, len(cats))
	for i, c := range cats {
		out[i] = c.ID
	}
	return out
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
