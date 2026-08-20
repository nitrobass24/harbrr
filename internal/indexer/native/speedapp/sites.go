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
			CategoryMappings: []loader.CategoryMapping{
				category("401", "Movies", "Movies"),
				category("402", "TV Series", "TV"),
				category("406", "Music Videos", "Audio/Video"),
				category("407", "Sports", "TV/Sport"),
				category("408", "HQ Audio", "Audio"),
				category("409", "Books", "Books"),
			},
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

func category(id, desc, name string) loader.CategoryMapping {
	return loader.CategoryMapping{
		ID:   loader.Scalar{Value: id, Set: true},
		Cat:  name,
		Desc: desc,
	}
}
