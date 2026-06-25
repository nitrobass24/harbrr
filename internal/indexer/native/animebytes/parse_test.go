package animebytes

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// credPass is a synthetic test secret (the configured passkey). It exists only to prove
// the scrubber/redaction paths and lives only in this test file.
const credPass = "PASSKEY32CHARSYNTHETICxxxxxxxxxx"

// parseTestDriver is a full driver (caps + cfg) for the parser tests: parse needs the cfg
// (passkey for the scrubber) and a fixed base URL for the details URLs.
func parseTestDriver(t *testing.T, cfg map[string]string) *driver {
	t.Helper()
	def := Families()[0].Definition
	d, err := New(native.Params{Def: def, Cfg: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	drv := d.(*driver)
	drv.baseURL = "https://animebytes.tv/"
	return drv
}

// TestParseReleasesGolden parses the synthetic scrape.php response and asserts the full
// group×torrent->Release mapping: the SYNTHESIZED titles (releaseGroup prefix + main
// title + Sonarr-compat S01 + bracketed infoString for the anime group; FullName + music
// infoString for the album group), the inline category mapping (TV Series->TV/Anime 5070,
// Album+MP3->Audio 3000/3010), the volume factors straight from RawDown/UpMultiplier, the
// flexInt-decoded numerics (Year and Size arrive as JSON strings in one branch and numbers
// in the other), the UTC publish dates, the rebuilt details URL, and the
// PublishDate-descending sort. The goldens are derived from Prowlarr's AnimeBytesParser,
// not a live capture.
func TestParseReleasesGolden(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/scrape_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	d := parseTestDriver(t, map[string]string{"passkey": credPass})
	got, err := d.parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}

	want := []*normalizer.Release{
		{
			Title:       "[SubGroup] Great BluRay SoftSubbed Anime S01 [Blu-ray][MKV][h264 10-bit][1080p][FLAC 2.0][Dual Audio][Softsubs (SubGroup)]",
			Description: "desc",
			Link:        "https://animebytes.tv/torrent/67890/download/" + credPass,
			Details:     "https://animebytes.tv/torrent/67890/group",
			Poster:      "https://animebytes.tv/img/x.jpg",
			Categories:  []int{catTVAnime},
			Size:        12345678901, Files: 13, Grabs: 42,
			Seeders: 10, Leechers: 2, Peers: 12,
			Year: 2020, Genre: "comedy, drama",
			PublishDate:          "2020-05-01T12:00:00Z",
			DownloadVolumeFactor: 0, UploadVolumeFactor: 1,
			MinimumRatio: 1, MinimumSeedTime: 457200,
		},
		{
			Title:      "Some OST  [MP3][320][CD]",
			Link:       "https://animebytes.tv/torrent/333/download/" + credPass,
			Details:    "https://animebytes.tv/torrent/333/group",
			Categories: []int{catAudio, catAudioMP3},
			Size:       104857600, Files: 12, Grabs: 5,
			Seeders: 3, Leechers: 0, Peers: 3,
			Year:                 2019,
			PublishDate:          "2019-08-15T09:30:00Z",
			DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
			MinimumRatio: 1, MinimumSeedTime: 259200,
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

// TestParseReleasesEmpty proves a Matches==0 envelope yields zero releases and no error.
func TestParseReleasesEmpty(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/empty_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := parseTestDriver(t, nil).parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d releases, want 0", len(got))
	}
}

// TestParseErrorEnvelope proves a non-empty error envelope surfaces the right sentinel
// (an auth-looking message -> login.ErrLoginFailed) and that an echoed passkey is scrubbed
// before the error string is built.
func TestParseErrorEnvelope(t *testing.T) {
	t.Parallel()
	d := parseTestDriver(t, map[string]string{"passkey": credPass})
	body := `{"error":"Invalid passkey ` + credPass + ` rejected"}`
	_, err := d.parseReleases([]byte(body))
	if err == nil {
		t.Fatal("want an error for the {\"error\":…} envelope")
	}
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("err = %v, want login.ErrLoginFailed", err)
	}
	if strings.Contains(err.Error(), credPass) {
		t.Errorf("error leaks the passkey: %v", err)
	}
}

// TestParseNonAuthErrorEnvelope proves a non-auth error envelope is a parse error.
func TestParseNonAuthErrorEnvelope(t *testing.T) {
	t.Parallel()
	d := parseTestDriver(t, nil)
	_, err := d.parseReleases([]byte(`{"error":"Internal server error"}`))
	if !errors.Is(err, search.ErrParseError) {
		t.Errorf("err = %v, want search.ErrParseError", err)
	}
}

// TestParseMalformed proves a malformed body is a parse error.
func TestParseMalformed(t *testing.T) {
	t.Parallel()
	d := parseTestDriver(t, nil)
	if _, err := d.parseReleases([]byte(`{`)); !errors.Is(err, search.ErrParseError) {
		t.Errorf("err = %v, want search.ErrParseError", err)
	}
}

// TestFreeleechOnlyFilter proves a non-freeleech torrent (RawDownMultiplier != 0) is
// dropped when freeleech_only is set, and kept otherwise.
func TestFreeleechOnlyFilter(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/scrape_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	on := parseTestDriver(t, map[string]string{"passkey": credPass, "freeleech_only": "True"})
	got, err := on.parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	// Only the anime torrent (RawDownMultiplier 0) survives; the album torrent (1) drops.
	if len(got) != 1 {
		t.Fatalf("freeleech_only on: got %d releases, want 1", len(got))
	}
	if got[0].DownloadVolumeFactor != 0 {
		t.Errorf("surviving release DownloadVolumeFactor = %v, want 0", got[0].DownloadVolumeFactor)
	}
}

// TestSkippedSpecialGroups proves the TV/DVD/BD Special groups are dropped entirely.
func TestSkippedSpecialGroups(t *testing.T) {
	t.Parallel()
	body := `{"Matches":1,"Groups":[{"CategoryName":"Anime","GroupName":"TV Special","FullName":"X",` +
		`"Torrents":[{"ID":1,"Property":"Blu-ray","UploadTime":"2020-01-01 00:00:00","RawDownMultiplier":1,"RawUpMultiplier":1}]}]}`
	got, err := parseTestDriver(t, nil).parseReleases([]byte(body))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d releases, want 0 (TV Special is skipped)", len(got))
	}
}

// TestMovieTitle proves the movie title branch ("{group}{title} {year} {info}") and the
// Movies category.
func TestMovieTitle(t *testing.T) {
	t.Parallel()
	body := `{"Matches":1,"Groups":[{"CategoryName":"Anime Movie","GroupName":"Movie","FullName":"Big Movie","Year":2021,` +
		`"Torrents":[{"ID":9,"Property":"Blu-ray | 1080p","UploadTime":"2021-03-03 00:00:00","RawDownMultiplier":1,"RawUpMultiplier":1}]}]}`
	got, err := parseTestDriver(t, nil).parseReleases([]byte(body))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d releases, want 1", len(got))
	}
	if got[0].Title != "Big Movie 2021 [Blu-ray][1080p]" {
		t.Errorf("title = %q, want the movie composition", got[0].Title)
	}
	if !reflect.DeepEqual(got[0].Categories, []int{catMovies}) {
		t.Errorf("categories = %v, want [%d]", got[0].Categories, catMovies)
	}
}

// TestFlexIntStringAndNumber proves the flexInt decode tolerates both a JSON string and a
// bare number for the same field.
func TestFlexIntStringAndNumber(t *testing.T) {
	t.Parallel()
	var n flexInt
	if err := n.UnmarshalJSON([]byte(`"2020"`)); err != nil || n.int64() != 2020 {
		t.Errorf("string decode = %d (err %v), want 2020", n.int64(), err)
	}
	if err := n.UnmarshalJSON([]byte(`2019`)); err != nil || n.int64() != 2019 {
		t.Errorf("number decode = %d (err %v), want 2019", n.int64(), err)
	}
	if err := n.UnmarshalJSON([]byte(`"x"`)); err != nil || n.int64() != 0 {
		t.Errorf("malformed decode = %d (err %v), want 0", n.int64(), err)
	}
}
