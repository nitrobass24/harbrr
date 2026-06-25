package passthepopcorn

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// parseDriver builds a full driver (caps + cfg) for the parser tests: parse needs the
// caps (the CategoryId map) and the base URL (download/info URLs).
func parseDriver(t *testing.T, cfg map[string]string) *driver {
	t.Helper()
	def := Families()[0].Definition
	d, err := New(native.Params{Def: def, Cfg: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

// TestParseReleasesGolden parses the synthetic search response and asserts the full
// movie-group × torrent flatten: title=ReleaseName verbatim, the secret-free download
// URL, the info URL, the movie-group category (all -> Movies 2000), peers=seeders+
// leechers, grabs=snatched, size, the shared imdb/year/genre/poster, the UploadTime->UTC
// publish date, and the FreeleechType-driven volume factors. It pins the deterministic
// PublishDate-descending sort (2021 > 2018 > 2015). The Id field exercises both the int
// (1100) and string ("1101") wire forms. The goldens are derived from Prowlarr's PTP
// parse contract + autobrr's pkg/ptp shape, not a live capture.
func TestParseReleasesGolden(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := parseDriver(t, nil).parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}

	base := "https://passthepopcorn.me/"
	want := []*normalizer.Release{
		{
			Title:      "Some Collection 2005 2160p UHD BluRay x265-GROUP",
			Link:       base + "torrents.php?action=download&id=2200",
			Details:    base + "torrents.php?id=55021&torrentid=2200",
			Categories: []int{2000},
			Size:       32212254720, Grabs: 12,
			Seeders: 100, Leechers: 20, Peers: 120,
			PublishDate:          "2021-11-20T23:59:59Z",
			Year:                 2005,
			DownloadVolumeFactor: 0, UploadVolumeFactor: 1,
			MinimumRatio: 1, MinimumSeedTime: 345600,
		},
		{
			Title:      "The Matrix 1999 1080p BluRay x264-GROUP",
			Link:       base + "torrents.php?action=download&id=1100",
			Details:    base + "torrents.php?id=84813&torrentid=1100",
			Categories: []int{2000},
			Size:       12884901888, Grabs: 321,
			Seeders: 50, Leechers: 3, Peers: 53,
			PublishDate:          "2018-05-01T12:30:00Z",
			IMDBID:               "tt0133093",
			Year:                 1999,
			Genre:                "action, sci.fi",
			Poster:               "https://passthepopcorn.me/cover/matrix.jpg",
			DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
			MinimumRatio: 1, MinimumSeedTime: 345600,
		},
		{
			Title:      "The Matrix 1999 DVDRip XviD-GROUP",
			Link:       base + "torrents.php?action=download&id=1101",
			Details:    base + "torrents.php?id=84813&torrentid=1101",
			Categories: []int{2000},
			Size:       734003200, Grabs: 999,
			Seeders: 7, Leechers: 1, Peers: 8,
			PublishDate:          "2015-02-10T08:00:00Z",
			IMDBID:               "tt0133093",
			Year:                 1999,
			Genre:                "action, sci.fi",
			Poster:               "https://passthepopcorn.me/cover/matrix.jpg",
			DownloadVolumeFactor: 0.5, UploadVolumeFactor: 1,
			MinimumRatio: 1, MinimumSeedTime: 345600,
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

// TestParseFreeleechVolumeFactors pins the FreeleechType -> volume-factor mapping across
// all three PTP freeleech kinds (Prowlarr PassThePopcornParser): Freeleech (DVF 0/UVF 1),
// Neutral Leech (DVF 0/UVF 0), Half Leech (DVF 0.5/UVF 1).
func TestParseFreeleechVolumeFactors(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/freeleech_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := parseDriver(t, nil).parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	// Sorted by PublishDate desc: Freeleech (10:00) > Neutral (09:00) > Half (08:00).
	want := []struct {
		title string
		dvf   float64
		uvf   float64
	}{
		{"Freeleech Movie 2010 Freeleech", 0, 1},
		{"Freeleech Movie 2010 Neutral", 0, 0},
		{"Freeleech Movie 2010 Half", 0.5, 1},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d releases, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Title != w.title {
			t.Errorf("release[%d] title = %q, want %q", i, got[i].Title, w.title)
		}
		if got[i].DownloadVolumeFactor != w.dvf || got[i].UploadVolumeFactor != w.uvf {
			t.Errorf("release[%d] factors = (%v,%v), want (%v,%v)",
				i, got[i].DownloadVolumeFactor, got[i].UploadVolumeFactor, w.dvf, w.uvf)
		}
	}
}

// TestParseReleasesEmpty proves a TotalResults "0" / null Movies body yields zero
// releases and no error (Prowlarr's early return).
func TestParseReleasesEmpty(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/empty.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := parseDriver(t, nil).parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d releases, want 0", len(got))
	}
}

// TestParseReleasesEmptyVariants proves the several empty-page shapes (missing/blank
// TotalResults, an explicit "0", and a null Movies list) all yield zero releases.
func TestParseReleasesEmptyVariants(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"Movies":[]}`,
		`{"TotalResults":"","Movies":[]}`,
		`{"TotalResults":"0","Movies":[{"Torrents":[]}]}`,
		`{"TotalResults":"2","Movies":null}`,
	}
	d := parseDriver(t, nil)
	for _, body := range cases {
		got, err := d.parseReleases([]byte(body))
		if err != nil {
			t.Fatalf("parseReleases(%s): %v", body, err)
		}
		if len(got) != 0 {
			t.Errorf("parseReleases(%s) = %d releases, want 0", body, len(got))
		}
	}
}

// TestParseReleasesSkipsZeroID proves a torrent whose decoded Id is 0 (empty/malformed)
// is skipped: emitting it would produce a broken download link (action=download&id=0).
// The sibling row with a valid Id is still returned.
func TestParseReleasesSkipsZeroID(t *testing.T) {
	t.Parallel()
	body := `{"TotalResults":"2","Movies":[{"GroupId":"1","CategoryId":"1","Title":"M",` +
		`"Torrents":[{"Id":0,"ReleaseName":"Bad"},{"Id":"99","ReleaseName":"Good"}]}]}`
	got, err := parseDriver(t, nil).parseReleases([]byte(body))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d releases, want 1 (the id==0 row skipped)", len(got))
	}
	if got[0].Title != "Good" {
		t.Errorf("Title = %q, want the valid-id row %q", got[0].Title, "Good")
	}
	if strings.Contains(got[0].Link, "id=0") {
		t.Errorf("Link carries a broken id=0: %q", got[0].Link)
	}
}

// TestParseReleasesMalformed proves a non-JSON body is a parse error.
func TestParseReleasesMalformed(t *testing.T) {
	t.Parallel()
	if _, err := parseDriver(t, nil).parseReleases([]byte(`{`)); !errors.Is(err, search.ErrParseError) {
		t.Fatalf("err = %v, want search.ErrParseError", err)
	}
}

// TestFlexStringDecode proves flexString accepts a JSON string, a bare number, and
// degrades blank/null/garbage to 0.
func TestFlexStringDecode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int64
	}{
		{`"42"`, 42},
		{`42`, 42},
		{`""`, 0},
		{`null`, 0},
		{`"notanumber"`, 0},
	}
	for _, c := range cases {
		var s flexString
		if err := s.UnmarshalJSON([]byte(c.in)); err != nil {
			t.Errorf("UnmarshalJSON(%s): %v", c.in, err)
			continue
		}
		if got := s.int64(); got != c.want {
			t.Errorf("flexString(%s).int64() = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestFlexIntDecode proves the polymorphic Torrent.Id decodes from both an int and a
// string wire form, degrading blank/null/garbage to 0.
func TestFlexIntDecode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int64
	}{
		{`1100`, 1100},
		{`"1101"`, 1101},
		{`""`, 0},
		{`null`, 0},
		{`"x"`, 0},
	}
	for _, c := range cases {
		var n flexInt
		if err := n.UnmarshalJSON([]byte(c.in)); err != nil {
			t.Errorf("UnmarshalJSON(%s): %v", c.in, err)
			continue
		}
		if got := n.int64(); got != c.want {
			t.Errorf("flexInt(%s).int64() = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestFormatIMDB proves the digits-only ImdbId -> "tt"+7-digit feed form, with blank/0/
// non-numeric ids omitted.
func TestFormatIMDB(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"0133093", "tt0133093"},
		{"81229", "tt0081229"},
		{"", ""},
		{"0", ""},
		{"abc", ""},
	}
	for _, c := range cases {
		if got := formatIMDB(c.in); got != c.want {
			t.Errorf("formatIMDB(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPosterURL proves only an absolute http(s) cover is kept (Prowlarr GetPosterUrl).
func TestPosterURL(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"https://ptp.me/x.jpg", "https://ptp.me/x.jpg"},
		{"http://ptp.me/x.jpg", "http://ptp.me/x.jpg"},
		{"not-a-url", ""},
		{"ftp://ptp.me/x.jpg", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := posterURL(c.in); got != c.want {
			t.Errorf("posterURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPublishDate proves UploadTime is parsed as UTC RFC3339; a blank/unparseable value
// yields "".
func TestPublishDate(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"2018-05-01 12:30:00", "2018-05-01T12:30:00Z"},
		{"", ""},
		{"not a date", ""},
	}
	for _, c := range cases {
		if got := publishDate(c.in); got != c.want {
			t.Errorf("publishDate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
