package broadcastthenet

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

// credAPIKey is a synthetic test secret (the configured API key). It exists only to
// prove the scrubber/redaction paths and lives only in this test file.
const credAPIKey = "SYNTHETICAPIKEY"

// parseDriver is a full driver (caps + cfg) for the parser tests: parse needs the caps
// (the Resolution-keyed category map) and the cfg (apikey for the scrubber).
func parseDriver(t *testing.T, cfg map[string]string) *driver {
	t.Helper()
	def := Families()[0].Definition
	d, err := New(native.Params{Def: def, Cfg: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

// TestParseReleasesGolden parses the synthetic getTorrents response and asserts the full
// btnTorrent->Release mapping: title=ReleaseName, link=DownloadURL, infohash, the
// Resolution-keyed category (1080p->5040, 2160p->5045, SD->5030), peers=seeders+leechers,
// grabs=snatched, size, tvdbid/rageid, and the unix Time->UTC publish date. It also pins
// the deterministic sort by TorrentID (the fixture is keyed in non-sorted order
// 1555073,1555200,1555000 -> expected 1555000,1555073,1555200). The goldens are derived
// from Prowlarr's parse contract + autobrr's pkg/btn shape, not a live capture.
func TestParseReleasesGolden(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/getTorrents_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	d := parseDriver(t, map[string]string{"apikey": credAPIKey})
	got, err := d.parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}

	want := []*normalizer.Release{
		{
			Title:      "Old.Show.S01E01.SDTV.XviD-GRP",
			Link:       "https://broadcasthe.net/torrents.php?action=download&id=1555000&authkey=SYNTHETICKEY2&torrent_pass=SYNTHETICPASS2",
			InfoHash:   "AAAA1111BBBB2222CCCC3333DDDD4444EEEE5555",
			Categories: []int{5030},
			Size:       367001600, Grabs: 100,
			Seeders: 10, Leechers: 2, Peers: 12,
			PublishDate: "2020-01-01T00:00:00Z",
			TVDBID:      100001, RageID: 55555,
			DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
		},
		{
			Title:      "That.Show.S05E04.1080p.WEB-DL.H.264-NOGRP",
			Link:       "https://broadcasthe.net/torrents.php?action=download&id=1555073&authkey=SYNTHETICKEY1&torrent_pass=SYNTHETICPASS1",
			InfoHash:   "56CD94119F6BF7FC294A92D7A4099C3D1815C907",
			Categories: []int{5040},
			Size:       3288852849, Grabs: 4,
			Seeders: 5, Leechers: 41, Peers: 46,
			PublishDate:          "2022-01-02T20:04:46Z",
			TVDBID:               332747,
			DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
		},
		{
			Title:      "New.Show.S02.2160p.BluRay.x265-GRP",
			Link:       "https://broadcasthe.net/torrents.php?action=download&id=1555200&authkey=SYNTHETICKEY3&torrent_pass=SYNTHETICPASS3",
			InfoHash:   "FFFF6666AAAA7777BBBB8888CCCC9999DDDD0000",
			Categories: []int{5045},
			Size:       54975581388, Grabs: 7,
			Seeders: 20, Leechers: 3, Peers: 23,
			PublishDate:          "2021-01-01T00:00:00Z",
			TVDBID:               200002,
			DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
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

// TestParseReleasesEmpty proves a results=0 / empty torrents map yields zero releases
// (not an error).
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

// TestParseReleasesEmptyArray proves a results=0 body whose torrents field is a JSON
// ARRAY (`[]`, how PHP serializes an empty associative array) yields zero releases and no
// error — a straight map decode would otherwise fail and surface ErrParseError.
func TestParseReleasesEmptyArray(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/empty_array.json")
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

// TestParseReleasesStableSortOnTies proves the sort is TOTAL: two torrents with
// non-numeric ids (both parse to 0) are ordered deterministically by their raw map-key
// string, so the feed is stable across runs despite Go's randomized map iteration.
func TestParseReleasesStableSortOnTies(t *testing.T) {
	t.Parallel()
	body := []byte(`{"result":{"results":"2","torrents":{` +
		`"zzz":{"TorrentID":"zzz","ReleaseName":"Zee"},` +
		`"aaa":{"TorrentID":"aaa","ReleaseName":"Aye"}}}}`)
	d := parseDriver(t, nil)
	for i := 0; i < 50; i++ {
		got, err := d.parseReleases(body)
		if err != nil {
			t.Fatalf("parseReleases: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d releases, want 2", len(got))
		}
		// Both ids parse to 0, so the map-key tie-breaker decides: "aaa" < "zzz".
		if got[0].Title != "Aye" || got[1].Title != "Zee" {
			t.Fatalf("order = [%q,%q], want [Aye,Zee] (stable map-key tie-break)", got[0].Title, got[1].Title)
		}
	}
}

// TestParseBadKeyMapsToLoginFailed proves the -32001 ("Invalid API Key") error envelope
// (and a null result) is surfaced as login.ErrLoginFailed.
func TestParseBadKeyMapsToLoginFailed(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/bad_key.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	_, err = parseDriver(t, map[string]string{"apikey": credAPIKey}).parseReleases(body)
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("err = %v, want login.ErrLoginFailed", err)
	}
}

// TestParseReleasesErrors proves a malformed body, a non-auth JSON-RPC error, and a null
// result are each a parse error.
func TestParseReleasesErrors(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, map[string]string{"apikey": credAPIKey})
	cases := []struct{ name, body string }{
		{"malformed json", `{`},
		{"non-auth error", `{"result":null,"error":{"code":-32000,"message":"boom"}}`},
		{"null result no error", `{"result":null}`},
	}
	for _, tc := range cases {
		if _, err := d.parseReleases([]byte(tc.body)); !errors.Is(err, search.ErrParseError) {
			t.Errorf("%s: err = %v, want search.ErrParseError", tc.name, err)
		}
	}
}

// TestParseErrorScrubsAPIKey proves an error message that echoes the configured API key
// cannot leak it (scrubAPIKey replaces it before the error surfaces).
func TestParseErrorScrubsAPIKey(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, map[string]string{"apikey": credAPIKey})
	body := `{"result":null,"error":{"code":-32000,"message":"rejected key ` + credAPIKey + ` here"}}`
	_, err := d.parseReleases([]byte(body))
	if err == nil {
		t.Fatal("want a parse error")
	}
	if strings.Contains(err.Error(), credAPIKey) {
		t.Errorf("error leaks the apikey: %v", err)
	}
}

// TestCategoriesFallback proves an unmapped/blank Resolution falls back to the TV root
// (5000), and that the synthetic 1:1 custom category (id >= 100000) is discarded so each
// release carries exactly one canonical newznab category.
func TestCategoriesFallback(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, nil)
	cases := []struct {
		res  string
		want []int
	}{
		{"1080p", []int{5040}},
		{"2160p", []int{5045}},
		{"SD", []int{5030}},
		{"Portable Device", []int{5030}},
		{"", []int{5000}},
		{"4320p", []int{5000}}, // unmapped resolution -> TV root
	}
	for _, c := range cases {
		if got := d.categories(c.res); !reflect.DeepEqual(got, c.want) {
			t.Errorf("categories(%q) = %v, want %v", c.res, got, c.want)
		}
	}
}

// TestFlexStringDecode proves flexString accepts both a JSON string and a bare JSON
// number, and that int64() parses tolerantly (blank/garbage -> 0).
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
