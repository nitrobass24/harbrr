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
