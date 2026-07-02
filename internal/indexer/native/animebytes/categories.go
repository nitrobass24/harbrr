package animebytes

import "strings"

// categories maps a group to its newznab category ids inline, reproducing Prowlarr's
// AnimeBytesParser category logic. AnimeBytes' scrape.php has no numeric tracker category,
// so the mapping keys off the group's GroupName (anime video / movie), its CategoryName
// (printed media / games / music), and — for games and music — the torrent Property
// descriptors. The order of the checks mirrors Prowlarr (later matches win, so the music
// and game branches override an earlier video assignment only when their CategoryName
// applies). A group that matches nothing yields no categories.
func categories(g *group, props []string) []int {
	switch {
	case isAnimeVideoGroup(g.GroupName):
		return []int{catTVAnime}
	case isMovieGroup(g.GroupName):
		return []int{catMovies}
	case isBookCategory(g.CategoryName):
		return []int{catBooksComics}
	case isGameCategory(g.CategoryName):
		return gameCategories(props)
	case isMusicCategory(g.CategoryName):
		return musicCategories(props)
	default:
		return nil
	}
}

// isAnimeVideoGroup reports whether a GroupName is an anime video type mapped to TV/Anime.
func isAnimeVideoGroup(groupName string) bool {
	switch groupName {
	case "TV Series", "OVA", "ONA":
		return true
	default:
		return false
	}
}

// isMovieGroup reports whether a GroupName is a movie type mapped to Movies.
func isMovieGroup(groupName string) bool {
	return groupName == "Movie" || groupName == "Live Action Movie"
}

// isBookCategory reports whether a CategoryName is a printed-media type mapped to
// Books/Comics.
func isBookCategory(categoryName string) bool {
	switch categoryName {
	case "Manga", "Oneshot", "Anthology", "Manhwa", "Manhua", "Light Novel", "Novel", "Artbook":
		return true
	default:
		return false
	}
}

// isGameCategory reports whether a CategoryName is a game type (the platform property then
// selects Console/PC).
func isGameCategory(categoryName string) bool {
	return categoryName == "Game" || categoryName == "Visual Novel"
}

// isMusicCategory reports whether a CategoryName is a music type (the format property then
// selects Lossless/MP3/Other).
func isMusicCategory(categoryName string) bool {
	switch categoryName {
	case "Single", "EP", "Album", "Compilation", "Soundtrack", "Remix CD", "PV", "Live Album", "Image CD", "Drama CD", "Vocal CD":
		return true
	default:
		return false
	}
}

// gameCategories selects the Console subcategory (or PC Games) from the platform property,
// mirroring Prowlarr. An unmatched platform yields the bare Console root.
func gameCategories(props []string) []int {
	if hasProperty(props, "PC") {
		return []int{catPCGames}
	}
	if sub, ok := consoleSubcategory(props); ok {
		return []int{catConsole, sub}
	}
	return []int{catConsole}
}

// consoleSubcategory resolves a console platform property to its newznab subcategory.
func consoleSubcategory(props []string) (int, bool) {
	switch {
	case hasProperty(props, "PSP"):
		return catConsolePSP, true
	case hasProperty(props, "PS3"):
		return catConsolePS3, true
	case hasProperty(props, "PS Vita"):
		return catConsolePSVita, true
	case hasProperty(props, "3DS"):
		return catConsole3DS, true
	case hasProperty(props, "NDS"):
		return catConsoleNDS, true
	case hasAnyProperty(props, "PSX", "PS2", "SNES", "NES", "GBA", "Switch", "N64"):
		return catConsoleOther, true
	default:
		return 0, false
	}
}

// musicCategories selects the Audio subcategory from the format property, mirroring
// Prowlarr: a "Lossless" property -> Lossless, an "MP3" property -> MP3, else Other.
func musicCategories(props []string) []int {
	switch {
	case hasPropertyContains(props, "Lossless"):
		return []int{catAudio, catAudioLossless}
	case hasPropertyContains(props, "MP3"):
		return []int{catAudio, catAudioMP3}
	default:
		return []int{catAudio, catAudioOther}
	}
}

// hasProperty reports whether any property equals name exactly (Prowlarr's
// properties.Contains, ordinal).
func hasProperty(props []string, name string) bool {
	for _, p := range props {
		if p == name {
			return true
		}
	}
	return false
}

// hasAnyProperty reports whether any property equals one of the names exactly.
func hasAnyProperty(props []string, names ...string) bool {
	for _, n := range names {
		if hasProperty(props, n) {
			return true
		}
	}
	return false
}

// hasPropertyContains reports whether any property contains the substring (Prowlarr's
// properties.Any(p => p.Contains(...)) for the music format check).
func hasPropertyContains(props []string, substr string) bool {
	for _, p := range props {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}
