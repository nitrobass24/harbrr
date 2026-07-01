package torrentday

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

const base = "https://www.torrentday.com/"

// parseDriver builds a full driver (caps + cfg) for the parser tests: parse needs the
// caps (the `c`->newznab category map) and the cfg (the freeleech_only toggle). The
// session Cookie is never needed by parse — it rides the request header at the HTTP
// layer — so cfg here carries no secret.
func parseDriver(t *testing.T, cfg map[string]string) *driver {
	t.Helper()
	def := Families()[0].Definition
	d, err := New(native.Params{Def: def, Cfg: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

// TestParseReleasesGolden parses the synthetic /t.json response and asserts the full
// row->Release mapping: title=name verbatim, the download.php/<id>/<id>.torrent link and
// details.php?id=<id> page, the `c`->newznab category (96->2045 Movies/UHD, 7->5040
// TV/HD), peers=seeders+leechers, size/files/grabs, the unix ctime->UTC publish date, the
// download-multiplier-derived DownloadVolumeFactor (0=freeleech, default 1), and the
// canonical imdb id. Row 2's numerics are all JSON strings, proving the tolerant flexInt
// decode. The goldens are derived from Prowlarr's TorrentDayParser contract, not a live
// capture.
//
// Note: the Phase-1 contract describes row 1's imdb as the numeric "1234567"; harbrr
// stores it as the canonical "tt"+7-digit string ("tt1234567"), matching filelist/
// iptorrents — the same numeric, harbrr's string representation.
func TestParseReleasesGolden(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search_results.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := parseDriver(t, nil).parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}

	// Releases are sorted by download link; the ids already order this way.
	want := []*normalizer.Release{
		{
			Title:                "Some Movie 2024 2160p UHD BluRay x265-GROUP",
			Link:                 base + "download.php/2743197/2743197.torrent",
			Details:              base + "details.php?id=2743197",
			Categories:           []int{2045},
			Size:                 48318382080,
			Files:                3,
			Grabs:                128,
			Seeders:              42,
			Leechers:             5,
			Peers:                47,
			PublishDate:          "2024-06-20T00:00:00Z",
			DownloadVolumeFactor: 0,
			UploadVolumeFactor:   1,
			MinimumRatio:         1,
			MinimumSeedTime:      259200,
			IMDBID:               "tt1234567",
		},
		{
			Title:                "Some Show S03E04 1080p WEB-DL DDP5.1 H264-CREW",
			Link:                 base + "download.php/2743210/2743210.torrent",
			Details:              base + "details.php?id=2743210",
			Categories:           []int{5040},
			Size:                 2147483648,
			Files:                1,
			Grabs:                7,
			Seeders:              10,
			Leechers:             2,
			Peers:                12,
			PublishDate:          "2024-06-20T16:13:20Z",
			DownloadVolumeFactor: 1,
			UploadVolumeFactor:   1,
			MinimumRatio:         1,
			MinimumSeedTime:      259200,
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

// TestParseReleasesEmpty proves the literal `[]` empty-result body yields zero releases
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

// TestParseReleasesNotArray proves a body that is not a JSON array — an HTML login page
// from a redirect, a truncated response — is a parse error (TorrentDay always returns an
// array; anything else is malformed, never a silently-empty page).
func TestParseReleasesNotArray(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, nil)
	cases := []struct{ name, body string }{
		{"login html", "<!doctype html><html><body>login</body></html>"},
		{"object envelope", `{"error":"nope"}`},
		{"empty body", ""},
	}
	for _, tc := range cases {
		if _, err := d.parseReleases([]byte(tc.body)); !errorsIsParse(err) {
			t.Errorf("%s: err = %v, want search.ErrParseError", tc.name, err)
		}
	}
}

// TestParseReleasesMalformedArray proves a body that starts as a JSON array but is
// truncated mid-decode is a parse error.
func TestParseReleasesMalformedArray(t *testing.T) {
	t.Parallel()
	_, err := parseDriver(t, nil).parseReleases([]byte(`[{"t":1,`))
	if !errorsIsParse(err) {
		t.Fatalf("err = %v, want search.ErrParseError", err)
	}
	// The discarded decode error is now surfaced as an actionable, redacted
	// diagnostic: a truncated JSON array reports an offset ("invalid JSON at
	// offset N"). The sentinel wrap (errorsIsParse above) must still hold.
	if msg := err.Error(); !strings.Contains(msg, "offset") && !strings.Contains(msg, "invalid") {
		t.Errorf("err = %q, want an actionable token (\"offset\" or \"invalid\")", msg)
	}
}

// TestParseFreeleechOnlyFilter proves the freeleech_only toggle drops rows whose
// download-multiplier is non-zero, keeping only the freeleech (multiplier 0) row.
func TestParseFreeleechOnlyFilter(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search_results.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := parseDriver(t, map[string]string{"freeleech_only": "true"}).parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d releases, want 1 (only the freeleech row)", len(got))
	}
	if got[0].DownloadVolumeFactor != 0 {
		t.Errorf("kept row DownloadVolumeFactor = %v, want 0 (freeleech)", got[0].DownloadVolumeFactor)
	}
}

// TestCategories proves the `c` id resolves to a single canonical newznab category (the
// synthetic >= customCatCutoff custom id is discarded) and an unmapped id yields no
// category.
func TestCategories(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, nil)
	cases := []struct {
		c    flexInt
		want []int
	}{
		{"96", []int{2045}}, // Movies/UHD
		{"7", []int{5040}},  // TV/HD
		{"34", []int{5040}}, // TV/x265 routed to TV/HD
		{"99999", nil},      // unmapped tracker id -> no category
		{"", nil},
	}
	for _, c := range cases {
		if got := d.categories(c.c); !reflect.DeepEqual(got, c.want) {
			t.Errorf("categories(%q) = %v, want %v", c.c, got, c.want)
		}
	}
}

// TestFlexIntDecode proves flexInt accepts a JSON string and a bare JSON number, and that
// int64()/string() parse tolerantly (blank/garbage -> 0/"").
func TestFlexIntDecode(t *testing.T) {
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
		var f flexInt
		if err := f.UnmarshalJSON([]byte(c.in)); err != nil {
			t.Errorf("UnmarshalJSON(%s): %v", c.in, err)
			continue
		}
		if got := f.int64(); got != c.want {
			t.Errorf("flexInt(%s).int64() = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestFlexFloatDefault proves the download-multiplier defaults to the supplied default
// when absent/blank/unparseable, and otherwise parses the number (string or bare).
func TestFlexFloatDefault(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want float64
	}{
		{`"0"`, 0},
		{`0`, 0},
		{`"1.5"`, 1.5},
		{`""`, 1},     // blank -> default
		{`null`, 1},   // absent -> default
		{`"junk"`, 1}, // unparseable -> default
	}
	for _, c := range cases {
		var f flexFloat
		if err := f.UnmarshalJSON([]byte(c.in)); err != nil {
			t.Errorf("UnmarshalJSON(%s): %v", c.in, err)
			continue
		}
		if got := f.float64WithDefault(1); got != c.want {
			t.Errorf("flexFloat(%s).float64WithDefault(1) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestNormalizeIMDBID proves the imdb id normalizes to the canonical "tt"+7-digit form
// and rejects short/non-numeric values.
func TestNormalizeIMDBID(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"tt1234567", "tt1234567"},
		{"1234567", "tt1234567"},
		{"133093", "tt0133093"},
		{"", ""},
		{"tt", ""},
		{"notnum", ""},
	}
	for _, c := range cases {
		if got := normalizeIMDBID(c.in); got != c.want {
			t.Errorf("normalizeIMDBID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// errorsIsParse reports whether err wraps search.ErrParseError.
func errorsIsParse(err error) bool {
	return errors.Is(err, search.ErrParseError)
}
