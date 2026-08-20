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
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
			{Name: "freeleech_only", Label: "Only freeleech", Type: "checkbox"},
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
	return []loader.CategoryMapping{
		category("70", "TV/Anime", "Anime"),
		category("113", "TV/Anime", "Anime Boxsets"),
		category("112", "Movies/Other", "Anime Movies"),
		category("111", "Movies/Other", "Anime TV"),
		category("150", "PC", "Apps"),
		category("153", "Books", "Books"),
		category("154", "Audio/Audiobook", "Books Audiobooks"),
		category("155", "Books", "Books eBooks & Magazines"),
		category("68", "Movies/Other", "Cams/TS"),
		category("140", "TV/Documentary", "Documentary"),
		category("10", "Movies/DVD", "DVDR"),
		category("109", "Movies/BluRay", "DVDR Bluray Disc"),
		category("131", "TV/Sport", "Fighting"),
		category("134", "TV/Sport", "Fighting Boxing"),
		category("133", "TV/Sport", "Fighting MMA"),
		category("132", "TV/Sport", "Fighting Wrestling"),
		category("72", "Movies/Foreign", "Foreign"),
		category("116", "TV/Foreign", "Foreign Boxsets"),
		category("114", "Movies/Foreign", "Foreign Movies"),
		category("115", "TV/Foreign", "Foreign TV"),
		category("103", "Console/Other", "Games Console"),
		category("105", "Console/Other", "Games Console Nintendo"),
		category("104", "Console/PS4", "Games Console Playstation"),
		category("106", "Console/XBox", "Games Console XBOX"),
		category("6", "PC/Games", "Games PC"),
		category("108", "PC", "Games PC Linux"),
		category("107", "PC/Mac", "Games PC Mac"),
	}
}

func movieCategoryMappings() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		category("11", "Movies", "Movie Boxsets"),
		category("118", "Movies/UHD", "Movie Boxsets 4K"),
		category("162", "Movies/HD", "Movie Boxsets AV1"),
		category("143", "Movies/HD", "Movie Boxsets HD"),
		category("119", "Movies/HD", "Movie Boxsets HEVC"),
		category("144", "Movies/SD", "Movie Boxsets SD"),
		category("12", "Movies", "Movies"),
		category("117", "Movies/UHD", "Movies 4K"),
		category("163", "Movies/HD", "Movies AV1"),
		category("145", "Movies/HD", "Movies HD"),
		category("100", "Movies/HD", "Movies HEVC"),
		category("146", "Movies/SD", "Movies SD"),
	}
}

func musicOtherCategoryMappings() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		category("13", "Audio", "Music"),
		category("135", "Audio/Lossless", "Music FLAC"),
		category("151", "Audio", "Music Karaoke"),
		category("136", "Audio", "Music Boxset"),
		category("148", "Audio/Video", "Music Videos"),
		category("9", "Other", "Other"),
		category("125", "Other", "Other Pictures"),
		category("54", "TV/Other", "Other Soaps"),
		category("83", "TV/Other", "Other Specials"),
		category("139", "TV", "TOTM (Freeleech)"),
		category("138", "TV", "TOTW (x2 upload)"),
		category("139", "Movies", "TOTM (Freeleech)"),
		category("138", "Movies", "TOTW (x2 upload)"),
	}
}

func tvCategoryMappings() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		category("20", "TV/Sport", "Sports"),
		category("88", "TV/Sport", "Sports/Football"),
		category("86", "TV/Sport", "Sports/MotorSports"),
		category("89", "TV/Sport", "Sports/Olympics"),
		category("126", "TV", "TV"),
		category("127", "TV/UHD", "TV 4K"),
		category("164", "TV/HD", "TV AV1"),
		category("129", "TV/HD", "TV HD"),
		category("130", "TV/HD", "TV HEVC"),
		category("128", "TV/SD", "TV SD"),
		category("149", "TV", "TV Specials"),
		category("21", "TV/SD", "TV Boxsets"),
		category("120", "TV/UHD", "TV Boxset 4K"),
		category("165", "TV/UHD", "TV Boxset AV1"),
		category("76", "TV/HD", "TV Boxset HD"),
		category("97", "TV/HD", "TV Boxset HEVC"),
		category("147", "TV/SD", "TV Boxset SD"),
	}
}

func category(id, cat, desc string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: cat, Desc: desc}
}
