package animebytes

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// simpleSeasonRe / episodeRe extract a season/episode number from an EditionTitle
// ("Season 1", "Episode 12") for the Sonarr-compatible release info (Prowlarr's
// simpleSeasonRegex / episodeRegex).
var (
	simpleSeasonRe = regexp.MustCompile(`\bSeason (\d+)\b`)
	episodeRe      = regexp.MustCompile(`\bEpisode (\d+)\b`)
)

// composeTitle synthesizes a release title for a group×torrent, reproducing Prowlarr's
// AnimeBytesParser title algorithm for the PRIMARY (main) title:
//
//	movie:     "{releaseGroup}{mainTitle} {year} {infoString}"
//	non-movie: "{releaseGroup}{mainTitle}{ year?} {releaseInfo} {infoString}"
//
// then trimmed. mainTitle = SeriesName if set else HTML-decoded FullName; releaseGroup is
// a "[Group] " prefix from a Softsubs/Hardsubs/RAW/Translated property; releaseInfo is the
// Sonarr-compat S/E descriptor; infoString is each property bracketed and concatenated;
// the year is appended only when a file name contains it (Prowlarr useYearInTitle).
func composeTitle(g *group, t *torrent, props []string) string {
	mainTitle := mainTitle(g)
	infoString := infoString(props)
	releaseGroup := releaseGroupPrefix(props)
	year := g.Year.Int64()

	if g.GroupName == "Movie" || g.GroupName == "Live Action Movie" {
		return strings.TrimSpace(fmt.Sprintf("%s%s %d %s", releaseGroup, mainTitle, year, infoString))
	}

	releaseInfo := releaseInfo(g, t)
	yearPart := ""
	if useYearInTitle(g, t) {
		yearPart = " " + strconv.FormatInt(year, 10)
	}
	return strings.TrimSpace(fmt.Sprintf("%s%s%s %s %s", releaseGroup, mainTitle, yearPart, releaseInfo, infoString))
}

// mainTitle is the group's primary title: SeriesName when set, otherwise the HTML-decoded
// FullName (Prowlarr's mainTitle selection).
func mainTitle(g *group) string {
	if strings.TrimSpace(g.SeriesName) != "" {
		return g.SeriesName
	}
	return html.UnescapeString(g.FullName)
}

// infoString brackets each property and concatenates them with no separator
// ("[Blu-ray][MKV]…"), matching Prowlarr's properties.Select(p => "["+p+"]").Join("").
func infoString(props []string) string {
	var b strings.Builder
	for _, p := range props {
		b.WriteString("[" + p + "]")
	}
	return b.String()
}

// releaseGroupPrefix returns the "[Group] " title prefix from the LAST property that
// starts with a common release-group keyword (Softsubs/Hardsubs/RAW/Translated) and
// carries a parenthesised group name, e.g. "Softsubs (SubGroup)" -> "[SubGroup] ". When
// none matches, the prefix is empty (Prowlarr releaseGroup).
func releaseGroupPrefix(props []string) string {
	for i := len(props) - 1; i >= 0; i-- {
		p := props[i]
		if !hasReleaseGroupPrefix(p) {
			continue
		}
		open := strings.Index(p, "(")
		closeIdx := strings.Index(p, ")")
		if open < 0 || closeIdx < 0 || closeIdx <= open {
			continue
		}
		return "[" + p[open+1:closeIdx] + "] "
	}
	return ""
}

// hasReleaseGroupPrefix reports whether a property starts (case-insensitively) with one
// of the common release-group keywords.
func hasReleaseGroupPrefix(p string) bool {
	lower := strings.ToLower(p)
	for _, prefix := range commonReleaseGroupPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// releaseInfo builds the descriptor that sits between the title and the infoString,
// reproducing Prowlarr's AnimeBytesParser releaseInfo logic (AnimeBytes.cs:440-487):
//
//	releaseInfo seeds to "S01" for an Anime group (Sonarr compatibility, on by default),
//	else "". When the torrent carries an EditionTitle it OVERRIDES the seed with the raw
//	HTML-decoded edition text; the season is then parsed from a "Season N" token (kept null
//	otherwise) and the episode from an "Episode N" token. Finally:
//	  - episode>0 && season is null  -> "- NN" (episode-only, keeps season null)
//	  - season>0                     -> "SNN" (+ "ENN - NN" when episode>0)
//	  - otherwise                    -> the seed/edition text is kept verbatim
//
// So a non-"Season N" edition ("Director's Cut") is preserved as-is rather than being
// flattened to "S01", and an "Episode N"-only edition yields "- NN", matching Prowlarr.
func releaseInfo(g *group, t *torrent) string {
	info := ""
	if isAnimeCategory(g) {
		info = "S01"
	}
	// An edition title overrides the seed with its raw HTML-decoded text; season/episode
	// are then parsed from it (season stays null unless a "Season N" token matches).
	if edition := editionTitle(t); edition != "" {
		info = edition
	}
	season, hasSeason, episode := seasonEpisode(t)
	switch {
	case episode > 0 && !hasSeason:
		info = fmt.Sprintf("- %02d", episode)
	case season > 0:
		info = fmt.Sprintf("S%02d", season)
		if episode > 0 {
			info += fmt.Sprintf("E%02d - %02d", episode, episode)
		}
	}
	return strings.TrimSpace(info)
}

// isAnimeCategory reports whether a group is an Anime group — the gate Prowlarr uses to
// seed releaseInfo to "S01" (categoryName == "Anime", with EnableSonarrCompatibility on,
// which the minimal driver keeps on).
func isAnimeCategory(g *group) bool {
	return g.CategoryName == "Anime"
}

// editionTitle returns the torrent's HTML-decoded EditionTitle, or "" when absent.
func editionTitle(t *torrent) string {
	if t.EditionData == nil {
		return ""
	}
	return html.UnescapeString(t.EditionData.EditionTitle)
}

// seasonEpisode parses the season and episode from a torrent's EditionTitle, mirroring
// Prowlarr's simpleSeasonRegex / episodeRegex. The season is NULLABLE: hasSeason is false
// unless a "Season N" token is present (Prowlarr keeps `int? season` null otherwise, which
// drives the episode-only "- NN" branch). episode is 0 when no "Episode N" token matches.
func seasonEpisode(t *torrent) (season int, hasSeason bool, episode int) {
	title := editionTitle(t)
	if title == "" {
		return 0, false, 0
	}
	if m := simpleSeasonRe.FindStringSubmatch(title); m != nil {
		season = atoiDefault(m[1], 0)
		hasSeason = true
	}
	if m := episodeRe.FindStringSubmatch(title); m != nil {
		episode = atoiDefault(m[1], 0)
	}
	return season, hasSeason, episode
}

// useYearInTitle reports whether the group year should be appended to a non-movie title:
// the year must be positive AND appear in one of the torrent's file names (Prowlarr
// useYearInTitle).
func useYearInTitle(g *group, t *torrent) bool {
	year := g.Year.Int64()
	if year <= 0 {
		return false
	}
	yearStr := strconv.FormatInt(year, 10)
	for _, f := range t.FileList {
		if strings.Contains(f.FileName, yearStr) {
			return true
		}
	}
	return false
}

// atoiDefault parses s as an int, returning def on failure.
func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
