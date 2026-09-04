package speedapp

import (
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const requestDelaySeconds = 2.1

// Families returns RetroFlix as a native SpeedApp family.
func Families() []native.Family {
	return []native.Family{{Definition: retroFlixDefinition(), Factory: New}}
}

// Definition returns RetroFlix's static settings and capabilities definition.
func Definition() *loader.Definition { return retroFlixDefinition() }

// retroFlixDefinition is hand-built rather than native.Site{}.Definition(): Site
// carries a single Link and RetroFlix serves two alternate domains, and the
// credential fields carry Required, which the kit's Field* constants deliberately
// do not (the alpharatio precedent). Everything Site would have supplied
// identically — the category table — goes through the kit below.
func retroFlixDefinition() *loader.Definition {
	delay := requestDelaySeconds
	allowIMDB := true
	return &loader.Definition{
		ID:           "retroflix",
		Name:         "RetroFlix",
		Description:  "RetroFlix (native SpeedApp driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{"https://retroflix.club/", "https://retroflix.net/"},
		RequestDelay: &delay,
		Settings: []loader.SettingsField{
			{Name: "email", Label: "Email", Type: "password", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
		Caps: loader.Caps{
			CategoryMappings: native.Cats(
				native.Cat{ID: "401", Newznab: "Movies", Desc: "Movies"},
				native.Cat{ID: "402", Newznab: "TV", Desc: "TV Series"},
				native.Cat{ID: "406", Newznab: "Audio/Video", Desc: "Music Videos"},
				native.Cat{ID: "407", Newznab: "TV/Sport", Desc: "Sports"},
				native.Cat{ID: "408", Newznab: "Audio", Desc: "HQ Audio"},
				native.Cat{ID: "409", Newznab: "Books", Desc: "Books"},
			),
			Modes: loader.Modes{
				Search:      []string{"q"},
				MovieSearch: []string{"q", "imdbid"},
				TVSearch:    []string{"q", "season", "ep", "imdbid"},
				MusicSearch: []string{"q"},
				BookSearch:  []string{"q"},
			},
			AllowTVSearchIMDB: &allowIMDB,
		},
	}
}
