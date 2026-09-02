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
		{Definition: native.Site{
			ID: "myanonamouse", Name: "MyAnonamouse", Link: "https://www.myanonamouse.net/",
			Driver: "MyAnonamouse", DelaySeconds: requestDelaySeconds,
			Settings: mamSettings,
			Caps:     mamCaps(),
		}.Definition(), Factory: New},
	}
}

// mamSettings are the user-entered fields — none matches a kit Field* spelling, so all
// stay inline. mam_id is the essential session credential: it is password-typed, so the
// secret store encrypts it at rest and the API redacts it. The search-scope toggles are
// checkboxes (non-secret) mirroring Prowlarr's SearchInDescription/Series/Filenames
// options.
var mamSettings = []loader.SettingsField{
	{Name: "mam_id", Label: "Mam ID", Type: "password"},
	{Name: "search_in_description", Label: "Search in description", Type: "checkbox"},
	{Name: "search_in_series", Label: "Search in series", Type: "checkbox"},
	{Name: "search_in_filenames", Label: "Search in filenames", Type: "checkbox"},
}

// mamCaps is the MyAnonamouse capability document: the full Audiobooks/Ebooks/
// Musicology/Radio tracker-category map (Prowlarr's AddCategoryMapping list, in
// order) plus the basic + book search modes (Prowlarr advertises BookSearchParam.Q;
// the always-available basic search carries `q`).
func mamCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: native.Cats(mamCategoryTable...),
		Modes: loader.Modes{
			Search:     []string{"q"},
			BookSearch: []string{"q"},
		},
	}
}

const (
	audiobook = "Audio/Audiobook"
	ebook     = "Books/EBook"
	comics    = "Books/Comics"
	mags      = "Books/Mags"
	technical = "Books/Technical"
)

// mamCategoryTable ports Prowlarr's MyAnonamouse AddCategoryMapping list verbatim, in
// order: each tracker category id maps to a standard newznab category by name, carrying
// the Prowlarr display string as the description.
var mamCategoryTable = []native.Cat{
	{ID: "13", Newznab: audiobook, Desc: "AudioBooks"},
	{ID: "14", Newznab: ebook, Desc: "E-Books"},
	{ID: "15", Newznab: audiobook, Desc: "Musicology"},
	{ID: "16", Newznab: audiobook, Desc: "Radio"},
	{ID: "39", Newznab: audiobook, Desc: "Audiobooks - Action/Adventure"},
	{ID: "49", Newznab: audiobook, Desc: "Audiobooks - Art"},
	{ID: "50", Newznab: audiobook, Desc: "Audiobooks - Biographical"},
	{ID: "83", Newznab: audiobook, Desc: "Audiobooks - Business"},
	{ID: "51", Newznab: audiobook, Desc: "Audiobooks - Computer/Internet"},
	{ID: "97", Newznab: audiobook, Desc: "Audiobooks - Crafts"},
	{ID: "40", Newznab: audiobook, Desc: "Audiobooks - Crime/Thriller"},
	{ID: "41", Newznab: audiobook, Desc: "Audiobooks - Fantasy"},
	{ID: "106", Newznab: audiobook, Desc: "Audiobooks - Food"},
	{ID: "42", Newznab: audiobook, Desc: "Audiobooks - General Fiction"},
	{ID: "52", Newznab: audiobook, Desc: "Audiobooks - General Non-Fic"},
	{ID: "98", Newznab: audiobook, Desc: "Audiobooks - Historical Fiction"},
	{ID: "54", Newznab: audiobook, Desc: "Audiobooks - History"},
	{ID: "55", Newznab: audiobook, Desc: "Audiobooks - Home/Garden"},
	{ID: "43", Newznab: audiobook, Desc: "Audiobooks - Horror"},
	{ID: "99", Newznab: audiobook, Desc: "Audiobooks - Humor"},
	{ID: "84", Newznab: audiobook, Desc: "Audiobooks - Instructional"},
	{ID: "44", Newznab: audiobook, Desc: "Audiobooks - Juvenile"},
	{ID: "56", Newznab: audiobook, Desc: "Audiobooks - Language"},
	{ID: "45", Newznab: audiobook, Desc: "Audiobooks - Literary Classics"},
	{ID: "57", Newznab: audiobook, Desc: "Audiobooks - Math/Science/Tech"},
	{ID: "85", Newznab: audiobook, Desc: "Audiobooks - Medical"},
	{ID: "87", Newznab: audiobook, Desc: "Audiobooks - Mystery"},
	{ID: "119", Newznab: audiobook, Desc: "Audiobooks - Nature"},
	{ID: "88", Newznab: audiobook, Desc: "Audiobooks - Philosophy"},
	{ID: "58", Newznab: audiobook, Desc: "Audiobooks - Pol/Soc/Relig"},
	{ID: "59", Newznab: audiobook, Desc: "Audiobooks - Recreation"},
	{ID: "46", Newznab: audiobook, Desc: "Audiobooks - Romance"},
	{ID: "47", Newznab: audiobook, Desc: "Audiobooks - Science Fiction"},
	{ID: "53", Newznab: audiobook, Desc: "Audiobooks - Self-Help"},
	{ID: "89", Newznab: audiobook, Desc: "Audiobooks - Travel/Adventure"},
	{ID: "100", Newznab: audiobook, Desc: "Audiobooks - True Crime"},
	{ID: "108", Newznab: audiobook, Desc: "Audiobooks - Urban Fantasy"},
	{ID: "48", Newznab: audiobook, Desc: "Audiobooks - Western"},
	{ID: "111", Newznab: audiobook, Desc: "Audiobooks - Young Adult"},
	{ID: "60", Newznab: ebook, Desc: "Ebooks - Action/Adventure"},
	{ID: "71", Newznab: ebook, Desc: "Ebooks - Art"},
	{ID: "72", Newznab: ebook, Desc: "Ebooks - Biographical"},
	{ID: "90", Newznab: ebook, Desc: "Ebooks - Business"},
	{ID: "61", Newznab: comics, Desc: "Ebooks - Comics/Graphic novels"},
	{ID: "73", Newznab: ebook, Desc: "Ebooks - Computer/Internet"},
	{ID: "101", Newznab: ebook, Desc: "Ebooks - Crafts"},
	{ID: "62", Newznab: ebook, Desc: "Ebooks - Crime/Thriller"},
	{ID: "63", Newznab: ebook, Desc: "Ebooks - Fantasy"},
	{ID: "107", Newznab: ebook, Desc: "Ebooks - Food"},
	{ID: "64", Newznab: ebook, Desc: "Ebooks - General Fiction"},
	{ID: "74", Newznab: ebook, Desc: "Ebooks - General Non-Fiction"},
	{ID: "102", Newznab: ebook, Desc: "Ebooks - Historical Fiction"},
	{ID: "76", Newznab: ebook, Desc: "Ebooks - History"},
	{ID: "77", Newznab: ebook, Desc: "Ebooks - Home/Garden"},
	{ID: "65", Newznab: ebook, Desc: "Ebooks - Horror"},
	{ID: "103", Newznab: ebook, Desc: "Ebooks - Humor"},
	{ID: "115", Newznab: ebook, Desc: "Ebooks - Illusion/Magic"},
	{ID: "91", Newznab: ebook, Desc: "Ebooks - Instructional"},
	{ID: "66", Newznab: ebook, Desc: "Ebooks - Juvenile"},
	{ID: "78", Newznab: ebook, Desc: "Ebooks - Language"},
	{ID: "67", Newznab: ebook, Desc: "Ebooks - Literary Classics"},
	{ID: "79", Newznab: mags, Desc: "Ebooks - Magazines/Newspapers"},
	{ID: "80", Newznab: technical, Desc: "Ebooks - Math/Science/Tech"},
	{ID: "92", Newznab: ebook, Desc: "Ebooks - Medical"},
	{ID: "118", Newznab: ebook, Desc: "Ebooks - Mixed Collections"},
	{ID: "94", Newznab: ebook, Desc: "Ebooks - Mystery"},
	{ID: "120", Newznab: ebook, Desc: "Ebooks - Nature"},
	{ID: "95", Newznab: ebook, Desc: "Ebooks - Philosophy"},
	{ID: "81", Newznab: ebook, Desc: "Ebooks - Pol/Soc/Relig"},
	{ID: "82", Newznab: ebook, Desc: "Ebooks - Recreation"},
	{ID: "68", Newznab: ebook, Desc: "Ebooks - Romance"},
	{ID: "69", Newznab: ebook, Desc: "Ebooks - Science Fiction"},
	{ID: "75", Newznab: ebook, Desc: "Ebooks - Self-Help"},
	{ID: "96", Newznab: ebook, Desc: "Ebooks - Travel/Adventure"},
	{ID: "104", Newznab: ebook, Desc: "Ebooks - True Crime"},
	{ID: "109", Newznab: ebook, Desc: "Ebooks - Urban Fantasy"},
	{ID: "70", Newznab: ebook, Desc: "Ebooks - Western"},
	{ID: "112", Newznab: ebook, Desc: "Ebooks - Young Adult"},
	{ID: "19", Newznab: audiobook, Desc: "Guitar/Bass Tabs"},
	{ID: "20", Newznab: audiobook, Desc: "Individual Sheet"},
	{ID: "24", Newznab: audiobook, Desc: "Individual Sheet MP3"},
	{ID: "126", Newznab: audiobook, Desc: "Instructional Book with Video"},
	{ID: "22", Newznab: audiobook, Desc: "Instructional Media - Music"},
	{ID: "113", Newznab: audiobook, Desc: "Lick Library - LTP/Jam With"},
	{ID: "114", Newznab: audiobook, Desc: "Lick Library - Techniques/QL"},
	{ID: "17", Newznab: audiobook, Desc: "Music - Complete Editions"},
	{ID: "26", Newznab: audiobook, Desc: "Music Book"},
	{ID: "27", Newznab: audiobook, Desc: "Music Book MP3"},
	{ID: "30", Newznab: audiobook, Desc: "Sheet Collection"},
	{ID: "31", Newznab: audiobook, Desc: "Sheet Collection MP3"},
	{ID: "127", Newznab: audiobook, Desc: "Radio -  Comedy"},
	{ID: "130", Newznab: audiobook, Desc: "Radio - Drama"},
	{ID: "128", Newznab: audiobook, Desc: "Radio - Factual/Documentary"},
	{ID: "132", Newznab: audiobook, Desc: "Radio - Reading"},
}
