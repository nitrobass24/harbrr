package newznab

import (
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
)

// Wire <searching> mode element names. The Newznab/Torznab caps wire uses <audio-search>
// for what harbrr keys internally as music-search (mapper.ModeMusicSearch); a literal
// <music-search> element is also accepted, mapping to the same key.
const (
	wireSearch      = "search"
	wireTVSearch    = "tv-search"
	wireMovieSearch = "movie-search"
	wireAudioSearch = "audio-search"
	wireMusicSearch = "music-search"
	wireBookSearch  = "book-search"
)

// imdbParam is the supportedParams token whose presence in tv-search sets AllowTVSearchIMDB.
const imdbParam = "imdbid"

// otherCat / otherMiscCat are the standard fallback category names used when a remote
// category or subcategory cannot be resolved to a more specific standard name.
const (
	otherCat     = "Other"      // id 8000
	otherMiscCat = "Other/Misc" // id 8010
)

// capsToLoaderCaps translates a parsed <caps> document into a loader.Caps: the available
// search modes (with AllowTVSearchIMDB derived from tv-search supportedParams), and one
// CategoryMapping per <category>/<subcat> resolved to a standard category name. The result
// is fed to mapper.Build, so CategoryMap, custom 1:1 categories, family-root advertising,
// and id sorting all come for free.
func capsToLoaderCaps(root *capsRoot) loader.Caps {
	modes, allowIMDB := capsModes(root.Searching.Modes)
	return loader.Caps{
		Modes:             modes,
		AllowTVSearchIMDB: &allowIMDB,
		CategoryMappings:  capsCategoryMappings(root.Categories.Categories),
	}
}

// capsModes maps the available <searching> children onto loader.Modes and reports whether
// tv-search advertises the imdbid param. Only modes with available="yes" are included; the
// stored value is the parsed supportedParams list.
func capsModes(parsed []capsMode) (loader.Modes, bool) {
	var modes loader.Modes
	allowIMDB := false
	for _, m := range parsed {
		if !m.isAvailable() {
			continue
		}
		params := m.params()
		switch strings.ToLower(strings.TrimSpace(m.XMLName.Local)) {
		case wireSearch:
			modes.Search = params
		case wireTVSearch:
			modes.TVSearch = params
			allowIMDB = slices.Contains(params, imdbParam)
		case wireMovieSearch:
			modes.MovieSearch = params
		case wireAudioSearch, wireMusicSearch:
			modes.MusicSearch = params
		case wireBookSearch:
			modes.BookSearch = params
		}
	}
	return modes, allowIMDB
}

// capsCategoryMappings builds one loader.CategoryMapping per remote <category> and <subcat>,
// keying the remote Newznab id to a resolved standard category name (Cat) and keeping the
// remote name as Desc (so the caps round-trip and a custom 1:1 category is synthesised).
func capsCategoryMappings(parsed []capsCategory) []loader.CategoryMapping {
	mappings := make([]loader.CategoryMapping, 0, len(parsed))
	for _, c := range parsed {
		parentName := resolveParent(c)
		mappings = append(mappings, mapping(c.ID, parentName, c.Name))
		for _, sub := range c.Subcats {
			subName := resolveSubcat(parentName, sub)
			mappings = append(mappings, mapping(sub.ID, subName, sub.Name))
		}
	}
	return mappings
}

// resolveParent resolves a remote <category> to a standard top-level category name, using
// the clean rule (exact name -> exact parent id -> Other). No fuzzy substring heuristics.
func resolveParent(c capsCategory) string {
	if cat, ok := getByNameFold(c.Name); ok && cat.IsParent() {
		return cat.Name
	}
	if cat, ok := getByIDStr(c.ID); ok && cat.IsParent() {
		return cat.Name
	}
	return otherCat
}

// resolveSubcat resolves a remote <subcat> under a resolved parent name, using the clean
// rule (combined "Parent/Sub" name -> exact subcat id -> "Parent/Other" -> Other/Misc).
func resolveSubcat(parentName string, sub capsSubcat) string {
	if cat, ok := getByNameFold(parentName + "/" + sub.Name); ok {
		return cat.Name
	}
	if cat, ok := getByIDStr(sub.ID); ok {
		return cat.Name
	}
	if parentName != otherCat {
		if cat, ok := mapper.GetByName(parentName + "/" + otherCat); ok {
			return cat.Name
		}
	}
	return otherMiscCat
}

// mapping builds a CategoryMapping keying the remote id (string) to the resolved standard
// name, with the remote name as desc.
func mapping(remoteID, stdName, remoteName string) loader.CategoryMapping {
	return loader.CategoryMapping{
		ID:   loader.Scalar{Value: strings.TrimSpace(remoteID), Set: true},
		Cat:  stdName,
		Desc: strings.TrimSpace(remoteName),
	}
}

// getByNameFold resolves a standard category by name case-insensitively. The standard table
// is small, so a linear scan is fine and avoids a second index.
func getByNameFold(name string) (mapper.Category, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return mapper.Category{}, false
	}
	if cat, ok := mapper.GetByName(name); ok {
		return cat, true
	}
	for _, c := range mapper.StandardCategories() {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return mapper.Category{}, false
}

// getByIDStr resolves a standard category by its numeric id given as a string. A non-numeric
// or unknown id yields no match.
func getByIDStr(raw string) (mapper.Category, bool) {
	id := digits(raw)
	if id == "" {
		return mapper.Category{}, false
	}
	return mapper.GetByID(mustAtoiNonneg(id))
}

// mustAtoiNonneg parses a non-empty digit string (already validated by digits) to an
// int, returning 0 on the impossible error/overflow rather than silently wrapping.
func mustAtoiNonneg(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
