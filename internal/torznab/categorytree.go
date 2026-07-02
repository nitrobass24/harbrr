package torznab

import (
	"sort"
	"strconv"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
)

// capsCategory is one top-level <category> in the caps document, with its
// advertised <subcat> children.
type capsCategory struct {
	ID      int          `xml:"id,attr"`
	Name    string       `xml:"name,attr"`
	Subcats []capsSubcat `xml:"subcat"`
}

// capsSubcat is one <subcat> under a parent <category>.
type capsSubcat struct {
	ID   int    `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

// buildCategoryTree reconstructs Jackett's GetTorznabCategoryTree(sorted=true)
// from the mapper's flat, de-duplicated, ascending-id advertised list. The
// mapper guarantees every advertised standard child also carries its family
// root (advertise() adds the parent), so a top-level parent node exists for
// every subcat. Custom categories (id >= 100000) are top-level nodes with no
// subcats.
//
// Ordering mirrors Jackett: subcats ascending by id; top-level by the key
// id>=100000 ? "zzz"+Name : itoa(id) — standard parents ascending by id, then
// custom cats by name, placed last. Three cosmetic divergences are recorded in
// testdata/README.md, all affecting only the document order of top-level
// <category> nodes (ids/names/membership/subcats are identical, and *arr keys on
// id): duplicate-custom-id entries collapse (the mapper de-dups advertised cats
// by id); same-name customs tie-break by id rather than Jackett's insertion
// order; and the custom "zzz"+Name keys are compared with Go byte-ORDINAL order
// rather than Jackett's C# CurrentCulture (linguistic) OrderBy.
func buildCategoryTree(advertised []mapper.Category) []capsCategory {
	parents := map[string]*capsCategory{}
	var top []*capsCategory

	for _, c := range advertised {
		if c.IsCustom() || c.IsParent() {
			node := &capsCategory{ID: c.ID, Name: c.Name}
			top = append(top, node)
			if !c.IsCustom() {
				parents[c.Name] = node
			}
		}
	}
	for _, c := range advertised {
		if c.IsCustom() || c.IsParent() {
			continue
		}
		if parent, ok := parents[c.Parent()]; ok {
			parent.Subcats = append(parent.Subcats, capsSubcat{ID: c.ID, Name: c.Name})
		}
	}

	sortCategoryTree(top)
	out := make([]capsCategory, len(top))
	for i, n := range top {
		out[i] = *n
	}
	return out
}

// sortCategoryTree applies Jackett's sort: subcats ascending by id, then
// top-level by the standard-id / "zzz"+name key.
func sortCategoryTree(top []*capsCategory) {
	for _, n := range top {
		sort.Slice(n.Subcats, func(i, j int) bool { return n.Subcats[i].ID < n.Subcats[j].ID })
	}
	sort.SliceStable(top, func(i, j int) bool {
		return topLevelSortKey(top[i]) < topLevelSortKey(top[j])
	})
}

// topLevelSortKey reproduces Jackett's OrderBy key
// (TorznabCapabilitiesCategories.GetTorznabCategoryTree): a custom category
// (id >= 100000) sorts by "zzz"+Name (after every standard parent, by name); a
// standard parent sorts by its id rendered as a string. All standard parent ids
// are four digits (1000..8000), so their string order equals their numeric
// order.
func topLevelSortKey(c *capsCategory) string {
	if c.ID >= mapper.CustomCategoryOffset {
		return "zzz" + c.Name
	}
	return strconv.Itoa(c.ID)
}
