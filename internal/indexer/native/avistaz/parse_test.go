package avistaz

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestParseReleasesGolden parses the synthetic torrents response and asserts the full
// DTO->Release mapping (title=file_name, peers=leech+seed, the volume multipliers, the
// size-based MinimumSeedTime, the movie_tv ids, the type+video_quality category) and
// the descending publish-date sort. The goldens are derived from Prowlarr's parse
// contract, not a live capture.
func TestParseReleasesGolden(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := builderDriver("avistaz", nil).parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}

	// Sorted by publish date descending: music (Mar) > show (Feb) > matrix (Jan).
	want := []*normalizer.Release{
		{
			Title: "Some Artist - Album 2020 FLAC", Link: "https://avistaz.to/rss/download/3/FIXTUREKEY-CCCC.torrent",
			Details: "https://avistaz.to/torrent/3-some-album", InfoHash: "cccccccccccccccccccccccccccccccccccccccc",
			Categories: []int{3000}, Size: 524288000, Files: 0, Grabs: 8,
			Seeders: 12, Leechers: 0, Peers: 12, PublishDate: "2024-03-01T00:00:00Z",
			DownloadVolumeFactor: 1, UploadVolumeFactor: 1, MinimumRatio: 1, MinimumSeedTime: 262715,
		},
		{
			Title: "Some Show S01E02 2160p WEB-DL", Link: "https://avistaz.to/rss/download/2/FIXTUREKEY-BBBB.torrent",
			Details: "https://avistaz.to/torrent/2-some-show", InfoHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Categories: []int{5045}, Size: 64424509440, Files: 1, Grabs: 5,
			Seeders: 90, Leechers: 10, Peers: 100, PublishDate: "2024-02-20T08:00:00Z",
			DownloadVolumeFactor: 1, UploadVolumeFactor: 1, MinimumRatio: 1, MinimumSeedTime: 684000,
			TVDBID: 121361,
		},
		{
			Title: "The Matrix 1999 1080p BluRay x264", Link: "https://avistaz.to/rss/download/1/FIXTUREKEY-AAAA.torrent",
			Details: "https://avistaz.to/torrent/1-the-matrix", InfoHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Categories: []int{2040}, Size: 8589934592, Files: 1, Grabs: 120,
			Seeders: 47, Leechers: 3, Peers: 50, PublishDate: "2024-01-15T10:30:00Z",
			DownloadVolumeFactor: 0, UploadVolumeFactor: 1, MinimumRatio: 1, MinimumSeedTime: 316800,
			IMDBID: "tt0133093", TMDBID: 603,
		},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d releases, want %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("release[%d] =\n  %+v\nwant\n  %+v", i, got[i], want[i])
		}
	}
}

func TestParseReleasesEmpty(t *testing.T) {
	t.Parallel()
	got, err := builderDriver("avistaz", nil).parseReleases([]byte(`{"data":[]}`))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d releases, want 0", len(got))
	}
}

func TestParseReleasesErrors(t *testing.T) {
	t.Parallel()
	d := builderDriver("avistaz", nil)
	cases := []struct {
		name, body string
	}{
		{"malformed json", `{"data": [`},
		{"no data array (empty object)", `{}`},
		{"json null", `null`},
		{"explicit null data", `{"data":null}`},
		{"unknown type", `{"data":[{"type":"GAME","created_at_iso":"2024-01-01T00:00:00Z"}]}`},
		{"unparseable date", `{"data":[{"type":"MOVIE","video_quality":"1080p","created_at_iso":"not-a-date"}]}`},
	}
	for _, tc := range cases {
		if _, err := d.parseReleases([]byte(tc.body)); !errors.Is(err, search.ErrParseError) {
			t.Errorf("%s: err = %v, want search.ErrParseError", tc.name, err)
		}
	}
}

func TestBaseCategories(t *testing.T) {
	t.Parallel()
	cases := []struct {
		typ, vq string
		want    int
	}{
		{"MOVIE", "1080p", 2040},
		{"MOVIE", "1080i", 2040},
		{"MOVIE", "720p", 2040},
		{"MOVIE", "2160p", 2045},
		{"MOVIE", "480p", 2030},
		{"movie", "", 2030}, // case-insensitive type, unknown res -> SD
		{"TV-SHOW", "720p", 5040},
		{"TV-SHOW", "2160p", 5045},
		{"TV-SHOW", "576p", 5030},
		{"MUSIC", "", 3000},
	}
	for _, tc := range cases {
		got, err := baseCategories(&avistazRelease{Type: tc.typ, VideoQuality: tc.vq})
		if err != nil {
			t.Errorf("baseCategories(%q,%q): %v", tc.typ, tc.vq, err)
			continue
		}
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("baseCategories(%q,%q) = %v, want [%d]", tc.typ, tc.vq, got, tc.want)
		}
	}
	if _, err := baseCategories(&avistazRelease{Type: "ANIME"}); !errors.Is(err, search.ErrParseError) {
		t.Errorf("unknown type err = %v, want search.ErrParseError", err)
	}
}

func TestMinimumSeedTime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		size int64
		want int64
	}{
		{0, 259200},           // absent -> 72h
		{524288000, 262715},   // 500 MiB
		{8589934592, 316800},  // 8 GiB
		{53687091200, 619200}, // exactly 50 GiB (not > 50)
		{64424509440, 684000}, // 60 GiB (log curve)
		{54760833024, 622800}, // 51 GiB (log curve)
	}
	for _, tc := range cases {
		if got := minimumSeedTime(tc.size); got != tc.want {
			t.Errorf("minimumSeedTime(%d) = %d, want %d", tc.size, got, tc.want)
		}
	}
}

func TestParsePublishDate(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"2024-01-15T10:30:00+00:00", "2024-01-15T10:30:00Z"},
		{"2024-01-15T10:30:00+02:00", "2024-01-15T08:30:00Z"}, // adjusted to UTC
		{"2024-01-15T10:30:00Z", "2024-01-15T10:30:00Z"},
		{"2024-01-15 10:30:00", "2024-01-15T10:30:00Z"}, // no zone -> UTC
		{"2024-01-15T10:30:00.123456Z", "2024-01-15T10:30:00Z"},
	}
	for _, tc := range cases {
		got, err := parsePublishDate(tc.in)
		if err != nil {
			t.Errorf("parsePublishDate(%q): %v", tc.in, err)
			continue
		}
		if got.Format("2006-01-02T15:04:05Z07:00") != tc.want {
			t.Errorf("parsePublishDate(%q) = %s, want %s", tc.in, got.Format("2006-01-02T15:04:05Z07:00"), tc.want)
		}
	}
	if _, err := parsePublishDate("nope"); !errors.Is(err, search.ErrParseError) {
		t.Errorf("unparseable err = %v, want search.ErrParseError", err)
	}
}

func TestCoerceInt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int64
	}{
		{"603", 603},
		{" 121361 ", 121361},
		{"", 0},
		{"abc", 0},
	}
	for _, tc := range cases {
		if got := coerceInt(tc.in); got != tc.want {
			t.Errorf("coerceInt(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
