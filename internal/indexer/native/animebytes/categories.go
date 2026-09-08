package animebytes

import (
	"slices"
	"strings"
)

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
	return slices.Contains([]string{"TV Series", "OVA", "ONA"}, groupName)
}

// isMovieGroup reports whether a GroupName is a movie type mapped to Movies.
func isMovieGroup(groupName string) bool {
	return groupName == "Movie" || groupName == "Live Action Movie"
}

// isBookCategory reports whether a CategoryName is a printed-media type mapped to
// Books/Comics.
func isBookCategory(categoryName string) bool {
	return slices.Contains([]string{"Manga", "Oneshot", "Anthology", "Manhwa", "Manhua", "Light Novel", "Novel", "Artbook"}, categoryName)
}

// isGameCategory reports whether a CategoryName is a game type (the platform property then
// selects Console/PC).
func isGameCategory(categoryName string) bool {
	return categoryName == "Game" || categoryName == "Visual Novel"
}

// isMusicCategory reports whether a CategoryName is a music type (the format property then
// selects Lossless/MP3/Other).
func isMusicCategory(categoryName string) bool {
	return slices.Contains([]string{"Single", "EP", "Album", "Compilation", "Soundtrack", "Remix CD", "PV", "Live Album", "Image CD", "Drama CD", "Vocal CD"}, categoryName)
}

// gameCategories selects the Console subcategory (or PC Games) from the platform property,
// mirroring Prowlarr. An unmatched platform yields the bare Console root.
func gameCategories(props []string) []int {
	if slices.Contains(props, "PC") {
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
	case slices.Contains(props, "PSP"):
		return catConsolePSP, true
	case slices.Contains(props, "PS3"):
		return catConsolePS3, true
	case slices.Contains(props, "PS Vita"):
		return catConsolePSVita, true
	case slices.Contains(props, "3DS"):
		return catConsole3DS, true
	case slices.Contains(props, "NDS"):
		return catConsoleNDS, true
	case slices.ContainsFunc(props, func(p string) bool {
		return slices.Contains([]string{"PSX", "PS2", "SNES", "NES", "GBA", "Switch", "N64"}, p)
	}):
		return catConsoleOther, true
	default:
		return 0, false
	}
}

// musicCategories selects the Audio subcategory from the format property, mirroring
// Prowlarr: a "Lossless" property -> Lossless, an "MP3" property -> MP3, else Other.
func musicCategories(props []string) []int {
	switch {
	case slices.ContainsFunc(props, func(p string) bool { return strings.Contains(p, "Lossless") }):
		return []int{catAudio, catAudioLossless}
	case slices.ContainsFunc(props, func(p string) bool { return strings.Contains(p, "MP3") }):
		return []int{catAudio, catAudioMP3}
	default:
		return []int{catAudio, catAudioOther}
	}
}
