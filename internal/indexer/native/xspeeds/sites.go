package xspeeds

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const requestDelaySeconds = 2.1

// Families returns the Go-built XSpeeds definition and its native driver factory.
func Families() []native.Family {
	return []native.Family{{Definition: definition(), Factory: New}}
}

// definition is hand-built rather than native.Site{}.Definition(): the tracker's
// free-form description does not fit Site's "<Name> (native <Driver> driver)"
// format, and the credential fields carry Required (see Settings below).
func definition() *loader.Definition {
	delay := requestDelaySeconds
	return &loader.Definition{
		ID:           "xspeeds",
		Name:         "XSpeeds",
		Description:  "XSpeeds (XS) is a private torrent tracker for movies, TV, and general releases",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{"https://www.xspeeds.eu/"},
		RequestDelay: &delay,
		Settings: []loader.SettingsField{
			// username/password stay inline: they carry Required, which the kit's
			// Field* constants deliberately don't (the alpharatio precedent).
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
			native.FieldFreeleechOnly,
		},
		Caps: loader.Caps{
			CategoryMappings: categoryMappings(),
			Modes: loader.Modes{
				Search:      []string{"q"},
				TVSearch:    []string{"q", "season", "ep"},
				MovieSearch: []string{"q"},
				MusicSearch: []string{"q"},
				BookSearch:  []string{"q"},
			},
		},
	}
}

func categoryMappings() []loader.CategoryMapping {
	mappings := make([]loader.CategoryMapping, 0, 69)
	mappings = append(mappings, generalCategoryMappings()...)
	mappings = append(mappings, movieCategoryMappings()...)
	mappings = append(mappings, musicOtherCategoryMappings()...)
	mappings = append(mappings, tvCategoryMappings()...)
	return mappings
}

func generalCategoryMappings() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "70", Newznab: "TV/Anime", Desc: "Anime"},
		native.Cat{ID: "113", Newznab: "TV/Anime", Desc: "Anime Boxsets"},
		native.Cat{ID: "112", Newznab: "Movies/Other", Desc: "Anime Movies"},
		native.Cat{ID: "111", Newznab: "Movies/Other", Desc: "Anime TV"},
		native.Cat{ID: "150", Newznab: "PC", Desc: "Apps"},
		native.Cat{ID: "153", Newznab: "Books", Desc: "Books"},
		native.Cat{ID: "154", Newznab: "Audio/Audiobook", Desc: "Books Audiobooks"},
		native.Cat{ID: "155", Newznab: "Books", Desc: "Books eBooks & Magazines"},
		native.Cat{ID: "68", Newznab: "Movies/Other", Desc: "Cams/TS"},
		native.Cat{ID: "140", Newznab: "TV/Documentary", Desc: "Documentary"},
		native.Cat{ID: "10", Newznab: "Movies/DVD", Desc: "DVDR"},
		native.Cat{ID: "109", Newznab: "Movies/BluRay", Desc: "DVDR Bluray Disc"},
		native.Cat{ID: "131", Newznab: "TV/Sport", Desc: "Fighting"},
		native.Cat{ID: "134", Newznab: "TV/Sport", Desc: "Fighting Boxing"},
		native.Cat{ID: "133", Newznab: "TV/Sport", Desc: "Fighting MMA"},
		native.Cat{ID: "132", Newznab: "TV/Sport", Desc: "Fighting Wrestling"},
		native.Cat{ID: "72", Newznab: "Movies/Foreign", Desc: "Foreign"},
		native.Cat{ID: "116", Newznab: "TV/Foreign", Desc: "Foreign Boxsets"},
		native.Cat{ID: "114", Newznab: "Movies/Foreign", Desc: "Foreign Movies"},
		native.Cat{ID: "115", Newznab: "TV/Foreign", Desc: "Foreign TV"},
		native.Cat{ID: "103", Newznab: "Console/Other", Desc: "Games Console"},
		native.Cat{ID: "105", Newznab: "Console/Other", Desc: "Games Console Nintendo"},
		native.Cat{ID: "104", Newznab: "Console/PS4", Desc: "Games Console Playstation"},
		native.Cat{ID: "106", Newznab: "Console/XBox", Desc: "Games Console XBOX"},
		native.Cat{ID: "6", Newznab: "PC/Games", Desc: "Games PC"},
		native.Cat{ID: "108", Newznab: "PC", Desc: "Games PC Linux"},
		native.Cat{ID: "107", Newznab: "PC/Mac", Desc: "Games PC Mac"},
	)
}

func movieCategoryMappings() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "11", Newznab: "Movies", Desc: "Movie Boxsets"},
		native.Cat{ID: "118", Newznab: "Movies/UHD", Desc: "Movie Boxsets 4K"},
		native.Cat{ID: "162", Newznab: "Movies/HD", Desc: "Movie Boxsets AV1"},
		native.Cat{ID: "143", Newznab: "Movies/HD", Desc: "Movie Boxsets HD"},
		native.Cat{ID: "119", Newznab: "Movies/HD", Desc: "Movie Boxsets HEVC"},
		native.Cat{ID: "144", Newznab: "Movies/SD", Desc: "Movie Boxsets SD"},
		native.Cat{ID: "12", Newznab: "Movies", Desc: "Movies"},
		native.Cat{ID: "117", Newznab: "Movies/UHD", Desc: "Movies 4K"},
		native.Cat{ID: "163", Newznab: "Movies/HD", Desc: "Movies AV1"},
		native.Cat{ID: "145", Newznab: "Movies/HD", Desc: "Movies HD"},
		native.Cat{ID: "100", Newznab: "Movies/HD", Desc: "Movies HEVC"},
		native.Cat{ID: "146", Newznab: "Movies/SD", Desc: "Movies SD"},
	)
}

func musicOtherCategoryMappings() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "13", Newznab: "Audio", Desc: "Music"},
		native.Cat{ID: "135", Newznab: "Audio/Lossless", Desc: "Music FLAC"},
		native.Cat{ID: "151", Newznab: "Audio", Desc: "Music Karaoke"},
		native.Cat{ID: "136", Newznab: "Audio", Desc: "Music Boxset"},
		native.Cat{ID: "148", Newznab: "Audio/Video", Desc: "Music Videos"},
		native.Cat{ID: "9", Newznab: "Other", Desc: "Other"},
		native.Cat{ID: "125", Newznab: "Other", Desc: "Other Pictures"},
		native.Cat{ID: "54", Newznab: "TV/Other", Desc: "Other Soaps"},
		native.Cat{ID: "83", Newznab: "TV/Other", Desc: "Other Specials"},
		native.Cat{ID: "139", Newznab: "TV", Desc: "TOTM (Freeleech)"},
		native.Cat{ID: "138", Newznab: "TV", Desc: "TOTW (x2 upload)"},
		native.Cat{ID: "139", Newznab: "Movies", Desc: "TOTM (Freeleech)"},
		native.Cat{ID: "138", Newznab: "Movies", Desc: "TOTW (x2 upload)"},
	)
}

func tvCategoryMappings() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "20", Newznab: "TV/Sport", Desc: "Sports"},
		native.Cat{ID: "88", Newznab: "TV/Sport", Desc: "Sports/Football"},
		native.Cat{ID: "86", Newznab: "TV/Sport", Desc: "Sports/MotorSports"},
		native.Cat{ID: "89", Newznab: "TV/Sport", Desc: "Sports/Olympics"},
		native.Cat{ID: "126", Newznab: "TV", Desc: "TV"},
		native.Cat{ID: "127", Newznab: "TV/UHD", Desc: "TV 4K"},
		native.Cat{ID: "164", Newznab: "TV/HD", Desc: "TV AV1"},
		native.Cat{ID: "129", Newznab: "TV/HD", Desc: "TV HD"},
		native.Cat{ID: "130", Newznab: "TV/HD", Desc: "TV HEVC"},
		native.Cat{ID: "128", Newznab: "TV/SD", Desc: "TV SD"},
		native.Cat{ID: "149", Newznab: "TV", Desc: "TV Specials"},
		native.Cat{ID: "21", Newznab: "TV/SD", Desc: "TV Boxsets"},
		native.Cat{ID: "120", Newznab: "TV/UHD", Desc: "TV Boxset 4K"},
		native.Cat{ID: "165", Newznab: "TV/UHD", Desc: "TV Boxset AV1"},
		native.Cat{ID: "76", Newznab: "TV/HD", Desc: "TV Boxset HD"},
		native.Cat{ID: "97", Newznab: "TV/HD", Desc: "TV Boxset HEVC"},
		native.Cat{ID: "147", Newznab: "TV/SD", Desc: "TV Boxset SD"},
	)
}
