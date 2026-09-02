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
//
// The settings: cookie is the full pasted browser Cookie string — its name contains
// "cookie", so loader.SettingsField.IsSecret() classifies the text field as a secret
// (encrypted at rest, redacted by the API). user_agent is a plain text field (not
// secret-classified) sent on every request. freeleech_only is a toggle.
func Families() []native.Family {
	return []native.Family{
		{Definition: native.Site{
			ID: "iptorrents", Name: "IPTorrents", Link: "https://iptorrents.com/",
			Driver: "HTML-scrape", DelaySeconds: requestDelaySeconds,
			Settings: []loader.SettingsField{native.FieldCookie, native.FieldUserAgent, native.FieldFreeleechOnly},
			Caps:     iptCaps(),
		}.Definition(), Factory: New},
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
// list; the Newznab name is what mapper.GetByName resolves to a newznab id.
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
	return native.Cats(
		native.Cat{ID: "72", Newznab: "Movies", Desc: "Movies"},
		native.Cat{ID: "87", Newznab: "Movies/3D", Desc: "Movie/3D"},
		native.Cat{ID: "77", Newznab: "Movies/SD", Desc: "Movie/480p"},
		native.Cat{ID: "101", Newznab: "Movies/UHD", Desc: "Movie/4K"},
		native.Cat{ID: "89", Newznab: "Movies/HD", Desc: "Movie/BD-R"},
		native.Cat{ID: "90", Newznab: "Movies/SD", Desc: "Movie/BD-Rip"},
		native.Cat{ID: "96", Newznab: "Movies/SD", Desc: "Movie/Cam"},
		native.Cat{ID: "6", Newznab: "Movies/DVD", Desc: "Movie/DVD-R"},
		native.Cat{ID: "48", Newznab: "Movies/BluRay", Desc: "Movie/HD/Bluray"},
		native.Cat{ID: "54", Newznab: "Movies", Desc: "Movie/Kids"},
		native.Cat{ID: "62", Newznab: "Movies/SD", Desc: "Movie/MP4"},
		native.Cat{ID: "38", Newznab: "Movies/Foreign", Desc: "Movie/Non-English"},
		native.Cat{ID: "68", Newznab: "Movies", Desc: "Movie/Packs"},
		native.Cat{ID: "20", Newznab: "Movies/WEB-DL", Desc: "Movie/Web-DL"},
		native.Cat{ID: "7", Newznab: "Movies/SD", Desc: "Movie/Xvid"},
		native.Cat{ID: "100", Newznab: "Movies", Desc: "Movie/x265"},
	)
}

func iptTVCats() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "73", Newznab: "TV", Desc: "TV"},
		native.Cat{ID: "26", Newznab: "TV/Documentary", Desc: "TV/Documentaries"},
		native.Cat{ID: "55", Newznab: "TV/Sport", Desc: "Sports"},
		native.Cat{ID: "78", Newznab: "TV/SD", Desc: "TV/480p"},
		native.Cat{ID: "23", Newznab: "TV/HD", Desc: "TV/BD"},
		native.Cat{ID: "24", Newznab: "TV/SD", Desc: "TV/DVD-R"},
		native.Cat{ID: "25", Newznab: "TV/SD", Desc: "TV/DVD-Rip"},
		native.Cat{ID: "66", Newznab: "TV/SD", Desc: "TV/Mobile"},
		native.Cat{ID: "82", Newznab: "TV/Foreign", Desc: "TV/Non-English"},
		native.Cat{ID: "65", Newznab: "TV", Desc: "TV/Packs"},
		native.Cat{ID: "83", Newznab: "TV/Foreign", Desc: "TV/Packs/Non-English"},
		native.Cat{ID: "79", Newznab: "TV/SD", Desc: "TV/SD/x264"},
		native.Cat{ID: "22", Newznab: "TV/WEB-DL", Desc: "TV/Web-DL"},
		native.Cat{ID: "5", Newznab: "TV/HD", Desc: "TV/x264"},
		native.Cat{ID: "99", Newznab: "TV/HD", Desc: "TV/x265"},
		native.Cat{ID: "4", Newznab: "TV/SD", Desc: "TV/Xvid"},
	)
}

func iptGameCats() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "74", Newznab: "Console", Desc: "Games"},
		native.Cat{ID: "2", Newznab: "Console/Other", Desc: "Games/Mixed"},
		native.Cat{ID: "47", Newznab: "Console/NDS", Desc: "Games/Nintendo DS"},
		native.Cat{ID: "43", Newznab: "PC/ISO", Desc: "Games/PC-ISO"},
		native.Cat{ID: "45", Newznab: "PC/Games", Desc: "Games/PC-Rip"},
		native.Cat{ID: "71", Newznab: "Console/PS3", Desc: "Games/PS3"},
		native.Cat{ID: "50", Newznab: "Console/Wii", Desc: "Games/Wii"},
		native.Cat{ID: "44", Newznab: "Console/XBox 360", Desc: "Games/Xbox-360"},
	)
}

func iptMusicCats() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "75", Newznab: "Audio", Desc: "Music"},
		native.Cat{ID: "3", Newznab: "Audio/MP3", Desc: "Music/Audio"},
		native.Cat{ID: "80", Newznab: "Audio/Lossless", Desc: "Music/Flac"},
		native.Cat{ID: "93", Newznab: "Audio", Desc: "Music/Packs"},
		native.Cat{ID: "37", Newznab: "Audio/Video", Desc: "Music/Video"},
		native.Cat{ID: "21", Newznab: "Audio/Video", Desc: "Podcast"},
	)
}

func iptOtherCats() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "76", Newznab: "Other", Desc: "Other/Miscellaneous"},
		native.Cat{ID: "60", Newznab: "TV/Anime", Desc: "Anime"},
		native.Cat{ID: "1", Newznab: "PC/0day", Desc: "Appz"},
		native.Cat{ID: "86", Newznab: "PC/0day", Desc: "Appz/Non-English"},
		native.Cat{ID: "64", Newznab: "Audio/Audiobook", Desc: "AudioBook"},
		native.Cat{ID: "35", Newznab: "Books", Desc: "Books"},
		native.Cat{ID: "102", Newznab: "Books", Desc: "Books/Non-English"},
		native.Cat{ID: "94", Newznab: "Books/Comics", Desc: "Books/Comics"},
		native.Cat{ID: "95", Newznab: "Books/Other", Desc: "Books/Educational"},
		native.Cat{ID: "98", Newznab: "Other", Desc: "Other/Fonts"},
		native.Cat{ID: "69", Newznab: "PC/Mac", Desc: "Appz/Mac"},
		native.Cat{ID: "92", Newznab: "Books/Mags", Desc: "Books/Magazines & Newspapers"},
		native.Cat{ID: "58", Newznab: "PC/Mobile-Other", Desc: "Appz/Mobile"},
		native.Cat{ID: "36", Newznab: "Other", Desc: "Other/Pics/Wallpapers"},
	)
}

func iptXXXCats() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "88", Newznab: "XXX", Desc: "XXX"},
		native.Cat{ID: "85", Newznab: "XXX/Other", Desc: "XXX/Magazines"},
		native.Cat{ID: "8", Newznab: "XXX", Desc: "XXX/Movie"},
		native.Cat{ID: "81", Newznab: "XXX", Desc: "XXX/Movie/0Day"},
		native.Cat{ID: "91", Newznab: "XXX/Pack", Desc: "XXX/Packs"},
		native.Cat{ID: "84", Newznab: "XXX/ImageSet", Desc: "XXX/Pics/Wallpapers"},
	)
}
