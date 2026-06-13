package mapper

import (
	"crypto/sha1" //nolint:gosec // SHA1 here is a non-cryptographic id hash; it must match Jackett's BitConverter.ToUInt16(SHA1(id)) custom-category formula byte-for-byte.
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// Capabilities is the typed capabilities model produced by the mapper stage.
// It is the engine-internal equivalent of Jackett's TorznabCapabilities: which
// search modes the indexer advertises (with each mode's supported query
// params), whether raw search is allowed, the category map for tracker<->newznab
// resolution, and the de-duplicated list of advertised categories. Serialising
// this to Torznab/Newznab XML is a later (Phase 3) stage's job; this stays a
// pure typed model.
type Capabilities struct {
	// Modes maps a search-mode name ("search", "tv-search", "movie-search",
	// "music-search", "book-search") to its ordered list of supported query
	// params, copied faithfully from the definition's caps.modes. Only modes the
	// definition declares are present.
	Modes map[string][]string

	// AllowRawSearch mirrors caps.allowrawsearch (Jackett SupportsRawSearch).
	AllowRawSearch bool

	// AllowTVSearchIMDB mirrors caps.allowtvsearchimdb. Jackett's
	// TorznabCapabilities.TvSearchImdbAvailable defaults false (disabled for all
	// indexers per #8107) and is overridden ONLY by this per-definition flag
	// (CardigannIndexer sets TorznabCaps.TvSearchImdbAvailable =
	// Definition.Caps.Allowtvsearchimdb). So tv-search advertises the imdbid param
	// iff this flag is set, independent of whether "imdbid" appears in the
	// caps.modes tv-search list. The Phase 3 Torznab caps serializer needs it.
	AllowTVSearchIMDB bool

	// Categories is the de-duplicated, ascending-id-ordered list of categories
	// this indexer advertises: every standard category referenced by the
	// definition (plus each referenced category's family root) and every
	// synthesised custom (1:1) category (Category.IsCustom). The ascending-id sort
	// is an intentional, parity-verified substitute for Jackett's category-tree
	// flatten order; a later (Phase 3) serializer that needs the parent->child
	// nesting re-derives it from these ids (Category.Parent / Children).
	Categories []Category

	// CategoryMap provides Jackett's runtime tracker<->newznab lookups.
	CategoryMap *CategoryMap
}

// Mode name constants mirror the caps.modes keys.
const (
	ModeSearch      = "search"
	ModeTVSearch    = "tv-search"
	ModeMovieSearch = "movie-search"
	ModeMusicSearch = "music-search"
	ModeBookSearch  = "book-search"
)

// categoryMapEntry is one resolved (trackerCategory, desc, newznabID) triple,
// mirroring Jackett's CategoryMapping records inside TorznabCapabilitiesCategories.
// A single definition mapping with a desc produces two entries: one for the
// standard category id and one for the synthesised custom id.
type categoryMapEntry struct {
	trackerCategory string
	desc            string
	newznabID       int
}

// CategoryMap holds the resolved category mapping entries and reproduces
// Jackett's TorznabCapabilitiesCategories runtime lookups.
type CategoryMap struct {
	entries []categoryMapEntry
}

// add appends a (trackerCategory, desc, newznabID) entry, mirroring
// _categoryMapping.Add in Jackett.
func (m *CategoryMap) add(trackerCategory, desc string, newznabID int) {
	m.entries = append(m.entries, categoryMapEntry{
		trackerCategory: trackerCategory,
		desc:            desc,
		newznabID:       newznabID,
	})
}

// MapTrackerCatToNewznab returns the newznab category ids mapped from a tracker
// category id, case-insensitively, preserving insertion order. Mirrors
// TorznabCapabilitiesCategories.MapTrackerCatToNewznab.
func (m *CategoryMap) MapTrackerCatToNewznab(trackerCategory string) []int {
	if isBlank(trackerCategory) {
		return nil
	}
	var out []int
	for _, e := range m.entries {
		if !isBlank(e.trackerCategory) && equalsFold(e.trackerCategory, trackerCategory) {
			out = append(out, e.newznabID)
		}
	}
	return out
}

// MapTrackerCatDescToNewznab returns the newznab category ids mapped from a
// tracker category description, case-insensitively, preserving insertion order.
// Mirrors TorznabCapabilitiesCategories.MapTrackerCatDescToNewznab.
func (m *CategoryMap) MapTrackerCatDescToNewznab(trackerCategoryDesc string) []int {
	if isBlank(trackerCategoryDesc) {
		return nil
	}
	var out []int
	for _, e := range m.entries {
		if !isBlank(e.desc) && equalsFold(e.desc, trackerCategoryDesc) {
			out = append(out, e.newznabID)
		}
	}
	return out
}

// Build is the mapper stage entry point: it turns a loaded definition into the
// typed Capabilities model. Unknown category names produce a loud, descriptive
// error (never silently dropped), referencing the definition id and the
// offending tracker id/name only — never secrets.
func Build(def *loader.Definition) (*Capabilities, error) {
	if def == nil {
		return nil, errors.New("mapper: nil definition")
	}
	b := builder{def: def, catMap: &CategoryMap{}, advertised: map[int]Category{}}
	return b.build()
}

type builder struct {
	def        *loader.Definition
	catMap     *CategoryMap
	advertised map[int]Category
}

func (b builder) build() (*Capabilities, error) {
	if err := b.mapCategories(); err != nil {
		return nil, err
	}
	if err := b.mapCategoryMappings(); err != nil {
		return nil, err
	}
	return &Capabilities{
		Modes:             modesToMap(b.def.Caps.Modes),
		AllowRawSearch:    boolValue(b.def.Caps.AllowRawSearch),
		AllowTVSearchIMDB: boolValue(b.def.Caps.AllowTVSearchIMDB),
		Categories:        b.sortedAdvertised(),
		CategoryMap:       b.catMap,
	}, nil
}

// mapCategories handles the caps.categories object form (tracker id -> category
// name). Per Jackett, these have no desc, so no custom category is synthesised.
func (b builder) mapCategories() error {
	for trackerID, name := range b.def.Caps.Categories {
		cat, ok := GetByName(name)
		if !ok {
			return fmt.Errorf("mapper: definition %q: caps.categories id %q references unknown category name %q", b.def.ID, trackerID, name)
		}
		b.addMapping(trackerID, "", cat)
	}
	return nil
}

// mapCategoryMappings handles the caps.categorymappings list form. When a
// mapping declares a desc, Jackett additionally synthesises a custom (1:1)
// category at the CustomCategoryOffset.
func (b builder) mapCategoryMappings() error {
	for _, cm := range b.def.Caps.CategoryMappings {
		cat, ok := GetByName(cm.Cat)
		if !ok {
			return fmt.Errorf("mapper: definition %q: categorymapping id %q references unknown category name %q", b.def.ID, cm.ID.String(), cm.Cat)
		}
		b.addMapping(cm.ID.String(), cm.Desc, cat)
		if cm.Desc != "" {
			custom := Category{ID: customCategoryID(cm.ID.String()), Name: cm.Desc}
			b.addCustom(cm.ID.String(), cm.Desc, custom)
		}
	}
	return nil
}

// addMapping records the standard-category mapping and advertises the standard
// category (and, in Jackett, its family root via the category tree).
func (b builder) addMapping(trackerID, desc string, cat Category) {
	b.catMap.add(trackerID, desc, cat.ID)
	b.advertise(cat)
}

// addCustom records the synthesised custom-category mapping and advertises it.
func (b builder) addCustom(trackerID, desc string, custom Category) {
	b.catMap.add(trackerID, desc, custom.ID)
	b.advertised[custom.ID] = custom
}

// advertise adds a standard category and its family root to the advertised set,
// mirroring Jackett's AddTorznabCategoryTree which attaches the category under
// its parent family.
func (b builder) advertise(cat Category) {
	b.advertised[cat.ID] = cat
	if !cat.IsParent() {
		if parent, ok := GetByName(cat.Parent()); ok {
			b.advertised[parent.ID] = parent
		}
	}
}

func (b builder) sortedAdvertised() []Category {
	out := make([]Category, 0, len(b.advertised))
	for _, c := range b.advertised {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// customCategoryID reproduces Jackett's custom-category id formula
// (TorznabCapabilitiesCategories.AddCategoryMapping): a numeric tracker id is
// used directly; a non-numeric id is hashed to a uint16 via
// BitConverter.ToUInt16(SHA1(id), 0) (little-endian, first two SHA1 bytes). The
// result is offset by CustomCategoryOffset.
func customCategoryID(trackerCategory string) int {
	n, err := strconv.Atoi(trackerCategory)
	if err != nil {
		sum := sha1.Sum([]byte(trackerCategory)) //nolint:gosec // see import note: must match Jackett's hash.
		n = int(binary.LittleEndian.Uint16(sum[0:2]))
	}
	return n + CustomCategoryOffset
}

func modesToMap(m loader.Modes) map[string][]string {
	out := map[string][]string{}
	addMode(out, ModeSearch, m.Search)
	addMode(out, ModeTVSearch, m.TVSearch)
	addMode(out, ModeMovieSearch, m.MovieSearch)
	addMode(out, ModeMusicSearch, m.MusicSearch)
	addMode(out, ModeBookSearch, m.BookSearch)
	return out
}

func addMode(out map[string][]string, name string, params []string) {
	if params == nil {
		return
	}
	cp := make([]string, len(params))
	copy(cp, params)
	out[name] = cp
}

func boolValue(p *bool) bool {
	return p != nil && *p
}

// isBlank reports whether s is empty or all whitespace, mirroring Jackett's
// string.IsNullOrWhiteSpace guard.
func isBlank(s string) bool {
	return strings.TrimSpace(s) == ""
}

// equalsFold compares case-insensitively, mirroring Jackett's
// StringComparison.InvariantCultureIgnoreCase for the ASCII category ids/descs
// that appear in definitions.
func equalsFold(a, b string) bool {
	return strings.EqualFold(a, b)
}
