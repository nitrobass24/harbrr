package gazellegames

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// requestDelaySeconds is the between-request pacing for GazelleGames. autobrr's ggn
// client rate-limits to one request every 5 seconds (rate.Every(5*time.Second)); that
// steady budget is expressed here as a 5 s RequestDelay that rides on the definition so
// the registry's existing paced client enforces it (no special-casing). Prowlarr does
// not declare an explicit delay; this stays within autobrr's measured ceiling.
const requestDelaySeconds = 5.0

// Families returns GazelleGames (GGn) as a single native family. It carries a Go-built,
// caps-only definition (id/name/type/links/settings/caps) and the shared New factory; it
// is registered with the registry, not the Cardigann loader.
func Families() []native.Family {
	return []native.Family{
		{Definition: siteDef("gazellegames", "GazelleGames", "https://gazellegames.net/"), Factory: New},
	}
}

// siteDef builds the family's caps-only definition. It is never schema-validated (it has
// no login/search/download block); it exists so mapper.Build, the credential store
// (settingFields/IsSecret), indexerInfo, and the addable-indexer list all work for a
// native family with no special case.
func siteDef(id, name, link string) *loader.Definition {
	delay := requestDelaySeconds
	return &loader.Definition{
		ID:           id,
		Name:         name,
		Description:  name + " (native Gazelle-family games driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{link},
		RequestDelay: &delay,
		Settings:     credentialSettings(),
		Caps:         gazellegamesCaps(),
	}
}

// credentialSettings are the user-entered fields. apikey is text-typed but its name
// carries the "apikey" token, so harbrr's secret store auto-classifies it as a secret
// (encrypted at rest, redacted by the API) — matching Prowlarr's PrivacyLevel.ApiKey.
// The download passkey is NOT a user setting: GGn exposes it via request=quick_user, so
// a later leaf fetches it with the apikey and persists it via PersistSetting.
func credentialSettings() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "apikey", Label: "API Key", Type: "text"},
	}
}

// gazellegamesCaps is the GazelleGames capability document, ported byte-for-byte from
// Prowlarr's GazelleGames.SetCapabilities. It registers two kinds of category mapping:
//
//   - ~90 platform-NAME mappings (desc == platform name, e.g. "Windows"->PC/Games,
//     "PlayStation 4"->Console/PS4). These are the parser's PRIMARY release-category
//     source: GazelleGamesParser derives a release's categories from the group's artist
//     NAMES via MapTrackerCatDescToNewznab(artist.Name), and on real GGn data the artist
//     name IS the platform. They are keyed on the platform name (id == desc == name,
//     mirroring Prowlarr's AddCategoryMapping("Windows", PCGames, "Windows")).
//   - 4 numeric group-categoryId mappings (1->PC/Games "Games", 2->PC/0day "Applications",
//     3->Books/EBook "E-Books", 4->Audio/Other "OST"). These are the FALLBACK the parser
//     uses (MapTrackerCatToNewznab(torrent.CategoryId)) only when the artist-name path
//     yields nothing.
//
// The search mode is basic text only (q): GGn's api.php?request=search takes a
// searchstr/groupname term, with no structured movie/tv/music/book parameters.
func gazellegamesCaps() loader.Caps {
	return loader.Caps{
		CategoryMappings: append(platformCategoryMappings(), numericCategoryMappings()...),
		Modes: loader.Modes{
			Search: []string{"q"},
		},
	}
}

// numericCategoryMappings is the fallback group-categoryId -> newznab map (Prowlarr's four
// numeric AddCategoryMapping calls). The parser consults these via
// MapTrackerCatToNewznab(torrent.CategoryId) only when the artist-name path is empty.
func numericCategoryMappings() []loader.CategoryMapping {
	return []loader.CategoryMapping{
		cat("1", "PC/Games", "Games"),
		cat("2", "PC/0day", "Applications"),
		cat("3", "Books/EBook", "E-Books"),
		cat("4", "Audio/Other", "OST"),
	}
}

// platformCategoryMappings is the platform-NAME -> newznab map, the parser's primary
// release-category source (MapTrackerCatDescToNewznab(artist.Name)). Each entry mirrors a
// Prowlarr AddCategoryMapping(name, newznabCategory, name): the platform name is both the
// tracker key and the description, so a desc lookup on the artist name resolves it. Ported
// in Prowlarr's order from GazelleGames.SetCapabilities.
func platformCategoryMappings() []loader.CategoryMapping {
	out := make([]loader.CategoryMapping, 0, len(platformCats))
	for _, p := range platformCats {
		out = append(out, cat(p.name, p.newznab, p.name))
	}
	return out
}

// platformCat is one platform-name category mapping (the Prowlarr platform name and the
// harbrr newznab category name it maps to).
type platformCat struct {
	name    string
	newznab string
}

// platformCats is Prowlarr GazelleGames.SetCapabilities's platform-name list, in order.
// The newznab names are harbrr's canonical category names (mapper.standardCategories) for
// the NewznabStandardCategory each Prowlarr entry uses.
var platformCats = []platformCat{
	// Apple
	{"Mac", "Console/Other"},
	{"iOS", "PC/Mobile-iOS"},
	{"Apple Bandai Pippin", "Console/Other"},
	{"Apple II", "Console/Other"},
	// Google
	{"Android", "PC/Mobile-Android"},
	// Microsoft
	{"DOS", "PC/Games"},
	{"Windows", "PC/Games"},
	{"Xbox", "Console/XBox"},
	{"Xbox 360", "Console/XBox 360"},
	// Nintendo
	{"Game Boy", "Console/Other"},
	{"Game Boy Advance", "Console/Other"},
	{"Game Boy Color", "Console/Other"},
	{"NES", "Console/Other"},
	{"Nintendo 64", "Console/Other"},
	{"Nintendo 3DS", "Console/Other"},
	{"New Nintendo 3DS", "Console/Other"},
	{"Nintendo DS", "Console/NDS"},
	{"Nintendo GameCube", "Console/Other"},
	{"Pokemon Mini", "Console/Other"},
	{"SNES", "Console/Other"},
	{"Switch", "Console/Other"},
	{"Virtual Boy", "Console/Other"},
	{"Wii", "Console/Wii"},
	{"Wii U", "Console/WiiU"},
	// Sony
	{"PlayStation 1", "Console/Other"},
	{"PlayStation 2", "Console/Other"},
	{"PlayStation 3", "Console/PS3"},
	{"PlayStation 4", "Console/PS4"},
	{"PlayStation Portable", "Console/PSP"},
	{"PlayStation Vita", "Console/PS Vita"},
	// Sega
	{"Dreamcast", "Console/Other"},
	{"Game Gear", "Console/Other"},
	{"Master System", "Console/Other"},
	{"Mega Drive", "Console/Other"},
	{"Pico", "Console/Other"},
	{"Saturn", "Console/Other"},
	{"SG-1000", "Console/Other"},
	// Atari
	{"Atari 2600", "Console/Other"},
	{"Atari 5200", "Console/Other"},
	{"Atari 7800", "Console/Other"},
	{"Atari Jaguar", "Console/Other"},
	{"Atari Lynx", "Console/Other"},
	{"Atari ST", "Console/Other"},
	// Amstrad
	{"Amstrad CPC", "Console/Other"},
	// Bandai
	{"Bandai WonderSwan", "Console/Other"},
	{"Bandai WonderSwan Color", "Console/Other"},
	// Commodore
	{"Commodore 64", "Console/Other"},
	{"Commodore 128", "Console/Other"},
	{"Commodore Amiga", "Console/Other"},
	{"Amiga CD32", "Console/Other"},
	{"Commodore Plus-4", "Console/Other"},
	{"Commodore VIC-20", "Console/Other"},
	// NEC
	{"NEC PC-98", "Console/Other"},
	{"NEC PC-FX", "Console/Other"},
	{"NEC SuperGrafx", "Console/Other"},
	{"NEC TurboGrafx-16", "Console/Other"},
	// Sinclair
	{"ZX Spectrum", "Console/Other"},
	// Spectravideo
	{"MSX", "Console/Other"},
	{"MSX 2", "Console/Other"},
	// Tiger
	{"Game.com", "Console/Other"},
	{"Gizmondo", "Console/Other"},
	// VTech
	{"V.Smile", "Console/Other"},
	{"CreatiVision", "Console/Other"},
	// Tabletop Games
	{"Board Game", "Console/Other"},
	{"Card Game", "Console/Other"},
	{"Miniature Wargames", "Console/Other"},
	{"Pen and Paper RPG", "Console/Other"},
	// Other
	{"3DO", "Console/Other"},
	{"Casio Loopy", "Console/Other"},
	{"Casio PV-1000", "Console/Other"},
	{"Colecovision", "Console/Other"},
	{"Emerson Arcadia 2001", "Console/Other"},
	{"Entex Adventure Vision", "Console/Other"},
	{"Epoch Super Casette Vision", "Console/Other"}, //nolint:misspell // GGn's literal platform name (byte-for-byte parity with Prowlarr)
	{"Fairchild Channel F", "Console/Other"},
	{"Funtech Super Acan", "Console/Other"},
	{"GamePark GP32", "Console/Other"},
	{"General Computer Vectrex", "Console/Other"},
	{"Interactive DVD", "Console/Other"},
	{"Linux", "Console/Other"},
	{"Hartung Game Master", "Console/Other"},
	{"Magnavox-Phillips Odyssey", "Console/Other"},
	{"Mattel Intellivision", "Console/Other"},
	{"Memotech MTX", "Console/Other"},
	{"Miles Gordon Sam Coupe", "Console/Other"},
	{"Nokia N-Gage", "Console/Other"},
	{"Oculus Quest", "Console/Other"},
	{"Ouya", "Console/Other"},
	{"Philips Videopac+", "Console/Other"},
	{"Philips CD-i", "Console/Other"},
	{"Phone/PDA", "Console/Other"},
	{"RCA Studio II", "Console/Other"},
	{"Sharp X1", "Console/Other"},
	{"Sharp X68000", "Console/Other"},
	{"SNK Neo Geo", "Console/Other"},
	{"SNK Neo Geo Pocket", "Console/Other"},
	{"Taito Type X", "Console/Other"},
	{"Tandy Color Computer", "Console/Other"},
	{"Tangerine Oric", "Console/Other"},
	{"Thomson MO5", "Console/Other"},
	{"Watara Supervision", "Console/Other"},
	{"Retro - Other", "Console/Other"},
}

// cat builds a categorymapping with a tracker id, the newznab category name, and the
// tracker's category description string (the value the response's textual category
// carries, mapped via MapTrackerCatDescToNewznab).
func cat(id, name, desc string) loader.CategoryMapping {
	return loader.CategoryMapping{ID: loader.Scalar{Value: id, Set: true}, Cat: name, Desc: desc}
}
