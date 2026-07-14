package search

import (
	"testing"
)

// TestEpisodeSearchString pins Jackett's TorznabQuery.GetEpisodeSearchString:
// no (or zero) season yields nothing, a daily search (season is the year, ep is
// "MM/dd") renders "yyyy.MM.dd", a season alone renders "S%02d", a numeric
// episode renders "S%02dE%02d". The non-numeric-episode cases below pin
// harbrr's raw-append behavior, a deliberate divergence from Jackett's
// digit-stripping CoerceInt — both unreachable from a real torznab client.
func TestEpisodeSearchString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		q    Query
		want string
	}{
		{"no season no ep", Query{}, ""},
		{"ep without season", Query{Ep: "2"}, ""},
		{"season zero (specials)", Query{Season: "0", Ep: "5"}, ""},
		{"non-numeric season", Query{Season: "abc", Ep: "5"}, ""},
		{"season and episode zero-padded", Query{Season: "1", Ep: "2"}, "S01E02"},
		{"already padded input", Query{Season: "01", Ep: "02"}, "S01E02"},
		{"multi-digit episode", Query{Season: "12", Ep: "345"}, "S12E345"},
		{"season only", Query{Season: "1"}, "S01"},
		{"whitespace episode is season only", Query{Season: "1", Ep: "  "}, "S01"},
		{"daily episode date", Query{Season: "2024", Ep: "01/15"}, "2024.01.15"},
		// Non-daily (single-digit or out-of-range) falls through to raw append —
		// harbrr's deliberate divergence from Jackett's CoerceInt digit-stripping.
		{"daily needs two-digit month (ParseExact widths)", Query{Season: "2024", Ep: "1/15"}, "S2024E1/15"},
		{"out-of-range daily date falls through raw", Query{Season: "2024", Ep: "13/40"}, "S2024E13/40"},
		{"year season with numeric episode", Query{Season: "2024", Ep: "5"}, "S2024E05"},
		{"non-numeric episode appended raw", Query{Season: "3", Ep: "2v2"}, "S03E2v2"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.q.episodeSearchString(); got != tc.want {
				t.Errorf("episodeSearchString() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestKeywordsEpisodeToken pins the KeywordTokens join order (Q, Year, episode
// string — single spaces, empties skipped) so a tvsearch term carries the
// SxxExx token like Jackett's .Keywords, and a search with no season stays
// unchanged.
func TestKeywordsEpisodeToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		q    Query
		want string
	}{
		{"movie search unchanged", Query{Keywords: "Show"}, "Show"},
		{"tvsearch appends SxxExx", Query{Keywords: "Show", Season: "1", Ep: "2"}, "Show S01E02"},
		{"season-only search", Query{Keywords: "Show", Season: "1"}, "Show S01"},
		{"daily search appends the date", Query{Keywords: "Show", Season: "2024", Ep: "01/15"}, "Show 2024.01.15"},
		{"year precedes the episode token", Query{Keywords: "Show", Year: "2020", Season: "1", Ep: "2"}, "Show 2020 S01E02"},
		{"episode token alone has no stray space", Query{Season: "1", Ep: "2"}, "S01E02"},
		{"season zero appends nothing", Query{Keywords: "Show", Season: "0"}, "Show"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.q.keywords(); got != tc.want {
				t.Errorf("keywords() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestQueryMapEpisode pins the .Query.<name> episode variables: .Query.Episode
// is the FORMATTED episode string (Jackett sets variables[".Query.Episode"] =
// query.GetEpisodeSearchString()), .Query.Ep stays raw, and .Query.Season is
// absent for season 0 (Jackett: query.Season > 0 ? ... : null).
func TestQueryMapEpisode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		q           Query
		wantSeason  string
		wantEp      string
		wantEpisode string
	}{
		{"tvsearch", Query{Season: "1", Ep: "2"}, "1", "2", "S01E02"},
		{"daily", Query{Season: "2024", Ep: "01/15"}, "2024", "01/15", "2024.01.15"},
		{"season zero nulled", Query{Season: "0", Ep: "5"}, "", "5", ""},
		{"ep without season", Query{Ep: "2"}, "", "2", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := tc.q.queryMap()
			if got := m["Season"]; got != tc.wantSeason {
				t.Errorf(".Query.Season = %q, want %q", got, tc.wantSeason)
			}
			if got := m["Ep"]; got != tc.wantEp {
				t.Errorf(".Query.Ep = %q, want %q", got, tc.wantEp)
			}
			if got := m["Episode"]; got != tc.wantEpisode {
				t.Errorf(".Query.Episode = %q, want %q", got, tc.wantEpisode)
			}
		})
	}
}
