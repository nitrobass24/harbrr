package parity

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// oracleRelease mirrors the fields of the canonical JSON output that the ported
// Jackett assertions check. It is the comparison surface for the offline oracle:
// the expected values below are Jackett's own test assertions
// (CardigannIndexerHtmlTests / CardigannIndexerJsonTests at commit b4140c7),
// transcribed verbatim, so a passing test means harbrr reproduces Jackett's
// normalized output on the same saved bytes — not that it matches a self-captured
// golden.
type oracleRelease struct {
	Title                string  `json:"title"`
	Details              string  `json:"details"`
	Link                 string  `json:"link"`
	Magnet               string  `json:"magnet"`
	InfoHash             string  `json:"infohash"`
	Size                 int64   `json:"size"`
	Categories           []int   `json:"categories"`
	Seeders              int64   `json:"seeders"`
	Peers                int64   `json:"peers"`
	Grabs                int64   `json:"grabs"`
	Files                int64   `json:"files"`
	PublishDate          string  `json:"publishDate"`
	DownloadVolumeFactor float64 `json:"downloadVolumeFactor"`
	UploadVolumeFactor   float64 `json:"uploadVolumeFactor"`
	MinimumSeedTime      int64   `json:"minimumSeedTime"`
	IMDBID               string  `json:"imdbid"`
	TMDBID               int64   `json:"tmdbid"`
	TVDBID               int64   `json:"tvdbid"`
	Poster               string  `json:"poster"`
}

// runOracle runs a ported-oracle case and unmarshals its canonical output.
func runOracle(t *testing.T, name string) []oracleRelease {
	t.Helper()
	dir := filepath.Join("testdata", name)
	c, err := Load(dir)
	if err != nil {
		t.Fatalf("loading %s: %v", name, err)
	}
	if c.GoldenSource != SourceJackettPort {
		t.Fatalf("%s golden_source = %q, want %q", name, c.GoldenSource, SourceJackettPort)
	}
	out, err := c.Run(dir)
	if err != nil {
		t.Fatalf("running %s: %v", name, err)
	}
	var releases []oracleRelease
	if err := json.Unmarshal(out, &releases); err != nil {
		t.Fatalf("unmarshaling %s output: %v", name, err)
	}
	return releases
}

// TestJackettOracleHTML ports CardigannIndexerHtmlTests.TestCardigannHtmlAsync.
// Every assertion below is Jackett's, transcribed. Gain (Jackett 23.4375) is
// omitted: it is derived from size and volume factors and is not a harbrr field.
func TestJackettOracleHTML(t *testing.T) {
	t.Parallel()

	releases := runOracle(t, "oracle-jackett-html")
	if len(releases) != 25 {
		t.Fatalf("releases = %d, want 25 (Jackett)", len(releases))
	}

	r := releases[0]
	assertInts(t, "categories", r.Categories, []int{8000}) // Category.Count==1, First==8000
	assertStr(t, "title", r.Title, "ubuntu-19.04-desktop-amd64.iso")
	assertStr(t, "details", r.Details, "https://www.testdefinition1.cc/torrent/d540fc48eb12f2833163eed6421d449dd8f1ce1f")
	assertStr(t, "link", r.Link, "http://itorrents.org/torrent/d540fc48eb12f2833163eed6421d449dd8f1ce1f.torrent")
	// Jackett asserts the MagnetUri prefix before "&tr".
	if want := "magnet:?xt=urn:btih:d540fc48eb12f2833163eed6421d449dd8f1ce1f&dn=ubuntu-19.04-desktop-amd64.iso"; !strings.HasPrefix(r.Magnet, want) {
		t.Errorf("magnet = %q, want prefix %q", r.Magnet, want)
	}
	assertStr(t, "infohash", r.InfoHash, "d540fc48eb12f2833163eed6421d449dd8f1ce1f")
	assertYear(t, "publishDate", r.PublishDate, "2024") // PublishDate.Year == 2024
	assertInt(t, "size", r.Size, 2097152000)
	assertInt(t, "seeders", r.Seeders, 12)
	assertInt(t, "peers", r.Peers, 13)
	assertFloat(t, "downloadVolumeFactor", r.DownloadVolumeFactor, 1)
	assertFloat(t, "uploadVolumeFactor", r.UploadVolumeFactor, 2)
}

// TestJackettOracleJSON ports CardigannIndexerJsonTests.TestCardigannJsonAsync.
// Every assertion below is Jackett's. Imdb 9115530 maps to harbrr's tt-prefixed
// imdbid; null Magnet/InfoHash/MinimumRatio/RageID/TVDBId map to harbrr's
// empty/zero; Gain is omitted (not a harbrr field).
func TestJackettOracleJSON(t *testing.T) {
	t.Parallel()

	releases := runOracle(t, "oracle-jackett-json")
	if len(releases) != 78 {
		t.Fatalf("releases = %d, want 78 (Jackett)", len(releases))
	}

	r := releases[0]
	// Category.Count==2, First==2000, Last==100001 (custom cat from the mapping desc).
	assertInts(t, "categories", r.Categories, []int{2000, 100001})
	assertStr(t, "title", r.Title, "The Eyes of Tammy Faye (2021)  BDRip 1080p AVC ES DD+ 5.1 EN DTSSS 5.1 Subs] HDO")
	assertStr(t, "details", r.Details, "https://jsondefinition1.com/torrents/24804")
	assertStr(t, "link", r.Link, "https://jsondefinition1.com/torrent/download/24804.01c887e14d0845f195bc12b31ea27d38")
	assertStr(t, "magnet", r.Magnet, "")     // Jackett MagnetUri == null
	assertStr(t, "infohash", r.InfoHash, "") // Jackett InfoHash == null
	assertStr(t, "poster", r.Poster, "https://image.tmdb.org/t/p/w92/iBjkm6oxTPrvNkzr63cmnrpsQPR.jpg")
	assertYear(t, "publishDate", r.PublishDate, "2021") // PublishDate.Year == 2021
	assertInt(t, "size", r.Size, 17964744704)           // float32 GetBytes precision, as Jackett
	assertInt(t, "seeders", r.Seeders, 27)
	assertInt(t, "peers", r.Peers, 30)
	assertInt(t, "files", r.Files, 1)
	assertInt(t, "grabs", r.Grabs, 29)
	assertFloat(t, "downloadVolumeFactor", r.DownloadVolumeFactor, 1)
	assertFloat(t, "uploadVolumeFactor", r.UploadVolumeFactor, 1)
	assertInt(t, "minimumSeedTime", r.MinimumSeedTime, 345600)
	assertStr(t, "imdbid", r.IMDBID, "tt9115530") // Jackett Imdb == 9115530
	assertInt(t, "tmdbid", r.TMDBID, 601470)      // Jackett TMDb == 601470
	assertInt(t, "tvdbid", r.TVDBID, 0)           // Jackett TVDBId == 0
}

func assertStr(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func assertInt(t *testing.T, field string, got, want int64) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d, want %d", field, got, want)
	}
}

func assertFloat(t *testing.T, field string, got, want float64) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

func assertYear(t *testing.T, field, got, wantYear string) {
	t.Helper()
	if !strings.HasPrefix(got, wantYear) {
		t.Errorf("%s = %q, want year %s", field, got, wantYear)
	}
}

func assertInts(t *testing.T, field string, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", field, got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s = %v, want %v", field, got, want)
			return
		}
	}
}
