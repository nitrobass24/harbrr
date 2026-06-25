package torrentday

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds is the between-request pacing applied to TorrentDay. Prowlarr's
// TorrentDay indexer sets no rate-limit override, so its framework default (2.0s,
// HttpIndexerBase) applies; harbrr uses a marginally more conservative 2.1s, riding on
// the definition's RequestDelay so the registry's existing paced client enforces it (no
// special-casing). Pacing does not affect results, so the 0.1s gap is not a parity diff.
const requestDelaySeconds = 2.1

// Families returns TorrentDay as a single native family. It carries a Go-built,
// caps-only definition (id/name/type/links/settings/caps) and the New factory; it is
// registered with the registry, not the Cardigann loader.
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("torrentday", "TorrentDay", "https://www.torrentday.com/", tdCaps()), Factory: New},
	}
}

// siteDef builds the TorrentDay caps-only definition. It is never schema-validated (it
// has no login/search/download block); it exists so mapper.Build, the credential store
// (settingFields/IsSecret), indexerInfo, and the addable-indexer list all work for a
// native family with no special case.
func siteDef(id, name, link string, caps loader.Caps) *loader.Definition {
	delay := requestDelaySeconds
	return &loader.Definition{
		ID:           id,
		Name:         name,
		Description:  name + " (native JSON-search driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         caps,
	}
}

// credentialSettings are the user-entered fields. cookie is the full pasted session
// Cookie string (e.g. uid=...; pass=...) — its name contains "cookie", so
// loader.SettingsField.IsSecret() classifies the text field as a secret (encrypted at
// rest, redacted by the API). freeleech_only is a toggle that restricts results to
// freeleech torrents (download-multiplier 0).
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "cookie", Label: "Cookie", Type: "text"},
		{Name: "freeleech_only", Label: "Only freeleech", Type: "checkbox"},
	}
}

// tdCaps is the full TorrentDay capability document, porting Prowlarr's SetCapabilities
// category map (TorrentDay.cs) entry-for-entry: each tracker category id maps to the
// standard newznab category named here. The search modes mirror Prowlarr: TV advertises
// q/season/ep/imdbid, movie advertises q/imdbid, music and book advertise q.
func tdCaps() loader.Caps {
	allowIMDB := true
	return loader.Caps{
		CategoryMappings: tdCategoryMappings(),
		Modes: loader.Modes{
			Search:      []string{"q"},
			MovieSearch: []string{"q", "imdbid"},
			TVSearch:    []string{"q", "season", "ep", "imdbid"},
			MusicSearch: []string{"q"},
			BookSearch:  []string{"q"},
		},
		AllowTVSearchIMDB: &allowIMDB,
	}
}

// tdCategoryMappings is Prowlarr's AddCategoryMapping list verbatim: the tracker
// category id (the value the response `c` field carries) → the standard newznab category
// name. The Desc is Prowlarr's human label, kept for parity in the addable list; the Cat
// name is what mapper.GetByName resolves to a newznab id.
//
// Two Prowlarr categories have no harbrr canonical name and are routed to the closest
// match, mirroring the iptorrents driver: TVx265 → TV/HD (5040) and XXX/0Day → XXX
// (6000).
func tdCategoryMappings() []loader.CategoryMapping {
	mappings := make([]loader.CategoryMapping, 0, 47)
	mappings = append(mappings, tdMovieCats()...)
	mappings = append(mappings, tdTVCats()...)
	mappings = append(mappings, tdGameCats()...)
	mappings = append(mappings, tdMusicCats()...)
	mappings = append(mappings, tdOtherCats()...)
	mappings = append(mappings, tdXXXCats()...)
	return mappings
}

func tdMovieCats() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		catDesc("25", "Movies/SD", "Movies/480p"),
		catDesc("96", "Movies/UHD", "Movie/4K"),
		catDesc("11", "Movies/BluRay", "Movies/Bluray"),
		catDesc("5", "Movies/BluRay", "Movies/Bluray-Full"),
		catDesc("103", "Movies/SD", "Movies/Cam"),
		catDesc("3", "Movies/DVD", "Movies/DVD-R"),
		catDesc("21", "Movies/SD", "Movies/MP4"),
		catDesc("22", "Movies/Foreign", "Movies/Non-English"),
		catDesc("13", "Movies", "Movies/Packs"),
		catDesc("44", "Movies/SD", "Movies/SD/x264"),
		catDesc("48", "Movies", "Movies/x265"),
		catDesc("1", "Movies/SD", "Movies/XviD"),
	}
}

func tdTVCats() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		catDesc("24", "TV/SD", "TV/480p"),
		catDesc("104", "TV/UHD", "TV/4K"),
		catDesc("32", "TV/HD", "TV/Bluray"),
		catDesc("31", "TV/SD", "TV/DVD-R"),
		catDesc("33", "TV/SD", "TV/DVD-Rip"),
		catDesc("46", "TV/SD", "TV/Mobile"),
		catDesc("82", "TV/Foreign", "TV/Non-English"),
		catDesc("14", "TV", "TV/Packs"),
		catDesc("26", "TV/SD", "TV/SD/x264"),
		catDesc("7", "TV/HD", "TV/x264"),
		catDesc("34", "TV/HD", "TV/x265"),
		catDesc("2", "TV/SD", "TV/XviD"),
	}
}

func tdGameCats() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		catDesc("10", "Console/NDS", "Nintendo"),
		catDesc("4", "PC/Games", "PC/Games"),
		catDesc("18", "Console/PS3", "PS"),
		catDesc("8", "Console/PSP", "PSP"),
		catDesc("9", "Console/XBox", "Xbox"),
	}
}

func tdMusicCats() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		catDesc("17", "Audio/MP3", "Music/Audio"),
		catDesc("27", "Audio", "Music/Flac"),
		catDesc("23", "Audio/Foreign", "Music/Non-English"),
		catDesc("41", "Audio", "Music/Packs"),
		catDesc("16", "Audio/Video", "Music/Video"),
	}
}

func tdOtherCats() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		catDesc("29", "TV/Anime", "Anime"),
		catDesc("42", "Audio/Audiobook", "Audio Books"),
		catDesc("20", "Books", "Books"),
		catDesc("102", "Books/Foreign", "Books/Non-English"),
		catDesc("30", "TV/Documentary", "Documentary"),
		catDesc("95", "TV/Documentary", "Educational"),
		catDesc("47", "Other", "Fonts"),
		catDesc("43", "PC/Mac", "Mac"),
		catDesc("45", "Audio/Other", "Podcast"),
		catDesc("28", "PC", "Softwa/Packs"),
		catDesc("12", "PC", "Software"),
	}
}

func tdXXXCats() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		catDesc("19", "XXX", "XXX/0Day"),
		catDesc("6", "XXX", "XXX/Movies"),
		catDesc("15", "XXX/Pack", "XXX/Packs"),
	}
}

// catDesc builds one mapping: id is the tracker category id, cat is the standard
// newznab category name (resolved to a newznab id by mapper.GetByName), desc is the
// site's human label.
func catDesc(id, cat, desc string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: cat, Desc: desc}
}
