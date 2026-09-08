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
			CategoryMappings: native.Cats(xsCategoryTable...),
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

// xsCategoryTable is Prowlarr's AddCategoryMapping list verbatim, in order: the tracker
// category id → the standard newznab category name. The Desc is Prowlarr's human label,
// kept for parity in the addable list; the Newznab name is what mapper.GetByName
// resolves to a newznab id.
var xsCategoryTable = []native.Cat{
	{ID: "70", Newznab: "TV/Anime", Desc: "Anime"},
	{ID: "113", Newznab: "TV/Anime", Desc: "Anime Boxsets"},
	{ID: "112", Newznab: "Movies/Other", Desc: "Anime Movies"},
	{ID: "111", Newznab: "Movies/Other", Desc: "Anime TV"},
	{ID: "150", Newznab: "PC", Desc: "Apps"},
	{ID: "153", Newznab: "Books", Desc: "Books"},
	{ID: "154", Newznab: "Audio/Audiobook", Desc: "Books Audiobooks"},
	{ID: "155", Newznab: "Books", Desc: "Books eBooks & Magazines"},
	{ID: "68", Newznab: "Movies/Other", Desc: "Cams/TS"},
	{ID: "140", Newznab: "TV/Documentary", Desc: "Documentary"},
	{ID: "10", Newznab: "Movies/DVD", Desc: "DVDR"},
	{ID: "109", Newznab: "Movies/BluRay", Desc: "DVDR Bluray Disc"},
	{ID: "131", Newznab: "TV/Sport", Desc: "Fighting"},
	{ID: "134", Newznab: "TV/Sport", Desc: "Fighting Boxing"},
	{ID: "133", Newznab: "TV/Sport", Desc: "Fighting MMA"},
	{ID: "132", Newznab: "TV/Sport", Desc: "Fighting Wrestling"},
	{ID: "72", Newznab: "Movies/Foreign", Desc: "Foreign"},
	{ID: "116", Newznab: "TV/Foreign", Desc: "Foreign Boxsets"},
	{ID: "114", Newznab: "Movies/Foreign", Desc: "Foreign Movies"},
	{ID: "115", Newznab: "TV/Foreign", Desc: "Foreign TV"},
	{ID: "103", Newznab: "Console/Other", Desc: "Games Console"},
	{ID: "105", Newznab: "Console/Other", Desc: "Games Console Nintendo"},
	{ID: "104", Newznab: "Console/PS4", Desc: "Games Console Playstation"},
	{ID: "106", Newznab: "Console/XBox", Desc: "Games Console XBOX"},
	{ID: "6", Newznab: "PC/Games", Desc: "Games PC"},
	{ID: "108", Newznab: "PC", Desc: "Games PC Linux"},
	{ID: "107", Newznab: "PC/Mac", Desc: "Games PC Mac"},
	{ID: "11", Newznab: "Movies", Desc: "Movie Boxsets"},
	{ID: "118", Newznab: "Movies/UHD", Desc: "Movie Boxsets 4K"},
	{ID: "162", Newznab: "Movies/HD", Desc: "Movie Boxsets AV1"},
	{ID: "143", Newznab: "Movies/HD", Desc: "Movie Boxsets HD"},
	{ID: "119", Newznab: "Movies/HD", Desc: "Movie Boxsets HEVC"},
	{ID: "144", Newznab: "Movies/SD", Desc: "Movie Boxsets SD"},
	{ID: "12", Newznab: "Movies", Desc: "Movies"},
	{ID: "117", Newznab: "Movies/UHD", Desc: "Movies 4K"},
	{ID: "163", Newznab: "Movies/HD", Desc: "Movies AV1"},
	{ID: "145", Newznab: "Movies/HD", Desc: "Movies HD"},
	{ID: "100", Newznab: "Movies/HD", Desc: "Movies HEVC"},
	{ID: "146", Newznab: "Movies/SD", Desc: "Movies SD"},
	{ID: "13", Newznab: "Audio", Desc: "Music"},
	{ID: "135", Newznab: "Audio/Lossless", Desc: "Music FLAC"},
	{ID: "151", Newznab: "Audio", Desc: "Music Karaoke"},
	{ID: "136", Newznab: "Audio", Desc: "Music Boxset"},
	{ID: "148", Newznab: "Audio/Video", Desc: "Music Videos"},
	{ID: "9", Newznab: "Other", Desc: "Other"},
	{ID: "125", Newznab: "Other", Desc: "Other Pictures"},
	{ID: "54", Newznab: "TV/Other", Desc: "Other Soaps"},
	{ID: "83", Newznab: "TV/Other", Desc: "Other Specials"},
	{ID: "139", Newznab: "TV", Desc: "TOTM (Freeleech)"},
	{ID: "138", Newznab: "TV", Desc: "TOTW (x2 upload)"},
	{ID: "139", Newznab: "Movies", Desc: "TOTM (Freeleech)"},
	{ID: "138", Newznab: "Movies", Desc: "TOTW (x2 upload)"},
	{ID: "20", Newznab: "TV/Sport", Desc: "Sports"},
	{ID: "88", Newznab: "TV/Sport", Desc: "Sports/Football"},
	{ID: "86", Newznab: "TV/Sport", Desc: "Sports/MotorSports"},
	{ID: "89", Newznab: "TV/Sport", Desc: "Sports/Olympics"},
	{ID: "126", Newznab: "TV", Desc: "TV"},
	{ID: "127", Newznab: "TV/UHD", Desc: "TV 4K"},
	{ID: "164", Newznab: "TV/HD", Desc: "TV AV1"},
	{ID: "129", Newznab: "TV/HD", Desc: "TV HD"},
	{ID: "130", Newznab: "TV/HD", Desc: "TV HEVC"},
	{ID: "128", Newznab: "TV/SD", Desc: "TV SD"},
	{ID: "149", Newznab: "TV", Desc: "TV Specials"},
	{ID: "21", Newznab: "TV/SD", Desc: "TV Boxsets"},
	{ID: "120", Newznab: "TV/UHD", Desc: "TV Boxset 4K"},
	{ID: "165", Newznab: "TV/UHD", Desc: "TV Boxset AV1"},
	{ID: "76", Newznab: "TV/HD", Desc: "TV Boxset HD"},
	{ID: "97", Newznab: "TV/HD", Desc: "TV Boxset HEVC"},
	{ID: "147", Newznab: "TV/SD", Desc: "TV Boxset SD"},
}
