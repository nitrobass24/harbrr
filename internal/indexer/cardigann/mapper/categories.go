package mapper

import "strings"

// Category is a Newznab/Torznab category: a numeric id and its name. It carries
// two kinds of value. A standard category's id/name come byte-for-byte from
// Jackett's TorznabCatType (GPL-2.0) numeric fact table — the parity anchor for
// Sonarr/Radarr; that table is cross-verified against Jackett's TorznabCatType.cs
// and must not drift from the IndexerCategories enum in vendor/schema.json
// (machine-checked in tests). A synthesised custom ("1:1") category (id >=
// CustomCategoryOffset, name taken from the definition's mapping desc) is not in
// the standard table; use IsCustom to distinguish the two without consulting
// GetByID. A wrong standard id silently breaks downstream consumers.
type Category struct {
	ID   int
	Name string
}

// IsCustom reports whether the category is a synthesised custom ("1:1") category
// rather than a standard table category. Custom ids are offset by
// CustomCategoryOffset, so id >= CustomCategoryOffset identifies them.
func (c Category) IsCustom() bool {
	return c.ID >= CustomCategoryOffset
}

// standardCategories is the canonical Torznab category table, ordered by id.
// Source: Jackett/Jackett src/Jackett.Common/Models/TorznabCatType.cs.
var standardCategories = []Category{
	{1000, "Console"},
	{1010, "Console/NDS"},
	{1020, "Console/PSP"},
	{1030, "Console/Wii"},
	{1040, "Console/XBox"},
	{1050, "Console/XBox 360"},
	{1060, "Console/Wiiware"},
	{1070, "Console/XBox 360 DLC"},
	{1080, "Console/PS3"},
	{1090, "Console/Other"},
	{1110, "Console/3DS"},
	{1120, "Console/PS Vita"},
	{1130, "Console/WiiU"},
	{1140, "Console/XBox One"},
	{1180, "Console/PS4"},
	{2000, "Movies"},
	{2010, "Movies/Foreign"},
	{2020, "Movies/Other"},
	{2030, "Movies/SD"},
	{2040, "Movies/HD"},
	{2045, "Movies/UHD"},
	{2050, "Movies/BluRay"},
	{2060, "Movies/3D"},
	{2070, "Movies/DVD"},
	{2080, "Movies/WEB-DL"},
	{3000, "Audio"},
	{3010, "Audio/MP3"},
	{3020, "Audio/Video"},
	{3030, "Audio/Audiobook"},
	{3040, "Audio/Lossless"},
	{3050, "Audio/Other"},
	{3060, "Audio/Foreign"},
	{4000, "PC"},
	{4010, "PC/0day"},
	{4020, "PC/ISO"},
	{4030, "PC/Mac"},
	{4040, "PC/Mobile-Other"},
	{4050, "PC/Games"},
	{4060, "PC/Mobile-iOS"},
	{4070, "PC/Mobile-Android"},
	{5000, "TV"},
	{5010, "TV/WEB-DL"},
	{5020, "TV/Foreign"},
	{5030, "TV/SD"},
	{5040, "TV/HD"},
	{5045, "TV/UHD"},
	{5050, "TV/Other"},
	{5060, "TV/Sport"},
	{5070, "TV/Anime"},
	{5080, "TV/Documentary"},
	{6000, "XXX"},
	{6010, "XXX/DVD"},
	{6020, "XXX/WMV"},
	{6030, "XXX/XviD"},
	{6040, "XXX/x264"},
	{6045, "XXX/UHD"},
	{6050, "XXX/Pack"},
	{6060, "XXX/ImageSet"},
	{6070, "XXX/Other"},
	{6080, "XXX/SD"},
	{6090, "XXX/WEB-DL"},
	{7000, "Books"},
	{7010, "Books/Mags"},
	{7020, "Books/EBook"},
	{7030, "Books/Comics"},
	{7040, "Books/Technical"},
	{7050, "Books/Other"},
	{7060, "Books/Foreign"},
	{8000, "Other"},
	{8010, "Other/Misc"},
	{8020, "Other/Hashed"},
}

// CustomCategoryOffset is the fixed offset Jackett applies when synthesising a
// custom ("1:1") category id from a tracker category id. Source:
// TorznabCapabilitiesCategories.AddCategoryMapping — customCat id is
// trackerCategoryInt + 100000.
const CustomCategoryOffset = 100000

var (
	catByName = func() map[string]Category {
		m := make(map[string]Category, len(standardCategories))
		for _, c := range standardCategories {
			m[c.Name] = c
		}
		return m
	}()

	catByID = func() map[int]Category {
		m := make(map[int]Category, len(standardCategories))
		for _, c := range standardCategories {
			m[c.ID] = c
		}
		return m
	}()
)

// GetByName resolves a standard category by its exact canonical name.
func GetByName(name string) (Category, bool) {
	c, ok := catByName[name]
	return c, ok
}

// GetByID resolves a standard category by its numeric id.
func GetByID(id int) (Category, bool) {
	c, ok := catByID[id]
	return c, ok
}

// IsParent reports whether the category is a top-level family (no "/" in the
// name), mirroring Jackett's parent/child tree where the family root carries
// its subcategories.
func (c Category) IsParent() bool {
	return !strings.Contains(c.Name, "/")
}

// Parent returns the family-root name of the category ("Movies/HD" -> "Movies").
// A parent category returns its own name.
func (c Category) Parent() string {
	if i := strings.IndexByte(c.Name, '/'); i >= 0 {
		return c.Name[:i]
	}
	return c.Name
}

// StandardCategories returns a copy of the full canonical table in id order.
func StandardCategories() []Category {
	out := make([]Category, len(standardCategories))
	copy(out, standardCategories)
	return out
}
