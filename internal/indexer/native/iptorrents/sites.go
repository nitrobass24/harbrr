package iptorrents

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds is the between-request pacing applied to IPTorrents. Prowlarr's
// IPTorrents indexer sets no rate-limit override, so its framework default (2.0s,
// HttpIndexerBase) applies; harbrr uses a marginally more conservative 2.1s, riding on
// the definition's RequestDelay so the registry's existing paced client enforces it (no
// special-casing). Pacing does not affect results, so the 0.1s gap is not a parity diff.
const requestDelaySeconds = 2.1

// Families returns IPTorrents as a single native family. It carries a Go-built,
// caps-only definition (id/name/type/links/settings/caps) and the New factory; it is
// registered with the registry, not the Cardigann loader.
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("iptorrents", "IPTorrents", "https://iptorrents.com/", iptCaps()), Factory: New},
	}
}

// siteDef builds the IPTorrents caps-only definition. It is never schema-validated (it
// has no login/search/download block); it exists so mapper.Build, the credential store
// (settingFields/IsSecret), indexerInfo, and the addable-indexer list all work for a
// native family with no special case.
func siteDef(id, name, link string, caps loader.Caps) *loader.Definition {
	delay := requestDelaySeconds
	return &loader.Definition{
		ID:           id,
		Name:         name,
		Description:  name + " (native HTML-scrape driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         caps,
	}
}

// credentialSettings are the user-entered fields. cookie is the full pasted browser
// Cookie string — its name contains "cookie", so loader.SettingsField.IsSecret()
// classifies the text field as a secret (encrypted at rest, redacted by the API).
// user_agent is a plain text field (not secret-classified) sent on every request.
// freeleech_only is a toggle.
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "cookie", Label: "Cookie", Type: "text"},
		{Name: "user_agent", Label: "User-Agent", Type: "text"},
		{Name: "freeleech_only", Label: "Only freeleech", Type: "checkbox"},
	}
}

// iptCaps is the full IPTorrents capability document, porting Prowlarr's SetCapabilities
// category map (IPTorrents.cs) entry-for-entry: each tracker category id maps to the
// standard newznab category named here. The search modes mirror Prowlarr: TV advertises
// q/season/ep/imdbid, movie advertises q/imdbid, music and book advertise q.
func iptCaps() loader.Caps {
	allowIMDB := true
	return loader.Caps{
		CategoryMappings: iptCategoryMappings(),
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

// iptCategoryMappings is Prowlarr's AddCategoryMapping list verbatim: the tracker
// category id (the value the site's category-icon href carries) → the standard newznab
// category name. The Desc is Prowlarr's human label, kept for parity in the addable
// list; the Cat name is what mapper.GetByName resolves to a newznab id.
func iptCategoryMappings() []loader.CategoryMapping {
	mappings := make([]loader.CategoryMapping, 0, 64)
	mappings = append(mappings, iptMovieCats()...)
	mappings = append(mappings, iptTVCats()...)
	mappings = append(mappings, iptGameCats()...)
	mappings = append(mappings, iptMusicCats()...)
	mappings = append(mappings, iptOtherCats()...)
	mappings = append(mappings, iptXXXCats()...)
	return mappings
}

func iptMovieCats() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		catDesc("72", "Movies", "Movies"),
		catDesc("87", "Movies/3D", "Movie/3D"),
		catDesc("77", "Movies/SD", "Movie/480p"),
		catDesc("101", "Movies/UHD", "Movie/4K"),
		catDesc("89", "Movies/HD", "Movie/BD-R"),
		catDesc("90", "Movies/SD", "Movie/BD-Rip"),
		catDesc("96", "Movies/SD", "Movie/Cam"),
		catDesc("6", "Movies/DVD", "Movie/DVD-R"),
		catDesc("48", "Movies/BluRay", "Movie/HD/Bluray"),
		catDesc("54", "Movies", "Movie/Kids"),
		catDesc("62", "Movies/SD", "Movie/MP4"),
		catDesc("38", "Movies/Foreign", "Movie/Non-English"),
		catDesc("68", "Movies", "Movie/Packs"),
		catDesc("20", "Movies/WEB-DL", "Movie/Web-DL"),
		catDesc("7", "Movies/SD", "Movie/Xvid"),
		catDesc("100", "Movies", "Movie/x265"),
	}
}

func iptTVCats() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		catDesc("73", "TV", "TV"),
		catDesc("26", "TV/Documentary", "TV/Documentaries"),
		catDesc("55", "TV/Sport", "Sports"),
		catDesc("78", "TV/SD", "TV/480p"),
		catDesc("23", "TV/HD", "TV/BD"),
		catDesc("24", "TV/SD", "TV/DVD-R"),
		catDesc("25", "TV/SD", "TV/DVD-Rip"),
		catDesc("66", "TV/SD", "TV/Mobile"),
		catDesc("82", "TV/Foreign", "TV/Non-English"),
		catDesc("65", "TV", "TV/Packs"),
		catDesc("83", "TV/Foreign", "TV/Packs/Non-English"),
		catDesc("79", "TV/SD", "TV/SD/x264"),
		catDesc("22", "TV/WEB-DL", "TV/Web-DL"),
		catDesc("5", "TV/HD", "TV/x264"),
		catDesc("99", "TV/HD", "TV/x265"),
		catDesc("4", "TV/SD", "TV/Xvid"),
	}
}

func iptGameCats() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		catDesc("74", "Console", "Games"),
		catDesc("2", "Console/Other", "Games/Mixed"),
		catDesc("47", "Console/NDS", "Games/Nintendo DS"),
		catDesc("43", "PC/ISO", "Games/PC-ISO"),
		catDesc("45", "PC/Games", "Games/PC-Rip"),
		catDesc("71", "Console/PS3", "Games/PS3"),
		catDesc("50", "Console/Wii", "Games/Wii"),
		catDesc("44", "Console/XBox 360", "Games/Xbox-360"),
	}
}

func iptMusicCats() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		catDesc("75", "Audio", "Music"),
		catDesc("3", "Audio/MP3", "Music/Audio"),
		catDesc("80", "Audio/Lossless", "Music/Flac"),
		catDesc("93", "Audio", "Music/Packs"),
		catDesc("37", "Audio/Video", "Music/Video"),
		catDesc("21", "Audio/Video", "Podcast"),
	}
}

func iptOtherCats() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		catDesc("76", "Other", "Other/Miscellaneous"),
		catDesc("60", "TV/Anime", "Anime"),
		catDesc("1", "PC/0day", "Appz"),
		catDesc("86", "PC/0day", "Appz/Non-English"),
		catDesc("64", "Audio/Audiobook", "AudioBook"),
		catDesc("35", "Books", "Books"),
		catDesc("102", "Books", "Books/Non-English"),
		catDesc("94", "Books/Comics", "Books/Comics"),
		catDesc("95", "Books/Other", "Books/Educational"),
		catDesc("98", "Other", "Other/Fonts"),
		catDesc("69", "PC/Mac", "Appz/Mac"),
		catDesc("92", "Books/Mags", "Books/Magazines & Newspapers"),
		catDesc("58", "PC/Mobile-Other", "Appz/Mobile"),
		catDesc("36", "Other", "Other/Pics/Wallpapers"),
	}
}

func iptXXXCats() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		catDesc("88", "XXX", "XXX"),
		catDesc("85", "XXX/Other", "XXX/Magazines"),
		catDesc("8", "XXX", "XXX/Movie"),
		catDesc("81", "XXX", "XXX/Movie/0Day"),
		catDesc("91", "XXX/Pack", "XXX/Packs"),
		catDesc("84", "XXX/ImageSet", "XXX/Pics/Wallpapers"),
	}
}

// catDesc builds one mapping: id is the tracker category id, cat is the standard
// newznab category name (resolved to a newznab id by mapper.GetByName), desc is the
// site's human label.
func catDesc(id, cat, desc string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: cat, Desc: desc}
}
