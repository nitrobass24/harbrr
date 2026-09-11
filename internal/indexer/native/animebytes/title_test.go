package animebytes

import "testing"

// TestReleaseInfo pins Prowlarr's AnimeBytesParser releaseInfo logic (AnimeBytes.cs:440-487),
// the nullable-season behaviour the original "season defaults to 1" code garbled:
//
//   - an Anime group with no edition seeds "S01";
//   - an "Episode N"-only edition (season stays null) yields "- NN", NOT "S01ENN - NN";
//   - a non-"Season N"/"Episode N" edition ("Director's Cut") is preserved verbatim, NOT
//     flattened to "S01";
//   - a "Season N" edition yields "SNN" (+ "ENN - NN" when an episode is also present);
//   - a non-Anime group with no edition yields "".
func TestReleaseInfo(t *testing.T) {
	t.Parallel()
	anime := func(edition string) (*group, *torrent) {
		g := &group{CategoryName: "Anime"}
		tr := &torrent{}
		if edition != "" {
			tr.EditionData = &editionData{EditionTitle: edition}
		}
		return g, tr
	}

	cases := []struct {
		name    string
		group   *group
		torrent *torrent
		want    string
	}{
		{
			name:    "anime, no edition -> S01 seed",
			group:   func() *group { g, _ := anime(""); return g }(),
			torrent: func() *torrent { _, tr := anime(""); return tr }(),
			want:    "S01",
		},
		{
			name:    "episode-only edition keeps season null -> - NN",
			group:   func() *group { g, _ := anime("Episode 12"); return g }(),
			torrent: func() *torrent { _, tr := anime("Episode 12"); return tr }(),
			want:    "- 12",
		},
		{
			name:    "non season/episode edition preserved verbatim",
			group:   func() *group { g, _ := anime("Director's Cut"); return g }(),
			torrent: func() *torrent { _, tr := anime("Director's Cut"); return tr }(),
			want:    "Director's Cut",
		},
		{
			name:    "season edition -> SNN",
			group:   func() *group { g, _ := anime("Season 3"); return g }(),
			torrent: func() *torrent { _, tr := anime("Season 3"); return tr }(),
			want:    "S03",
		},
		{
			name:    "season + episode edition -> SNNENN - NN",
			group:   func() *group { g, _ := anime("Season 2 Episode 5"); return g }(),
			torrent: func() *torrent { _, tr := anime("Season 2 Episode 5"); return tr }(),
			want:    "S02E05 - 05",
		},
		{
			name:    "non-anime group, no edition -> empty",
			group:   &group{CategoryName: "Single"},
			torrent: &torrent{},
			want:    "",
		},
		{
			name:    "html-encoded edition is decoded",
			group:   &group{CategoryName: "Single"},
			torrent: &torrent{EditionData: &editionData{EditionTitle: "Tom &amp; Jerry"}},
			want:    "Tom & Jerry",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := releaseInfo(tc.group, tc.torrent); got != tc.want {
				t.Errorf("releaseInfo = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestParseSeasonFromTitle pins the port of Prowlarr's ParseSeasonFromTitles: the
// "Nth Season"/"Season N" token, the trailing Roman numeral, and the trailing bare 2-9
// with the lookbehind exclusions RE2 cannot express (Part/No./fraction/#).
func TestParseSeasonFromTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		title    string
		want     int
		wantOK   bool
		whyNotOK string
	}{
		{title: "Kimetsu no Yaiba 2nd Season", want: 2, wantOK: true},
		{title: "Some Show 3rd season", want: 3, wantOK: true},
		{title: "Some Show Season 4", want: 4, wantOK: true},
		{title: "Some Show II", want: 2, wantOK: true},
		{title: "Some Show III", want: 3, wantOK: true},
		{title: "Some Show 2", want: 2, wantOK: true},
		{title: "Some Show S2", want: 2, wantOK: true},
		{title: "Some Show Part 2", whyNotOK: "Part is excluded"},
		{title: "Some Show No. 2", whyNotOK: "No. is excluded"},
		{title: "Some Show 1/2", whyNotOK: "a fraction is excluded"},
		{title: "Some Show #2", whyNotOK: "a numbered entry is excluded"},
		{title: "Some Show", whyNotOK: "no season token"},
		{title: "Some Show 12", whyNotOK: "a trailing multi-digit number is not a season"},
		{title: "Some Show 1", whyNotOK: "only 2-9 counts as a sequel number"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			t.Parallel()
			got, ok := parseSeasonFromTitle(tc.title)
			if ok != tc.wantOK {
				t.Fatalf("parseSeasonFromTitle(%q) ok = %v, want %v (%s)", tc.title, ok, tc.wantOK, tc.whyNotOK)
			}
			if ok && got != tc.want {
				t.Errorf("parseSeasonFromTitle(%q) = %d, want %d", tc.title, got, tc.want)
			}
		})
	}
}

// TestReleaseInfoSeasonFromTitle proves the title fallback reaches releaseInfo: an Anime
// sequel with no edition season is S02, not the S01 seed, and an "Episode N" edition on
// the same group yields "S02E05 - 05".
func TestReleaseInfoSeasonFromTitle(t *testing.T) {
	t.Parallel()
	sequel := &group{CategoryName: "Anime", SeriesName: "Kimetsu no Yaiba 2nd Season"}
	cases := []struct {
		name    string
		group   *group
		torrent *torrent
		want    string
	}{
		{
			name:    "sequel title, no edition -> S02",
			group:   sequel,
			torrent: &torrent{},
			want:    "S02",
		},
		{
			name:    "sequel title + episode edition -> S02E05 - 05",
			group:   sequel,
			torrent: &torrent{EditionData: &editionData{EditionTitle: "Episode 5"}},
			want:    "S02E05 - 05",
		},
		{
			name:    "edition season still wins over the title",
			group:   sequel,
			torrent: &torrent{EditionData: &editionData{EditionTitle: "Season 3"}},
			want:    "S03",
		},
		{
			name:    "non-anime group gets no title fallback",
			group:   &group{CategoryName: "Single", SeriesName: "Some Album II"},
			torrent: &torrent{},
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := releaseInfo(tc.group, tc.torrent); got != tc.want {
				t.Errorf("releaseInfo = %q, want %q", got, tc.want)
			}
		})
	}
}
