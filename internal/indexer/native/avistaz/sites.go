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
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("avistaz", "AvistaZ", "https://avistaz.to/", movieTVCaps(true)), Factory: New},
		{Definition: siteDef("cinemaz", "CinemaZ", "https://cinemaz.to/", movieTVCaps(false)), Factory: New},
		{Definition: siteDef("privatehd", "PrivateHD", "https://privatehd.to/", movieTVCaps(true)), Factory: New},
		{Definition: siteDef("exoticaz", "ExoticaZ", "https://exoticaz.to/", exoticaCaps()), Factory: New},
	}
}

// siteDef builds one family's caps-only definition. It is never schema-validated
// (it has no login/search/download block); it exists so mapper.Build, the
// credential store (settingFields/IsSecret), indexerInfo, and the addable-indexer
// list all work for a native family with no special case.
func siteDef(id, name, link string, caps loader.Caps) *loader.Definition {
	delay := requestDelaySeconds
	return &loader.Definition{
		ID:           id,
		Name:         name,
		Description:  name + " (native AvistaZ-family driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         caps,
	}
}

// credentialSettings are the user-entered fields. username is stored as-is (not a
// credential on its own); password and pid are password-typed, so the secret store
// encrypts them at rest and the API redacts them. freeleech_only is a toggle.
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "username", Label: "Username", Type: "text"},
		{Name: "password", Label: "Password", Type: "password"},
		{Name: "pid", Label: "PID", Type: "password"},
		{Name: "freeleech_only", Label: "Only freeleech", Type: "checkbox"},
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
		CategoryMappings: []loader.CategoryMapping{
			cat("1", "Movies"), cat("1", "Movies/UHD"), cat("1", "Movies/HD"), cat("1", "Movies/SD"),
			cat("2", "TV"), cat("2", "TV/UHD"), cat("2", "TV/HD"), cat("2", "TV/SD"),
		},
		Modes:             loader.Modes{Search: []string{"q"}, MovieSearch: movie, TVSearch: tv},
		AllowTVSearchIMDB: &allowIMDB,
	}
}

// exoticaCaps is the ExoticaZ (adult) caps: an 8-entry XXX map keyed by the tracker
// category id its response `category` dict carries, and basic search only (no
// TV/movie id params), mirroring Prowlarr's ExoticaZ.
func exoticaCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: []loader.CategoryMapping{
			catDesc("1", "Video Clip", "XXX/x264"),
			catDesc("2", "Video Pack", "XXX/Pack"),
			catDesc("3", "Siterip Pack", "XXX/Pack"),
			catDesc("4", "Pornstar Pack", "XXX/Pack"),
			catDesc("5", "DVD", "XXX/DVD"),
			catDesc("6", "BluRay", "XXX/x264"),
			catDesc("7", "Photo Pack", "XXX/ImageSet"),
			catDesc("8", "Books & Magazines", "XXX/ImageSet"),
		},
		Modes: loader.Modes{Search: []string{"q"}},
	}
}

func cat(id, name string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name}
}

func catDesc(id, desc, name string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name, Desc: desc}
}
