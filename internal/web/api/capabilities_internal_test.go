package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
)

// TestToCapabilitiesResponseExcludesCategoryMap proves the internal CategoryMap is
// never serialized, the limits are the advertised 100/100, and the category-tree
// fields (custom/parent) are derived.
func TestToCapabilitiesResponseExcludesCategoryMap(t *testing.T) {
	t.Parallel()
	caps := &mapper.Capabilities{
		Modes:          map[string][]string{"search": {"q"}},
		AllowRawSearch: true,
		Categories: []mapper.Category{
			{ID: 2000, Name: "Movies"},
			{ID: 2040, Name: "Movies/HD"},
			{ID: 100001, Name: "Custom"},
		},
		DefaultCategories: []string{"1"},
		CategoryMap:       &mapper.CategoryMap{}, // present, but must NOT appear in JSON
	}
	resp := toCapabilitiesResponse(caps)
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(strings.ToLower(string(body)), "categorymap") {
		t.Fatalf("CategoryMap leaked into JSON: %s", body)
	}
	if resp.Limits.Max != 100 || resp.Limits.Default != 100 {
		t.Errorf("limits = %+v, want 100/100", resp.Limits)
	}
	byID := map[int]categoryResponse{}
	for _, c := range resp.Categories {
		byID[c.ID] = c
	}
	if !byID[2000].IsParent {
		t.Error("Movies should be a parent")
	}
	if byID[2040].Parent != "Movies" {
		t.Errorf("Movies/HD parent = %q, want Movies", byID[2040].Parent)
	}
	if !byID[100001].IsCustom {
		t.Error("category 100001 should be custom")
	}
}
