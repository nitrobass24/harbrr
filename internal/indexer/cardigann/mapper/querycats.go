package mapper

// MapTorznabCapsToTrackers resolves the newznab category ids supplied in a
// Torznab `cat` query parameter to the tracker category ids the search request
// drives ({{ .Categories }}), mirroring Jackett's
// TorznabCapabilitiesCategories.MapTorznabCapsToTrackers with the Cardigann
// default mapChildrenCatsToParent=false: each queried id is expanded (a queried
// PARENT family pulls in its advertised children), then every expanded newznab id
// is reverse-mapped to its tracker category ids via the category map. The result
// is the distinct tracker ids in category-map insertion order. An empty or
// all-unmapped input yields nil (the caller decides any default-category
// fallback).
func (c *Capabilities) MapTorznabCapsToTrackers(newznabCats []int) []string {
	if len(newznabCats) == 0 {
		return nil
	}
	return c.CategoryMap.trackersForNewznab(c.expandQueryCategories(newznabCats))
}

// ExpandQueryCategories is the exported form of expandQueryCategories, used by
// the response-side category filter. Jackett's BaseIndexer.FilterResults expands
// the query categories with the same ExpandTorznabQueryCategories logic before
// intersecting them with each release's categories.
func (c *Capabilities) ExpandQueryCategories(newznabCats []int) []int {
	return c.expandQueryCategories(newznabCats)
}

// expandQueryCategories reproduces ExpandTorznabQueryCategories: each queried id
// is kept, and a queried id that is an advertised PARENT family additionally
// pulls in that family's advertised child categories (the same subcats the caps
// tree node carries — NOT the full standard table). Custom ids (>= the offset)
// and child ids are not expanded. mapChildrenCatsToParent is fixed false (the
// Cardigann path), so a queried child never adds its parent. The result is
// de-duplicated, first-seen order preserved.
func (c *Capabilities) expandQueryCategories(newznabCats []int) []int {
	seen := make(map[int]struct{}, len(newznabCats))
	out := make([]int, 0, len(newznabCats))
	add := func(id int) {
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range newznabCats {
		add(id)
		if id >= CustomCategoryOffset {
			continue
		}
		if parent, ok := c.advertisedParent(id); ok {
			for _, child := range c.Categories {
				if !child.IsParent() && !child.IsCustom() && child.Parent() == parent.Name {
					add(child.ID)
				}
			}
		}
	}
	return out
}

// advertisedParent returns the advertised parent-family category with the given
// id, if one is advertised. A queried id matches only when it is both advertised
// and a parent (family root), mirroring Jackett expanding a queried id only when
// it resolves to a parent node in the indexer's category tree.
func (c *Capabilities) advertisedParent(id int) (Category, bool) {
	for _, cat := range c.Categories {
		if cat.ID == id && cat.IsParent() {
			return cat, true
		}
	}
	return Category{}, false
}

// trackersForNewznab returns the distinct tracker category ids whose mapping
// targets any of the given newznab ids, preserving category-map insertion order.
// Mirrors Jackett's _categoryMapping.Where(expanded.Contains(NewzNabCategory)).
// Select(TrackerCategory).Distinct(). Custom mapping entries share their standard
// counterpart's tracker id, so a queried standard OR custom id resolves the same
// tracker category.
func (m *CategoryMap) trackersForNewznab(newznabCats []int) []string {
	if len(newznabCats) == 0 {
		return nil
	}
	want := make(map[int]struct{}, len(newznabCats))
	for _, id := range newznabCats {
		want[id] = struct{}{}
	}
	seen := map[string]struct{}{}
	var out []string
	for _, e := range m.entries {
		if isBlank(e.trackerCategory) { // mirror the blank-id guard in the other lookups
			continue
		}
		if _, ok := want[e.newznabID]; !ok {
			continue
		}
		if _, dup := seen[e.trackerCategory]; dup {
			continue
		}
		seen[e.trackerCategory] = struct{}{}
		out = append(out, e.trackerCategory)
	}
	return out
}
