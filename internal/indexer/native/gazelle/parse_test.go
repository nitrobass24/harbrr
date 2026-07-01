package gazelle

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// credAPIKey is a synthetic test secret (the configured API key). It exists only to
// prove the scrubber/redaction paths and lives only in this test file.
const credAPIKey = "SYNTHETICAPIKEY"

// fixedClock is the reference time used for the fuzzy "now" path so the golden is stable.
var fixedClock = time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

// parseDriver builds a full driver (caps + cfg + clock) for one site. parse needs the
// caps (the description-keyed category map), the cfg (apikey for the scrubber), and a
// fixed clock (the fuzzy "now" publish date).
func parseDriver(t *testing.T, id string, cfg map[string]string) *driver {
	t.Helper()
	def := familyByID(t, id).Definition
	d, err := New(native.Params{Def: def, Cfg: cfg, Clock: func() time.Time { return fixedClock }})
	if err != nil {
		t.Fatalf("New(%q): %v", id, err)
	}
	return d.(*driver)
}

// TestParseBrowseMusicGolden flattens a MUSIC group (one release per torrent) and pins
// the full mapping: the exact Gazelle title composition (ReleaseType + Remaster +
// Format/Encoding/Media/Log/Cue flags), the header-less download link, the music fields
// (Artist/Album=GroupName/Year/Genre=joined tags), peers/grabs/size, the torrent
// datetime publish date (UTC), the Audio category from "Music", and DownloadVolumeFactor.
// Releases are sorted by PublishDate descending (mirroring Prowlarr), so the 2021 remaster
// (id 29991964) sorts before the two 2012 torrents.
func TestParseBrowseMusicGolden(t *testing.T) {
	t.Parallel()
	body := readFixture(t, "testdata/browse_music.json")
	d := parseDriver(t, "redacted", map[string]string{"apikey": credAPIKey})
	got, err := d.parseBrowse(body)
	if err != nil {
		t.Fatalf("parseBrowse: %v", err)
	}

	want := []*normalizer.Release{
		{
			Title:      "Logistics - Fear Not (2012) [Album] [Remaster 2021] [FLAC 24bit Lossless / Vinyl]",
			Link:       "https://redacted.sh/ajax.php?action=download&id=29991964",
			Artist:     "Logistics",
			Album:      "Fear Not",
			Year:       2012,
			Genre:      "drum.and.bass, electronic",
			Categories: []int{3000},
			Size:       943718400,
			Grabs:      3,
			Seeders:    4, Leechers: 0, Peers: 4,
			PublishDate:          "2021-06-01T08:30:00Z",
			DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
		},
		{
			Title:      "Logistics - Fear Not (2012) [Album] [MP3 320 / WEB]",
			Link:       "https://redacted.sh/ajax.php?action=download&id=29991963",
			Artist:     "Logistics",
			Album:      "Fear Not",
			Year:       2012,
			Genre:      "drum.and.bass, electronic",
			Categories: []int{3000},
			Size:       104857600,
			Grabs:      12,
			Seeders:    7, Leechers: 1, Peers: 8,
			PublishDate:          "2012-04-15T10:00:00Z",
			DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
		},
		{
			Title:      "Logistics - Fear Not (2012) [Album] [FLAC Lossless / CD / Log (100%) / Cue]",
			Link:       "https://redacted.sh/ajax.php?action=download&id=29991962",
			Artist:     "Logistics",
			Album:      "Fear Not",
			Year:       2012,
			Genre:      "drum.and.bass, electronic",
			Categories: []int{3000},
			Size:       527749302,
			Grabs:      55,
			Seeders:    20, Leechers: 0, Peers: 20,
			PublishDate:          "2012-04-14T15:57:00Z",
			DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
		},
	}
	assertReleases(t, got, want)
}

// TestParseBrowseSortsByPublishDateDesc is a focused regression for the ordering finding:
// the flattened feed must be PublishDate-descending (newest first) regardless of torrent
// id, matching Prowlarr's terminal OrderByDescending(PublishDate). The music fixture has
// ids ascending with time, so an id-ascending sort would invert this ordering.
func TestParseBrowseSortsByPublishDateDesc(t *testing.T) {
	t.Parallel()
	body := readFixture(t, "testdata/browse_music.json")
	d := parseDriver(t, "redacted", map[string]string{"apikey": credAPIKey})
	got, err := d.parseBrowse(body)
	if err != nil {
		t.Fatalf("parseBrowse: %v", err)
	}
	wantOrder := []string{
		"2021-06-01T08:30:00Z",
		"2012-04-15T10:00:00Z",
		"2012-04-14T15:57:00Z",
	}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d releases, want %d", len(got), len(wantOrder))
	}
	for i, want := range wantOrder {
		if got[i].PublishDate != want {
			t.Errorf("release[%d].PublishDate = %q, want %q", i, got[i].PublishDate, want)
		}
	}
}

// TestComposeTitleSkipsWhitespaceFields proves a whitespace-only ReleaseType or
// RemasterTitle emits no empty "[ ]" bracket, matching Prowlarr's IsNotNullOrWhiteSpace.
func TestComposeTitleSkipsWhitespaceFields(t *testing.T) {
	t.Parallel()
	g := &group{Artist: "Artist", GroupName: "Album", GroupYear: flexInt(2024), ReleaseType: "   "}
	tr := &torrent{Format: "FLAC", Encoding: "Lossless", Media: "CD", RemasterTitle: "  "}
	got := composeTitle(g, tr)
	want := "Artist - Album (2024) [FLAC Lossless / CD]"
	if got != want {
		t.Errorf("composeTitle = %q, want %q", got, want)
	}
}

// TestParseBrowseNonMusic proves a NON-MUSIC group (torrents == null) is one release
// (Title=GroupName) built from group-level fields, the Audiobooks->Audio/Audiobook
// category, the unix groupTime publish date, the "now" fuzzy fallback against the fixed
// clock, and the null-category default to Audio.
func TestParseBrowseNonMusic(t *testing.T) {
	t.Parallel()
	body := readFixture(t, "testdata/browse_nonmusic.json")
	got, err := parseDriver(t, "orpheus", nil).parseBrowse(body)
	if err != nil {
		t.Fatalf("parseBrowse: %v", err)
	}

	// Releases are sorted by PublishDate descending, so the 2024 "Fresh Upload" sorts
	// before the 2019 audiobook.
	want := []*normalizer.Release{
		{
			Title:      "Fresh Upload",
			Link:       "https://orpheus.network/ajax.php?action=download&id=30000002",
			Year:       2024,
			Categories: []int{3000},
			Size:       104857600,
			Grabs:      0,
			Seeders:    1, Leechers: 0, Peers: 1,
			PublishDate:          "2024-03-01T12:00:00Z",
			DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
		},
		{
			Title:      "Some Audiobook Title",
			Link:       "https://orpheus.network/ajax.php?action=download&id=30000001",
			Year:       2019,
			Categories: []int{3030},
			Size:       734003200,
			Grabs:      8,
			Seeders:    15, Leechers: 2, Peers: 17,
			PublishDate:          "2019-05-02T09:30:30Z",
			DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
		},
	}
	assertReleases(t, got, want)
}

// TestParseBrowseFreeleechRED pins the RED freeleech semantics on the four freeleech
// torrents: every variant (regular freeleech / neutral-leech / personal-freeleech /
// freeload) is treated as freeleech so DownloadVolumeFactor=0 AND the download link
// carries NO usetoken (canUseToken && !isFreeLeech == false — a token must not be wasted
// on already-free content). RED counts IsFreeload as freeleech and zeroes the upload
// factor for neutral-leech/freeload.
func TestParseBrowseFreeleechRED(t *testing.T) {
	t.Parallel()
	body := readFixture(t, "testdata/browse_freeleech.json")
	d := parseDriver(t, "redacted", map[string]string{"apikey": credAPIKey, "use_freeleech_token": "true"})
	got, err := d.parseBrowse(body)
	if err != nil {
		t.Fatalf("parseBrowse: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d releases, want 4", len(got))
	}
	for _, r := range got {
		if r.DownloadVolumeFactor != 0 {
			t.Errorf("%s: DownloadVolumeFactor = %v, want 0 (freeleech)", r.Link, r.DownloadVolumeFactor)
		}
		if strings.Contains(r.Link, "usetoken") {
			t.Errorf("%s: link must not carry usetoken on already-free content", r.Link)
		}
	}
	// Upload factor: neutral-leech (30000051) and freeload (30000053) are 0 on RED;
	// regular freeleech (30000050) and personal-freeleech (30000052) keep 1.
	wantUpload := map[string]float64{
		"id=30000050": 1, "id=30000051": 0, "id=30000052": 1, "id=30000053": 0,
	}
	for _, r := range got {
		for frag, want := range wantUpload {
			if strings.Contains(r.Link, frag) && r.UploadVolumeFactor != want {
				t.Errorf("%s: UploadVolumeFactor = %v, want %v", frag, r.UploadVolumeFactor, want)
			}
		}
	}
}

// TestParseBrowseFreeloadOPS proves OPS does NOT count IsFreeload as freeleech: the
// freeload-only torrent (30000053) is paid (DownloadVolumeFactor=1, UploadVolumeFactor=1)
// on Orpheus, while the genuine freeleech/neutral/personal flags still apply.
func TestParseBrowseFreeloadOPS(t *testing.T) {
	t.Parallel()
	body := readFixture(t, "testdata/browse_freeleech.json")
	got, err := parseDriver(t, "orpheus", nil).parseBrowse(body)
	if err != nil {
		t.Fatalf("parseBrowse: %v", err)
	}
	for _, r := range got {
		if strings.Contains(r.Link, "id=30000053") {
			if r.DownloadVolumeFactor != 1 || r.UploadVolumeFactor != 1 {
				t.Errorf("OPS freeload should be paid: download=%v upload=%v", r.DownloadVolumeFactor, r.UploadVolumeFactor)
			}
		}
	}
}

// TestDownloadLinkUsetoken proves usetoken=1 is appended only when the torrent can use a
// token AND is not already freeleech, and is omitted entirely otherwise (the OPS quirk:
// never usetoken=0).
func TestDownloadLinkUsetoken(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, "redacted", nil)
	if got := d.downloadLink(12345, true); got != "https://redacted.sh/ajax.php?action=download&id=12345&usetoken=1" {
		t.Errorf("with token: %q", got)
	}
	if got := d.downloadLink(12345, false); got != "https://redacted.sh/ajax.php?action=download&id=12345" {
		t.Errorf("without token: %q", got)
	}
}

// TestParseBrowseFailure proves a status:"failure" body with a non-auth error is a parse
// error, while an auth-flavoured error maps to login.ErrLoginFailed.
func TestParseBrowseFailure(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, "redacted", map[string]string{"apikey": credAPIKey})

	body := readFixture(t, "testdata/failure.json")
	if _, err := d.parseBrowse(body); !errors.Is(err, search.ErrParseError) {
		t.Errorf("non-auth failure: err = %v, want search.ErrParseError", err)
	}

	auth := []byte(`{"status":"failure","error":"bad credentials"}`)
	if _, err := d.parseBrowse(auth); !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("auth failure: err = %v, want login.ErrLoginFailed", err)
	}
}

// TestParseBrowseMalformed proves a non-JSON body is a parse error whose message now
// carries an actionable decode diagnostic (bytes count for a non-JSON body) while still
// wrapping search.ErrParseError.
func TestParseBrowseMalformed(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, "redacted", nil)
	_, err := d.parseBrowse([]byte("<html>not json</html>"))
	if !errors.Is(err, search.ErrParseError) {
		t.Errorf("err = %v, want search.ErrParseError", err)
	}
	if !strings.Contains(err.Error(), "bytes") {
		t.Errorf("err = %q, want actionable token %q for non-JSON body", err, "bytes")
	}
}

// TestParseBrowseSuccessEmpty proves a success body with a nil/empty response yields zero
// releases and no error.
func TestParseBrowseSuccessEmpty(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, "redacted", nil)
	got, err := d.parseBrowse([]byte(`{"status":"success"}`))
	if err != nil {
		t.Fatalf("parseBrowse: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d releases, want 0", len(got))
	}
}

// TestParseErrorScrubsAPIKey proves an error message echoing the configured apikey cannot
// leak it (scrubAPIKey replaces it before the error surfaces).
func TestParseErrorScrubsAPIKey(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, "redacted", map[string]string{"apikey": credAPIKey})
	body := []byte(`{"status":"failure","error":"rejected key ` + credAPIKey + ` boom"}`)
	_, err := d.parseBrowse(body)
	if err == nil {
		t.Fatal("want a parse error")
	}
	if strings.Contains(err.Error(), credAPIKey) {
		t.Errorf("error leaks the apikey: %v", err)
	}
}

// TestFlexIntDecode proves flexInt accepts a JSON string and a bare number and degrades
// blank/garbage/null to 0.
func TestFlexIntDecode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int64
	}{
		{`"527749302"`, 527749302},
		{`42`, 42},
		{`""`, 0},
		{`null`, 0},
		{`"notanumber"`, 0},
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

// TestCategoriesFallback proves the description-keyed mapping (Music->Audio,
// Audiobooks->Audio/Audiobook), the null/"Select Category" default to Audio, and that an
// unmapped description also defaults to Audio — each release carrying exactly one
// canonical newznab category (the synthetic custom id is discarded).
func TestCategoriesFallback(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, "redacted", nil)
	sel := "Select Category..."
	music := "Music"
	audiobooks := "Audiobooks"
	unknown := "Totally Unknown"
	cases := []struct {
		cat  *string
		want []int
	}{
		{&music, []int{3000}},
		{&audiobooks, []int{3030}},
		{&sel, []int{3000}},
		{nil, []int{3000}},
		{&unknown, []int{3000}},
	}
	for _, c := range cases {
		if got := d.categories(c.cat); !reflect.DeepEqual(got, c.want) {
			t.Errorf("categories(%v) = %v, want %v", c.cat, got, c.want)
		}
	}
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %q: %v", path, err)
	}
	return b
}

func assertReleases(t *testing.T, got, want []*normalizer.Release) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("parsed %d releases, want %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("release[%d] =\n  %+v\nwant\n  %+v", i, got[i], want[i])
		}
	}
}
