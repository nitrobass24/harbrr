package mapper

import (
	"reflect"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// queryCatsDef advertises Movies via two children (HD 2040, SD 2030) plus TV
// (5000) directly, exercising parent expansion, child, custom and unmapped
// lookups.
func queryCatsDef() *loader.Definition {
	return &loader.Definition{
		ID:    "querycats",
		Links: []string{"https://example.com"},
		Caps: loader.Caps{
			CategoryMappings: []loader.CategoryMapping{
				{ID: loader.Scalar{Value: "10", Set: true}, Cat: "Movies/HD", Desc: "HD"}, // -> 2040 + custom 100010
				{ID: loader.Scalar{Value: "11", Set: true}, Cat: "Movies/SD", Desc: "SD"}, // -> 2030 + custom 100011
				{ID: loader.Scalar{Value: "20", Set: true}, Cat: "TV"},                    // -> 5000 (parent, no child)
			},
			Modes: loader.Modes{Search: []string{"q"}},
		},
	}
}

func TestMapTorznabCapsToTrackers(t *testing.T) {
	t.Parallel()
	caps, err := Build(queryCatsDef())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	tests := []struct {
		name    string
		newznab []int
		want    []string
	}{
		{"parent expands to advertised children", []int{2000}, []string{"10", "11"}},
		{"child maps directly", []int{2040}, []string{"10"}},
		{"custom maps to its tracker cat", []int{100011}, []string{"11"}},
		{"parent with no advertised child", []int{5000}, []string{"20"}},
		{"unadvertised cat maps to nothing", []int{7000}, nil},
		{"empty input", nil, nil},
		{"multiple, deduped in map order", []int{2040, 100010}, []string{"10"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := caps.MapTorznabCapsToTrackers(tt.newznab)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MapTorznabCapsToTrackers(%v) = %v, want %v", tt.newznab, got, tt.want)
			}
		})
	}
}

// TestExpandQueryCategoriesUsesAdvertisedChildren confirms a queried parent
// expands only to the indexer's ADVERTISED children, not the full standard table
// (so a family the indexer only partially covers does not over-expand).
func TestExpandQueryCategoriesUsesAdvertisedChildren(t *testing.T) {
	t.Parallel()
	caps, err := Build(queryCatsDef())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Movies advertises only HD(2040) + SD(2030); querying the Movies parent must
	// expand to exactly those, never the other standard Movies children.
	got := caps.expandQueryCategories([]int{2000})
	want := []int{2000, 2030, 2040} // advertised children in ascending-id order
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expandQueryCategories([2000]) = %v, want %v (advertised children only)", got, want)
	}
}
