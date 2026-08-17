package speedapp

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func TestParseReleasesGolden(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	d := testDriver(t, &scriptDoer{})
	releases, err := d.parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(releases) != 7 {
		t.Fatalf("releases = %d, want 7", len(releases))
	}
	first := releases[0]
	want := &normalizer.Release{
		Title:                "Retro Movie",
		Description:          "Retro synthetic movie fixture",
		Details:              "https://retroflix.club/torrent/101",
		GUID:                 "https://retroflix.club/torrent/101",
		Link:                 "https://retroflix.club/api/torrent/101/download",
		Categories:           []int{2000},
		Size:                 4294967296,
		PublishDate:          "2026-08-17T08:15:30Z",
		Grabs:                42,
		Seeders:              11,
		Leechers:             3,
		Peers:                14,
		DownloadVolumeFactor: 0.5,
		UploadVolumeFactor:   2,
		MinimumRatio:         1,
		MinimumSeedTime:      432000,
		IMDBID:               "tt0000123",
		Poster:               "https://retroflix.club/posters/101.jpg",
	}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("first release =\n%+v\nwant\n%+v", first, want)
	}
	wantCategories := [][]int{{2000}, {5000}, {3020}, {5060}, {3000}, {7000}, nil}
	for i, wantCategory := range wantCategories {
		if !reflect.DeepEqual(releases[i].Categories, wantCategory) {
			t.Errorf("release[%d] categories = %v, want %v", i, releases[i].Categories, wantCategory)
		}
	}
	if releases[1].IMDBID != "" {
		t.Errorf("invalid IMDb id = %q, want empty", releases[1].IMDBID)
	}
}

func TestParseEmptyAndMalformedArrays(t *testing.T) {
	t.Parallel()
	d := testDriver(t, &scriptDoer{})
	releases, err := d.parseReleases([]byte(`[]`))
	if err != nil || len(releases) != 0 {
		t.Fatalf("empty array = %v / %v, want empty success", releases, err)
	}
	for _, body := range []string{`{`, `{}`, `null`, `"not an array"`} {
		_, err := d.parseReleases([]byte(body))
		if !errors.Is(err, search.ErrParseError) {
			t.Errorf("parseReleases(%s) = %v, want search.ErrParseError", body, err)
		}
	}
}

func TestParseInvalidDate(t *testing.T) {
	t.Parallel()
	d := testDriver(t, &scriptDoer{})
	_, err := d.parseReleases([]byte(rowsJSON(t, apiRow{ID: 1, CreatedAt: "not-a-date"})))
	if !errors.Is(err, search.ErrParseError) {
		t.Fatalf("err = %v, want search.ErrParseError", err)
	}
}

func TestTitleAndIMDBNormalization(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		in   string
		want string
	}{
		{in: "[REQUEST] Movie.", want: "Movie"},
		{in: " [requested] .Show... ", want: "Show"},
		{in: "Normal", want: "Normal"},
	} {
		if got := cleanTitle(tt.in); got != tt.want {
			t.Errorf("cleanTitle(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	for _, tt := range []struct {
		in   string
		want string
	}{
		{in: "123", want: "tt0000123"},
		{in: "tt0133093", want: "tt0133093"},
		{in: "12345678", want: "tt12345678"},
		{in: "TT123", want: ""},
		{in: "0", want: ""},
		{in: "123456789", want: ""},
		{in: "bad", want: ""},
	} {
		if got := fullIMDBID(tt.in); got != tt.want {
			t.Errorf("fullIMDBID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
