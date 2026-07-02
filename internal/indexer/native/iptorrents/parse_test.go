package iptorrents

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestParseGolden scrapes the synthetic torrent-list page and asserts the full
// row->Release mapping: title, the absolute download/details links, the category from
// the icon href, the size/seeders/leechers resolved BY HEADER TEXT (the golden puts the
// size column at a non-default index to prove resolution is not positional), the
// freeleech DownloadVolumeFactor, and a deterministic publish date from the relative
// "time ago" string against the fixed clock. The golden is derived from the Prowlarr
// selector contract, not a live capture.
func TestParseGolden(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search_results.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := testDriver(nil, nil).parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}

	// Rows are emitted in document order (the parser does not re-sort).
	want := []*normalizer.Release{
		{
			Title:                "The Matrix 1999 1080p BluRay x264-GROUP",
			Link:                 "https://iptorrents.com/download.php/123456/The.Matrix.1999.1080p.BluRay.x264-GROUP.torrent",
			Details:              "https://iptorrents.com/t/123456",
			Categories:           []int{2000, 100072},
			Size:                 9126805504, // 8.5 GB in Jackett's float32 chain
			Grabs:                120,
			Seeders:              47,
			Leechers:             3,
			Peers:                50,
			PublishDate:          "2026-06-15T09:30:00Z", // clock 12:00 - 2.5h
			DownloadVolumeFactor: 0,                      // span.free present
			UploadVolumeFactor:   1,
			MinimumRatio:         1,
			MinimumSeedTime:      1209600,
		},
		{
			Title:                "Some Show S01E02 720p WEB-DL-GROUP",
			Link:                 "https://iptorrents.com/download.php/654321/Some.Show.S01E02.720p.WEB-DL-GROUP.torrent",
			Details:              "https://iptorrents.com/t/654321",
			Categories:           []int{5000, 100073},
			Size:                 1288490240, // 1.2 GB
			Grabs:                5,
			Seeders:              90,
			Leechers:             10,
			Peers:                100,
			PublishDate:          "2026-06-12T12:00:00Z", // clock - 3 days
			DownloadVolumeFactor: 1,                      // no span.free
			UploadVolumeFactor:   1,
			MinimumRatio:         1,
			MinimumSeedTime:      1209600,
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

// TestParseColumnByHeaderText is the explicit proof that stat columns are resolved by
// header text, not a hardcoded index: in the golden, "Sort by size" is at column index
// 3, whereas Prowlarr's positional default is 5. A positional parser would read the
// seeders cell (or out of range) as the size; the header-resolved parser reads 8.5 GB.
func TestParseColumnByHeaderText(t *testing.T) {
	t.Parallel()
	body, _ := os.ReadFile("testdata/search_results.html")
	got, err := testDriver(nil, nil).parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if got[0].Size != 9126805504 {
		t.Fatalf("size = %d, want 9126805504 (size column resolved by header, not the default index 5)", got[0].Size)
	}
	if got[0].Seeders != 47 || got[0].Leechers != 3 {
		t.Errorf("seeders/leechers = %d/%d, want 47/3 (resolved by header)", got[0].Seeders, got[0].Leechers)
	}
}

// TestParseSkipsRowsWithoutLink proves a header/no-results row (no a.hv) and a row
// missing a download link are skipped, not errors.
func TestParseSkipsRowsWithoutLink(t *testing.T) {
	t.Parallel()
	html := `<table id="torrents"><tbody>
	  <tr><td>no title link, just a cell</td></tr>
	  <tr><td><a class="hv" href="/t/1">Has title but no download</a><div class="sub">1 hour ago</div></td></tr>
	</tbody></table>`
	got, err := testDriver(nil, nil).parseReleases([]byte(html))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d releases, want 0 (both rows skipped)", len(got))
	}
}

// TestParseBadDate proves an unparseable relative date on an otherwise complete row is a
// parse error (Prowlarr throws InvalidDateException).
func TestParseBadDate(t *testing.T) {
	t.Parallel()
	html := `<table id="torrents"><tbody>
	  <tr>
	    <td><a href="?72">cat</a></td>
	    <td><a class="hv" href="/t/1">Title</a><a href="/download.php/1/x.torrent">dl</a><div class="sub">at some point</div></td>
	    <td>5 GB</td><td>1</td><td>1</td>
	  </tr>
	</tbody></table>`
	_, err := testDriver(nil, nil).parseReleases([]byte(html))
	if !errors.Is(err, search.ErrParseError) {
		t.Errorf("err = %v, want search.ErrParseError", err)
	}
}

func TestCleanTitle(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"The Matrix 1999", "The Matrix 1999"},
		{"  Some.Movie  ", "Some.Movie"},
		{"[REQ] Wanted Movie", "Wanted Movie"},
		{"[REQUESTED] A Show", "A Show"},
		{"- Dashed Title :", "Dashed Title"},
	}
	for _, tc := range cases {
		if got := cleanTitle(tc.in); got != tc.want {
			t.Errorf("cleanTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseSizeBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int64
	}{
		{"8.5 GB", 9126805504},
		{"1.2 GB", 1288490240},
		{"500 MB", 524288000},
		{"1,018.29 MB", 1067754432},
		{"700 KB", 716800},
		{"-", 0},
	}
	for _, tc := range cases {
		if got := parseSizeBytes(tc.in); got != tc.want {
			t.Errorf("parseSizeBytes(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
