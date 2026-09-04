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
//
// The settings: cookie is the full pasted session Cookie string (e.g. uid=...;
// pass=...) — its name contains "cookie", so loader.SettingsField.IsSecret() classifies
// the text field as a secret (encrypted at rest, redacted by the API). freeleech_only
// is a toggle that restricts results to freeleech torrents (download-multiplier 0).
func Families() []native.Family {
	return []native.Family{
		{Definition: native.Site{
			ID: "torrentday", Name: "TorrentDay", Link: "https://www.torrentday.com/",
			Driver: "JSON-search", DelaySeconds: requestDelaySeconds,
			Settings: []loader.SettingsField{native.FieldCookie, native.FieldFreeleechOnly},
			Caps:     tdCaps(),
		}.Definition(), Factory: New},
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
// name. The Desc is Prowlarr's human label, kept for parity in the addable list; the
// Newznab name is what mapper.GetByName resolves to a newznab id.
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
	return native.Cats(
		native.Cat{ID: "25", Newznab: "Movies/SD", Desc: "Movies/480p"},
		native.Cat{ID: "96", Newznab: "Movies/UHD", Desc: "Movie/4K"},
		native.Cat{ID: "11", Newznab: "Movies/BluRay", Desc: "Movies/Bluray"},
		native.Cat{ID: "5", Newznab: "Movies/BluRay", Desc: "Movies/Bluray-Full"},
		native.Cat{ID: "103", Newznab: "Movies/SD", Desc: "Movies/Cam"},
		native.Cat{ID: "3", Newznab: "Movies/DVD", Desc: "Movies/DVD-R"},
		native.Cat{ID: "21", Newznab: "Movies/SD", Desc: "Movies/MP4"},
		native.Cat{ID: "22", Newznab: "Movies/Foreign", Desc: "Movies/Non-English"},
		native.Cat{ID: "13", Newznab: "Movies", Desc: "Movies/Packs"},
		native.Cat{ID: "44", Newznab: "Movies/SD", Desc: "Movies/SD/x264"},
		native.Cat{ID: "48", Newznab: "Movies", Desc: "Movies/x265"},
		native.Cat{ID: "1", Newznab: "Movies/SD", Desc: "Movies/XviD"},
	)
}

func tdTVCats() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "24", Newznab: "TV/SD", Desc: "TV/480p"},
		native.Cat{ID: "104", Newznab: "TV/UHD", Desc: "TV/4K"},
		native.Cat{ID: "32", Newznab: "TV/HD", Desc: "TV/Bluray"},
		native.Cat{ID: "31", Newznab: "TV/SD", Desc: "TV/DVD-R"},
		native.Cat{ID: "33", Newznab: "TV/SD", Desc: "TV/DVD-Rip"},
		native.Cat{ID: "46", Newznab: "TV/SD", Desc: "TV/Mobile"},
		native.Cat{ID: "82", Newznab: "TV/Foreign", Desc: "TV/Non-English"},
		native.Cat{ID: "14", Newznab: "TV", Desc: "TV/Packs"},
		native.Cat{ID: "26", Newznab: "TV/SD", Desc: "TV/SD/x264"},
		native.Cat{ID: "7", Newznab: "TV/HD", Desc: "TV/x264"},
		native.Cat{ID: "34", Newznab: "TV/HD", Desc: "TV/x265"},
		native.Cat{ID: "2", Newznab: "TV/SD", Desc: "TV/XviD"},
	)
}

func tdGameCats() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "10", Newznab: "Console/NDS", Desc: "Nintendo"},
		native.Cat{ID: "4", Newznab: "PC/Games", Desc: "PC/Games"},
		native.Cat{ID: "18", Newznab: "Console/PS3", Desc: "PS"},
		native.Cat{ID: "8", Newznab: "Console/PSP", Desc: "PSP"},
		native.Cat{ID: "9", Newznab: "Console/XBox", Desc: "Xbox"},
	)
}

func tdMusicCats() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "17", Newznab: "Audio/MP3", Desc: "Music/Audio"},
		native.Cat{ID: "27", Newznab: "Audio", Desc: "Music/Flac"},
		native.Cat{ID: "23", Newznab: "Audio/Foreign", Desc: "Music/Non-English"},
		native.Cat{ID: "41", Newznab: "Audio", Desc: "Music/Packs"},
		native.Cat{ID: "16", Newznab: "Audio/Video", Desc: "Music/Video"},
	)
}

func tdOtherCats() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "29", Newznab: "TV/Anime", Desc: "Anime"},
		native.Cat{ID: "42", Newznab: "Audio/Audiobook", Desc: "Audio Books"},
		native.Cat{ID: "20", Newznab: "Books", Desc: "Books"},
		native.Cat{ID: "102", Newznab: "Books/Foreign", Desc: "Books/Non-English"},
		native.Cat{ID: "30", Newznab: "TV/Documentary", Desc: "Documentary"},
		native.Cat{ID: "95", Newznab: "TV/Documentary", Desc: "Educational"},
		native.Cat{ID: "47", Newznab: "Other", Desc: "Fonts"},
		native.Cat{ID: "43", Newznab: "PC/Mac", Desc: "Mac"},
		native.Cat{ID: "45", Newznab: "Audio/Other", Desc: "Podcast"},
		native.Cat{ID: "28", Newznab: "PC", Desc: "Softwa/Packs"},
		native.Cat{ID: "12", Newznab: "PC", Desc: "Software"},
	)
}

func tdXXXCats() []loader.CategoryMapping {
	return native.Cats(
		native.Cat{ID: "19", Newznab: "XXX", Desc: "XXX/0Day"},
		native.Cat{ID: "6", Newznab: "XXX", Desc: "XXX/Movies"},
		native.Cat{ID: "15", Newznab: "XXX/Pack", Desc: "XXX/Packs"},
	)
}
