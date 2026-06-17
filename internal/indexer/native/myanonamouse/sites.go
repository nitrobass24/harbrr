package myanonamouse

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds paces MAM requests. Prowlarr declares no explicit rate limit
// for MyAnonamouse, so harbrr applies a conservative 2.1s between requests (riding on
// the definition's RequestDelay so the registry's existing paced client enforces it).
// See the testdata README divergence note.
const requestDelaySeconds = 2.1

// Families returns MyAnonamouse as a single native family. It carries a Go-built,
// caps-only definition (id/name/type/links/settings/caps) and the New factory; it is
// registered with the registry, not the Cardigann loader.
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("myanonamouse", "MyAnonamouse", "https://www.myanonamouse.net/", mamCaps()), Factory: New},
	}
}

// siteDef builds the family's caps-only definition. It is never schema-validated (it
// has no login/search/download block); it exists so mapper.Build, the credential
// store (settingFields/IsSecret), indexerInfo, and the addable-indexer list all work
// for a native family with no special case.
func siteDef(id, name, link string, caps loader.Caps) *loader.Definition {
	delay := requestDelaySeconds
	return &loader.Definition{
		ID:           id,
		Name:         name,
		Description:  name + " (native MyAnonamouse driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         caps,
	}
}

// credentialSettings are the user-entered fields. mam_id is the essential session
// credential: it is password-typed, so the secret store encrypts it at rest and the
// API redacts it. The search-scope toggles are checkboxes (non-secret) mirroring
// Prowlarr's SearchInDescription/Series/Filenames options.
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "mam_id", Label: "Mam ID", Type: "password"},
		{Name: "search_in_description", Label: "Search in description", Type: "checkbox"},
		{Name: "search_in_series", Label: "Search in series", Type: "checkbox"},
		{Name: "search_in_filenames", Label: "Search in filenames", Type: "checkbox"},
	}
}

// mamCaps is the MyAnonamouse capability document: the full Audiobooks/Ebooks/
// Musicology/Radio tracker-category map (Prowlarr's AddCategoryMapping list, in
// order) plus the basic + book search modes (Prowlarr advertises BookSearchParam.Q;
// the always-available basic search carries `q`).
func mamCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: categoryMappings(),
		Modes: loader.Modes{
			Search:     []string{"q"},
			BookSearch: []string{"q"},
		},
	}
}

// mamCat is one row of the ported category table: a tracker category id, the standard
// newznab category name, and the Prowlarr display string used as the description.
type mamCat struct {
	id, name, desc string
}

const (
	audiobook = "Audio/Audiobook"
	ebook     = "Books/EBook"
	comics    = "Books/Comics"
	mags      = "Books/Mags"
	technical = "Books/Technical"
)

// categoryMappings turns the ported MyAnonamouse category table into loader mappings.
func categoryMappings() []loader.CategoryMapping {
	out := make([]loader.CategoryMapping, 0, len(mamCategoryTable))
	for _, c := range mamCategoryTable {
		out = append(out, cat(c.id, c.name, c.desc))
	}
	return out
}

// mamCategoryTable ports Prowlarr's MyAnonamouse AddCategoryMapping list verbatim, in
// order: each tracker category id maps to a standard newznab category by name, carrying
// the Prowlarr display string as the description.
var mamCategoryTable = []mamCat{
	{"13", audiobook, "AudioBooks"},
	{"14", ebook, "E-Books"},
	{"15", audiobook, "Musicology"},
	{"16", audiobook, "Radio"},
	{"39", audiobook, "Audiobooks - Action/Adventure"},
	{"49", audiobook, "Audiobooks - Art"},
	{"50", audiobook, "Audiobooks - Biographical"},
	{"83", audiobook, "Audiobooks - Business"},
	{"51", audiobook, "Audiobooks - Computer/Internet"},
	{"97", audiobook, "Audiobooks - Crafts"},
	{"40", audiobook, "Audiobooks - Crime/Thriller"},
	{"41", audiobook, "Audiobooks - Fantasy"},
	{"106", audiobook, "Audiobooks - Food"},
	{"42", audiobook, "Audiobooks - General Fiction"},
	{"52", audiobook, "Audiobooks - General Non-Fic"},
	{"98", audiobook, "Audiobooks - Historical Fiction"},
	{"54", audiobook, "Audiobooks - History"},
	{"55", audiobook, "Audiobooks - Home/Garden"},
	{"43", audiobook, "Audiobooks - Horror"},
	{"99", audiobook, "Audiobooks - Humor"},
	{"84", audiobook, "Audiobooks - Instructional"},
	{"44", audiobook, "Audiobooks - Juvenile"},
	{"56", audiobook, "Audiobooks - Language"},
	{"45", audiobook, "Audiobooks - Literary Classics"},
	{"57", audiobook, "Audiobooks - Math/Science/Tech"},
	{"85", audiobook, "Audiobooks - Medical"},
	{"87", audiobook, "Audiobooks - Mystery"},
	{"119", audiobook, "Audiobooks - Nature"},
	{"88", audiobook, "Audiobooks - Philosophy"},
	{"58", audiobook, "Audiobooks - Pol/Soc/Relig"},
	{"59", audiobook, "Audiobooks - Recreation"},
	{"46", audiobook, "Audiobooks - Romance"},
	{"47", audiobook, "Audiobooks - Science Fiction"},
	{"53", audiobook, "Audiobooks - Self-Help"},
	{"89", audiobook, "Audiobooks - Travel/Adventure"},
	{"100", audiobook, "Audiobooks - True Crime"},
	{"108", audiobook, "Audiobooks - Urban Fantasy"},
	{"48", audiobook, "Audiobooks - Western"},
	{"111", audiobook, "Audiobooks - Young Adult"},
	{"60", ebook, "Ebooks - Action/Adventure"},
	{"71", ebook, "Ebooks - Art"},
	{"72", ebook, "Ebooks - Biographical"},
	{"90", ebook, "Ebooks - Business"},
	{"61", comics, "Ebooks - Comics/Graphic novels"},
	{"73", ebook, "Ebooks - Computer/Internet"},
	{"101", ebook, "Ebooks - Crafts"},
	{"62", ebook, "Ebooks - Crime/Thriller"},
	{"63", ebook, "Ebooks - Fantasy"},
	{"107", ebook, "Ebooks - Food"},
	{"64", ebook, "Ebooks - General Fiction"},
	{"74", ebook, "Ebooks - General Non-Fiction"},
	{"102", ebook, "Ebooks - Historical Fiction"},
	{"76", ebook, "Ebooks - History"},
	{"77", ebook, "Ebooks - Home/Garden"},
	{"65", ebook, "Ebooks - Horror"},
	{"103", ebook, "Ebooks - Humor"},
	{"115", ebook, "Ebooks - Illusion/Magic"},
	{"91", ebook, "Ebooks - Instructional"},
	{"66", ebook, "Ebooks - Juvenile"},
	{"78", ebook, "Ebooks - Language"},
	{"67", ebook, "Ebooks - Literary Classics"},
	{"79", mags, "Ebooks - Magazines/Newspapers"},
	{"80", technical, "Ebooks - Math/Science/Tech"},
	{"92", ebook, "Ebooks - Medical"},
	{"118", ebook, "Ebooks - Mixed Collections"},
	{"94", ebook, "Ebooks - Mystery"},
	{"120", ebook, "Ebooks - Nature"},
	{"95", ebook, "Ebooks - Philosophy"},
	{"81", ebook, "Ebooks - Pol/Soc/Relig"},
	{"82", ebook, "Ebooks - Recreation"},
	{"68", ebook, "Ebooks - Romance"},
	{"69", ebook, "Ebooks - Science Fiction"},
	{"75", ebook, "Ebooks - Self-Help"},
	{"96", ebook, "Ebooks - Travel/Adventure"},
	{"104", ebook, "Ebooks - True Crime"},
	{"109", ebook, "Ebooks - Urban Fantasy"},
	{"70", ebook, "Ebooks - Western"},
	{"112", ebook, "Ebooks - Young Adult"},
	{"19", audiobook, "Guitar/Bass Tabs"},
	{"20", audiobook, "Individual Sheet"},
	{"24", audiobook, "Individual Sheet MP3"},
	{"126", audiobook, "Instructional Book with Video"},
	{"22", audiobook, "Instructional Media - Music"},
	{"113", audiobook, "Lick Library - LTP/Jam With"},
	{"114", audiobook, "Lick Library - Techniques/QL"},
	{"17", audiobook, "Music - Complete Editions"},
	{"26", audiobook, "Music Book"},
	{"27", audiobook, "Music Book MP3"},
	{"30", audiobook, "Sheet Collection"},
	{"31", audiobook, "Sheet Collection MP3"},
	{"127", audiobook, "Radio -  Comedy"},
	{"130", audiobook, "Radio - Drama"},
	{"128", audiobook, "Radio - Factual/Documentary"},
	{"132", audiobook, "Radio - Reading"},
}

func cat(id, name, desc string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name, Desc: desc}
}
