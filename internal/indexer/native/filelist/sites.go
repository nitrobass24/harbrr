package filelist

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds is the between-request pacing for FileList. Prowlarr declares
// no explicit RequestDelay; instead FileListSettings sets QueryLimit=150 per hour,
// i.e. one query every 24 s. harbrr has no per-hour limiter, so that budget is
// expressed as a 24 s RequestDelay that rides on the definition and the registry's
// existing paced client enforces (no special-casing).
const requestDelaySeconds = 24.0

// Families returns FileList as a single native family. It carries a Go-built,
// caps-only definition (id/name/type/links/settings/caps) and the New factory; it is
// registered with the registry, not the Cardigann loader.
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("filelist", "FileList", "https://filelist.io/", filelistCaps()), Factory: New},
	}
}

// siteDef builds the family's caps-only definition. It is never schema-validated (it
// has no login/search/download block); it exists so mapper.Build, the credential
// store (settingFields/IsSecret), indexerInfo, and the addable-indexer list all work
// for a native family with no special case.
func siteDef(id, name, link string, caps loader.Caps) *loader.Definition {
	delay := requestDelaySeconds
	return &loader.Definition{
		ID:           id,
		Name:         name,
		Description:  name + " (native FileList driver)",
		Language:     "ro-RO",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         caps,
	}
}

// credentialSettings are the user-entered fields, mirroring FileListSettings.
// username is stored as-is. passkey is text-typed but its name contains "passkey",
// so harbrr's secret store auto-classifies it as a secret (encrypted at rest, redacted
// by the API) — matching Prowlarr's PrivacyLevel.Password on the passkey. freeleech_only
// is a toggle.
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "username", Label: "Username", Type: "text"},
		{Name: "passkey", Label: "Passkey", Type: "text"},
		{Name: "freeleech_only", Label: "Only freeleech", Type: "checkbox"},
	}
}

// filelistCaps is the full FileList category map ported from Prowlarr's
// FileList.SetCapabilities (every AddCategoryMapping, in order: tracker id → newznab
// category, with the FileList description string that the response `category` field
// carries and the parser maps through MapTrackerCatDescToNewznab). The search modes
// mirror Prowlarr's SupportedSearchParameters: basic q; movie q+imdbid; tv
// q+imdbid+season+ep; music q. imdbid is advertised for tv (AllowTVSearchIMDB), as
// Prowlarr advertises TvSearchParam.ImdbId.
func filelistCaps() loader.Caps {
	allowIMDB := true
	return loader.Caps{
		CategoryMappings: []loader.CategoryMapping{
			catDesc("1", "Filme SD", "Movies/SD"),
			catDesc("2", "Filme DVD", "Movies/DVD"),
			catDesc("3", "Filme DVD-RO", "Movies/Foreign"),
			catDesc("4", "Filme HD", "Movies/HD"),
			catDesc("5", "FLAC", "Audio/Lossless"),
			catDesc("6", "Filme 4K", "Movies/UHD"),
			catDesc("7", "XXX", "XXX"),
			catDesc("8", "Programe", "PC"),
			catDesc("9", "Jocuri PC", "PC/Games"),
			catDesc("10", "Jocuri Console", "Console"),
			catDesc("11", "Audio", "Audio"),
			catDesc("12", "Videoclip", "Audio/Video"),
			catDesc("13", "Sport", "TV/Sport"),
			catDesc("15", "Desene", "TV"),
			catDesc("16", "Docs", "Books"),
			catDesc("17", "Linux", "PC"),
			catDesc("18", "Diverse", "Other"),
			catDesc("19", "Filme HD-RO", "Movies/Foreign"),
			catDesc("20", "Filme Blu-Ray", "Movies/BluRay"),
			catDesc("21", "Seriale HD", "TV/HD"),
			catDesc("22", "Mobile", "PC/Mobile-Other"),
			catDesc("23", "Seriale SD", "TV/SD"),
			catDesc("24", "Anime", "TV/Anime"),
			catDesc("25", "Filme 3D", "Movies/3D"),
			catDesc("26", "Filme 4K Blu-Ray", "Movies/BluRay"),
			catDesc("27", "Seriale 4K", "TV/UHD"),
			catDesc("28", "RO Dubbed", "Movies/Foreign"),
			catDesc("28", "RO Dubbed", "TV/Foreign"),
			catDesc("31", "K-Drama", "TV/Foreign"),
		},
		Modes: loader.Modes{
			Search:      []string{"q"},
			MovieSearch: []string{"q", "imdbid"},
			TVSearch:    []string{"q", "season", "ep", "imdbid"},
			MusicSearch: []string{"q"},
		},
		AllowTVSearchIMDB: &allowIMDB,
	}
}

// catDesc builds a categorymapping with a tracker id, the newznab category name, and
// the FileList description string (its `category` response value). A desc additionally
// synthesises Jackett's custom 1:1 category (see mapper.mapCategoryMappings).
func catDesc(id, desc, name string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name, Desc: desc}
}
