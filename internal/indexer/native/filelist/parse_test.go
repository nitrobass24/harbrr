package filelist

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

// parseDriver is a full driver (caps + cfg) for the parser tests: parse needs the caps
// (category map) and the cfg (passkey for the rebuilt download URL).
func parseDriver(cfg map[string]string) *driver {
	def := Families()[0].Definition
	d, err := New(native.Params{Def: def, Cfg: cfg})
	if err != nil {
		panic(err)
	}
	drv := d.(*driver)
	drv.baseURL = "https://filelist.test/"
	return drv
}

// TestParseReleasesGolden parses the synthetic search response and asserts the full
// DTO->Release mapping (title=name, description, peers=leechers+seeders, the freeleech/
// doubleup volume factors, the fixed MinimumSeedTime/MinimumRatio, the imdb id, the
// description-keyed category, the +0300 upload_date -> UTC publish date, and the rebuilt
// download.php/details.php URLs). The goldens are derived from Prowlarr's parse
// contract, not a live capture.
func TestParseReleasesGolden(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	d := parseDriver(map[string]string{"passkey": credPass})
	got, err := d.parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}

	want := []*normalizer.Release{
		{
			Title:       "The Matrix 1999 1080p BluRay x264",
			Description: "Action, Sci-Fi",
			Link:        "https://filelist.test/download.php?id=12345&passkey=" + credPass,
			Details:     "https://filelist.test/details.php?id=12345",
			Categories:  []int{2040, 100004}, // Movies/HD + custom 1:1
			Size:        8589934592, Files: 1, Grabs: 120,
			Seeders: 47, Leechers: 3, Peers: 50,
			PublishDate:          "2024-01-15T07:30:00Z", // 10:30 +0300 -> UTC
			DownloadVolumeFactor: 0, UploadVolumeFactor: 1, MinimumRatio: 1, MinimumSeedTime: 172800,
			IMDBID: "tt0133093",
		},
		{
			Title:       "Some Show S01E02 1080p WEB-DL",
			Description: "Drama",
			Link:        "https://filelist.test/download.php?id=67890&passkey=" + credPass,
			Details:     "https://filelist.test/details.php?id=67890",
			Categories:  []int{5040, 100021}, // TV/HD + custom 1:1
			Size:        2147483648, Files: 3, Grabs: 5,
			Seeders: 90, Leechers: 10, Peers: 100,
			PublishDate:          "2024-02-20T05:00:00Z", // 08:00 +0300 -> UTC
			DownloadVolumeFactor: 1, UploadVolumeFactor: 2, MinimumRatio: 1, MinimumSeedTime: 172800,
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

// TestParseRebuiltDownloadURLCarriesPasskey proves the Link is the rebuilt
// download.php URL with the passkey query param (Prowlarr style — NOT an API link),
// and that the details URL carries no passkey.
func TestParseRebuiltDownloadURLCarriesPasskey(t *testing.T) {
	t.Parallel()
	d := parseDriver(map[string]string{"passkey": credPass})
	got, err := d.parseReleases([]byte(`[{"id":42,"name":"x","upload_date":"2024-01-01 00:00:00","category":"Filme HD"}]`))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if got[0].Link != "https://filelist.test/download.php?id=42&passkey="+credPass {
		t.Errorf("Link = %q, want the rebuilt download.php URL with passkey", got[0].Link)
	}
	if got[0].Details != "https://filelist.test/details.php?id=42" {
		t.Errorf("Details = %q, want details.php?id=42 (no passkey)", got[0].Details)
	}
}

// TestParseReleasesFreeleechFilter proves a non-freeleech row is dropped when
// freeleech_only is set (Prowlarr's parser-side filter), and kept otherwise.
func TestParseReleasesFreeleechFilter(t *testing.T) {
	t.Parallel()
	body := `[
	  {"id":1,"name":"free","upload_date":"2024-01-01 00:00:00","category":"Filme HD","freeleech":1},
	  {"id":2,"name":"paid","upload_date":"2024-01-02 00:00:00","category":"Filme HD","freeleech":0}
	]`
	on := parseDriver(map[string]string{"passkey": credPass, "freeleech_only": "True"})
	got, err := on.parseReleases([]byte(body))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 1 || got[0].Title != "free" {
		t.Fatalf("freeleech_only on: got %d releases (%v), want 1 (free only)", len(got), got)
	}

	off := parseDriver(map[string]string{"passkey": credPass})
	got, err = off.parseReleases([]byte(body))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("freeleech_only off: got %d releases, want 2", len(got))
	}
}

func TestParseReleasesEmpty(t *testing.T) {
	t.Parallel()
	got, err := parseDriver(nil).parseReleases([]byte(`[]`))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d releases, want 0", len(got))
	}
}

func TestParseReleasesErrors(t *testing.T) {
	t.Parallel()
	d := parseDriver(map[string]string{"passkey": credPass})
	cases := []struct {
		name, body string
		// wantToken, when set, is an actionable diagnostic token DecodeErrorDetail
		// must surface in the wrapped error (proving the decode error is passed
		// through, not swallowed into a bare sentinel). Empty = no token check
		// (decode-detail does not apply to that branch).
		wantToken string
	}{
		{"malformed json", `[{`, "offset"},
		{"non-json html body", `<html>not json</html>`, "bytes"},
		{"error envelope", `{"error":"Invalid passkey"}`, ""},
		{"unparseable date", `[{"id":1,"name":"x","upload_date":"not-a-date","category":"Filme HD"}]`, ""},
	}
	for _, tc := range cases {
		_, err := d.parseReleases([]byte(tc.body))
		if !errors.Is(err, search.ErrParseError) {
			t.Errorf("%s: err = %v, want search.ErrParseError", tc.name, err)
			continue
		}
		if tc.wantToken != "" && !strings.Contains(err.Error(), tc.wantToken) {
			t.Errorf("%s: err = %q, want it to contain actionable token %q", tc.name, err.Error(), tc.wantToken)
		}
	}
}

// TestParseErrorEnvelopeScrubsPasskey proves an {"error":…} envelope that echoes the
// passkey cannot leak it (scrubPasskey replaces it before the error surfaces).
func TestParseErrorEnvelopeScrubsPasskey(t *testing.T) {
	t.Parallel()
	d := parseDriver(map[string]string{"passkey": credPass})
	body := `{"error":"bad passkey ` + credPass + ` rejected"}`
	_, err := d.parseReleases([]byte(body))
	if err == nil {
		t.Fatal("want a parse error")
	}
	if strings.Contains(err.Error(), credPass) {
		t.Errorf("error leaks the passkey: %v", err)
	}
}

func TestParsePublishDate(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"2024-01-15 10:30:00", "2024-01-15T07:30:00Z"}, // +0300 -> UTC
		{"2024-06-01 00:00:00", "2024-05-31T21:00:00Z"},
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

func TestNormalizeIMDBID(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"tt0133093", "tt0133093"},
		{"133093", "tt0133093"},
		{"", ""},
		{"tt", ""}, // <= 2 chars -> empty (Prowlarr's Length > 2 guard)
	}
	for _, tc := range cases {
		if got := normalizeIMDBID(tc.in); got != tc.want {
			t.Errorf("normalizeIMDBID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
