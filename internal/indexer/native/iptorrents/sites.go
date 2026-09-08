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
		CategoryMappings: native.Cats(iptCategoryTable...),
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

// iptCategoryTable is Prowlarr's AddCategoryMapping list verbatim, in order: the tracker
// category id (the value the site's category-icon href carries) → the standard newznab
// category name. The Desc is Prowlarr's human label, kept for parity in the addable
// list; the Newznab name is what mapper.GetByName resolves to a newznab id.
var iptCategoryTable = []native.Cat{
	{ID: "72", Newznab: "Movies", Desc: "Movies"},
	{ID: "87", Newznab: "Movies/3D", Desc: "Movie/3D"},
	{ID: "77", Newznab: "Movies/SD", Desc: "Movie/480p"},
	{ID: "101", Newznab: "Movies/UHD", Desc: "Movie/4K"},
	{ID: "89", Newznab: "Movies/HD", Desc: "Movie/BD-R"},
	{ID: "90", Newznab: "Movies/SD", Desc: "Movie/BD-Rip"},
	{ID: "96", Newznab: "Movies/SD", Desc: "Movie/Cam"},
	{ID: "6", Newznab: "Movies/DVD", Desc: "Movie/DVD-R"},
	{ID: "48", Newznab: "Movies/BluRay", Desc: "Movie/HD/Bluray"},
	{ID: "54", Newznab: "Movies", Desc: "Movie/Kids"},
	{ID: "62", Newznab: "Movies/SD", Desc: "Movie/MP4"},
	{ID: "38", Newznab: "Movies/Foreign", Desc: "Movie/Non-English"},
	{ID: "68", Newznab: "Movies", Desc: "Movie/Packs"},
	{ID: "20", Newznab: "Movies/WEB-DL", Desc: "Movie/Web-DL"},
	{ID: "7", Newznab: "Movies/SD", Desc: "Movie/Xvid"},
	{ID: "100", Newznab: "Movies", Desc: "Movie/x265"},
	{ID: "73", Newznab: "TV", Desc: "TV"},
	{ID: "26", Newznab: "TV/Documentary", Desc: "TV/Documentaries"},
	{ID: "55", Newznab: "TV/Sport", Desc: "Sports"},
	{ID: "78", Newznab: "TV/SD", Desc: "TV/480p"},
	{ID: "23", Newznab: "TV/HD", Desc: "TV/BD"},
	{ID: "24", Newznab: "TV/SD", Desc: "TV/DVD-R"},
	{ID: "25", Newznab: "TV/SD", Desc: "TV/DVD-Rip"},
	{ID: "66", Newznab: "TV/SD", Desc: "TV/Mobile"},
	{ID: "82", Newznab: "TV/Foreign", Desc: "TV/Non-English"},
	{ID: "65", Newznab: "TV", Desc: "TV/Packs"},
	{ID: "83", Newznab: "TV/Foreign", Desc: "TV/Packs/Non-English"},
	{ID: "79", Newznab: "TV/SD", Desc: "TV/SD/x264"},
	{ID: "22", Newznab: "TV/WEB-DL", Desc: "TV/Web-DL"},
	{ID: "5", Newznab: "TV/HD", Desc: "TV/x264"},
	{ID: "99", Newznab: "TV/HD", Desc: "TV/x265"},
	{ID: "4", Newznab: "TV/SD", Desc: "TV/Xvid"},
	{ID: "74", Newznab: "Console", Desc: "Games"},
	{ID: "2", Newznab: "Console/Other", Desc: "Games/Mixed"},
	{ID: "47", Newznab: "Console/NDS", Desc: "Games/Nintendo DS"},
	{ID: "43", Newznab: "PC/ISO", Desc: "Games/PC-ISO"},
	{ID: "45", Newznab: "PC/Games", Desc: "Games/PC-Rip"},
	{ID: "71", Newznab: "Console/PS3", Desc: "Games/PS3"},
	{ID: "50", Newznab: "Console/Wii", Desc: "Games/Wii"},
	{ID: "44", Newznab: "Console/XBox 360", Desc: "Games/Xbox-360"},
	{ID: "75", Newznab: "Audio", Desc: "Music"},
	{ID: "3", Newznab: "Audio/MP3", Desc: "Music/Audio"},
	{ID: "80", Newznab: "Audio/Lossless", Desc: "Music/Flac"},
	{ID: "93", Newznab: "Audio", Desc: "Music/Packs"},
	{ID: "37", Newznab: "Audio/Video", Desc: "Music/Video"},
	{ID: "21", Newznab: "Audio/Video", Desc: "Podcast"},
	{ID: "76", Newznab: "Other", Desc: "Other/Miscellaneous"},
	{ID: "60", Newznab: "TV/Anime", Desc: "Anime"},
	{ID: "1", Newznab: "PC/0day", Desc: "Appz"},
	{ID: "86", Newznab: "PC/0day", Desc: "Appz/Non-English"},
	{ID: "64", Newznab: "Audio/Audiobook", Desc: "AudioBook"},
	{ID: "35", Newznab: "Books", Desc: "Books"},
	{ID: "102", Newznab: "Books", Desc: "Books/Non-English"},
	{ID: "94", Newznab: "Books/Comics", Desc: "Books/Comics"},
	{ID: "95", Newznab: "Books/Other", Desc: "Books/Educational"},
	{ID: "98", Newznab: "Other", Desc: "Other/Fonts"},
	{ID: "69", Newznab: "PC/Mac", Desc: "Appz/Mac"},
	{ID: "92", Newznab: "Books/Mags", Desc: "Books/Magazines & Newspapers"},
	{ID: "58", Newznab: "PC/Mobile-Other", Desc: "Appz/Mobile"},
	{ID: "36", Newznab: "Other", Desc: "Other/Pics/Wallpapers"},
	{ID: "88", Newznab: "XXX", Desc: "XXX"},
	{ID: "85", Newznab: "XXX/Other", Desc: "XXX/Magazines"},
	{ID: "8", Newznab: "XXX", Desc: "XXX/Movie"},
	{ID: "81", Newznab: "XXX", Desc: "XXX/Movie/0Day"},
	{ID: "91", Newznab: "XXX/Pack", Desc: "XXX/Packs"},
	{ID: "84", Newznab: "XXX/ImageSet", Desc: "XXX/Pics/Wallpapers"},
}
