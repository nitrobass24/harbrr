package avistaz

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds is the 6s between-request pacing both Prowlarr and Jackett
// apply to the AvistaZ API; it rides on the definition's RequestDelay so the
// registry's existing paced client enforces it (no special-casing).
const requestDelaySeconds = 6.0

// Families returns the four AvistaZ-network sites as native families. Each carries
// a Go-built, caps-only definition (id/name/type/links/settings/caps) and the
// shared New factory; the per-site behaviour (AvistaZ's seasonless episode term,
// ExoticaZ's response-category parser) is keyed off the definition id inside the
// driver. They are registered with the registry, not the Cardigann loader.
//
// The settings (identical for all four sites): username is stored as-is (not a
// credential on its own); password and pid are password-typed, so the secret store
// encrypts them at rest and the API redacts them. freeleech_only is a toggle.
func Families() []native.Family {
	return []native.Family{
		{Definition: native.Site{
			ID: "avistaz", Name: "AvistaZ", Link: "https://avistaz.to/",
			Driver: "AvistaZ-family", DelaySeconds: requestDelaySeconds,
			Settings: avistazSettings(), Caps: movieTVCaps(true),
		}.Definition(), Factory: New},
		{Definition: native.Site{
			ID: "cinemaz", Name: "CinemaZ", Link: "https://cinemaz.to/",
			Driver: "AvistaZ-family", DelaySeconds: requestDelaySeconds,
			Settings: avistazSettings(), Caps: movieTVCaps(false),
		}.Definition(), Factory: New},
		{Definition: native.Site{
			ID: "privatehd", Name: "PrivateHD", Link: "https://privatehd.to/",
			Driver: "AvistaZ-family", DelaySeconds: requestDelaySeconds,
			Settings: avistazSettings(), Caps: movieTVCaps(true),
		}.Definition(), Factory: New},
		{Definition: native.Site{
			ID: "exoticaz", Name: "ExoticaZ", Link: "https://exoticaz.to/",
			Driver: "AvistaZ-family", DelaySeconds: requestDelaySeconds,
			Settings: avistazSettings(), Caps: exoticaCaps(),
		}.Definition(), Factory: New},
	}
}

// avistazSettings composes the network-wide credential fields (see the Families
// doc comment), a fresh slice per site.
func avistazSettings() []loader.SettingsField {
	return []loader.SettingsField{
		native.FieldUsername, native.FieldPassword, native.FieldPID, native.FieldFreeleechOnly,
	}
}

// movieTVCaps is the AvistaZ/CinemaZ/PrivateHD caps: tracker category 1→Movies(+UHD/
// HD/SD), 2→TV(+UHD/HD/SD), mirroring Prowlarr's AddCategoryMapping. withTvdbTmdb is
// false for CinemaZ (which advertises neither tvdbid nor tmdbid).
//
// Prowlarr additionally advertises a `genre` param (forwarded as the API `tags=`
// filter). harbrr's shared search.Query carries no genre field — no harbrr indexer
// forwards it — so advertising it here would accept the param and silently drop it,
// a worse divergence than omitting it. It is deliberately omitted; see the native
// testdata README divergence note.
func movieTVCaps(withTvdbTmdb bool) loader.Caps {
	movie := []string{"q", "imdbid"}
	tv := []string{"q", "season", "ep", "imdbid"}
	if withTvdbTmdb {
		movie = append(movie, "tmdbid")
		tv = append(tv, "tvdbid")
	}
	allowIMDB := true
	return loader.Caps{
		CategoryMappings: native.Cats(
			native.Cat{ID: "1", Newznab: "Movies"}, native.Cat{ID: "1", Newznab: "Movies/UHD"},
			native.Cat{ID: "1", Newznab: "Movies/HD"}, native.Cat{ID: "1", Newznab: "Movies/SD"},
			native.Cat{ID: "2", Newznab: "TV"}, native.Cat{ID: "2", Newznab: "TV/UHD"},
			native.Cat{ID: "2", Newznab: "TV/HD"}, native.Cat{ID: "2", Newznab: "TV/SD"},
		),
		Modes:             loader.Modes{Search: []string{"q"}, MovieSearch: movie, TVSearch: tv},
		AllowTVSearchIMDB: &allowIMDB,
	}
}

// exoticaCaps is the ExoticaZ (adult) caps: an 8-entry XXX map keyed by the tracker
// category id its response `category` dict carries, and basic search only (no
// TV/movie id params), mirroring Prowlarr's ExoticaZ.
func exoticaCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: native.Cats(
			native.Cat{ID: "1", Newznab: "XXX/x264", Desc: "Video Clip"},
			native.Cat{ID: "2", Newznab: "XXX/Pack", Desc: "Video Pack"},
			native.Cat{ID: "3", Newznab: "XXX/Pack", Desc: "Siterip Pack"},
			native.Cat{ID: "4", Newznab: "XXX/Pack", Desc: "Pornstar Pack"},
			native.Cat{ID: "5", Newznab: "XXX/DVD", Desc: "DVD"},
			native.Cat{ID: "6", Newznab: "XXX/x264", Desc: "BluRay"},
			native.Cat{ID: "7", Newznab: "XXX/ImageSet", Desc: "Photo Pack"},
			native.Cat{ID: "8", Newznab: "XXX/ImageSet", Desc: "Books & Magazines"},
		),
		Modes: loader.Modes{Search: []string{"q"}},
	}
}
