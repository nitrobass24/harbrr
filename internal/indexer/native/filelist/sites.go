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
//
// The settings mirror FileListSettings. username is stored as-is. passkey is
// text-typed but its name contains "passkey", so harbrr's secret store auto-classifies
// it as a secret (encrypted at rest, redacted by the API) — matching Prowlarr's
// PrivacyLevel.Password on the passkey. freeleech_only is a toggle.
func Families() []native.Family {
	return []native.Family{
		{Definition: native.Site{
			ID: "filelist", Name: "FileList", Link: "https://filelist.io/",
			Driver: "FileList", DelaySeconds: requestDelaySeconds, Language: "ro-RO",
			Settings: []loader.SettingsField{native.FieldUsername, native.FieldPasskey, native.FieldFreeleechOnly},
			Caps:     filelistCaps(),
		}.Definition(), Factory: New},
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
		CategoryMappings: native.Cats(
			native.Cat{ID: "1", Newznab: "Movies/SD", Desc: "Filme SD"},
			native.Cat{ID: "2", Newznab: "Movies/DVD", Desc: "Filme DVD"},
			native.Cat{ID: "3", Newznab: "Movies/Foreign", Desc: "Filme DVD-RO"},
			native.Cat{ID: "4", Newznab: "Movies/HD", Desc: "Filme HD"},
			native.Cat{ID: "5", Newznab: "Audio/Lossless", Desc: "FLAC"},
			native.Cat{ID: "6", Newznab: "Movies/UHD", Desc: "Filme 4K"},
			native.Cat{ID: "7", Newznab: "XXX", Desc: "XXX"},
			native.Cat{ID: "8", Newznab: "PC", Desc: "Programe"},
			native.Cat{ID: "9", Newznab: "PC/Games", Desc: "Jocuri PC"},
			native.Cat{ID: "10", Newznab: "Console", Desc: "Jocuri Console"},
			native.Cat{ID: "11", Newznab: "Audio", Desc: "Audio"},
			native.Cat{ID: "12", Newznab: "Audio/Video", Desc: "Videoclip"},
			native.Cat{ID: "13", Newznab: "TV/Sport", Desc: "Sport"},
			native.Cat{ID: "15", Newznab: "TV", Desc: "Desene"},
			native.Cat{ID: "16", Newznab: "Books", Desc: "Docs"},
			native.Cat{ID: "17", Newznab: "PC", Desc: "Linux"},
			native.Cat{ID: "18", Newznab: "Other", Desc: "Diverse"},
			native.Cat{ID: "19", Newznab: "Movies/Foreign", Desc: "Filme HD-RO"},
			native.Cat{ID: "20", Newznab: "Movies/BluRay", Desc: "Filme Blu-Ray"},
			native.Cat{ID: "21", Newznab: "TV/HD", Desc: "Seriale HD"},
			native.Cat{ID: "22", Newznab: "PC/Mobile-Other", Desc: "Mobile"},
			native.Cat{ID: "23", Newznab: "TV/SD", Desc: "Seriale SD"},
			native.Cat{ID: "24", Newznab: "TV/Anime", Desc: "Anime"},
			native.Cat{ID: "25", Newznab: "Movies/3D", Desc: "Filme 3D"},
			native.Cat{ID: "26", Newznab: "Movies/BluRay", Desc: "Filme 4K Blu-Ray"},
			native.Cat{ID: "27", Newznab: "TV/UHD", Desc: "Seriale 4K"},
			native.Cat{ID: "28", Newznab: "Movies/Foreign", Desc: "RO Dubbed"},
			native.Cat{ID: "28", Newznab: "TV/Foreign", Desc: "RO Dubbed"},
			native.Cat{ID: "31", Newznab: "TV/Foreign", Desc: "K-Drama"},
		),
		Modes: loader.Modes{
			Search:      []string{"q"},
			MovieSearch: []string{"q", "imdbid"},
			TVSearch:    []string{"q", "season", "ep", "imdbid"},
			MusicSearch: []string{"q"},
		},
		AllowTVSearchIMDB: &allowIMDB,
	}
}
