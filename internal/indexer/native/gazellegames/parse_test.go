package gazellegames

import (
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// credAPIKey is a synthetic test secret (the configured API key). It exists only to prove
// the scrubber/redaction paths and lives only in this test file.
const credAPIKey = "SYNTHETICAPIKEY"

// credPasskey is a synthetic download passkey. It exists only to pin the rebuilt
// download URL and lives only in this test file.
const credPasskey = "SYNTHETICPASSKEY"

// fixedClock is the reference time used so any fuzzy publish date is stable.
var fixedClock = time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

// parseDriver builds a full driver (caps + cfg + clock). parse needs the caps (the
// description-keyed category map), the cfg (apikey for the scrubber, passkey for the
// rebuilt download URL), and a fixed clock.
func parseDriver(t *testing.T, cfg map[string]string) *driver {
	t.Helper()
	def := Families()[0].Definition
	d, err := New(native.Params{Def: def, Cfg: cfg, Clock: func() time.Time { return fixedClock }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %q: %v", path, err)
	}
	return b
}

// TestParseSearchGolden flattens a multi-group search body and pins the full mapping:
// the exact Prowlarr title composition (Year append gated by an existing year, the
// Remaster bracket, the format/encoding/artist/language/region/misc/Trumpable flags, the
// GameDox bracket), the rebuilt torrents.php download/info URLs, size/peers/grabs/files,
// the UTC publish date, the CategoryId fallback categories, the freeleech/neutral/low-seed
// volume factors, and the fixed minimum seed time. Non-TORRENT rows and empty groups emit
// nothing, and releases are sorted by PublishDate descending.
func TestParseSearchGolden(t *testing.T) {
	t.Parallel()
	body := readFixture(t, "testdata/search.json")
	d := parseDriver(t, map[string]string{"apikey": credAPIKey, "passkey": credPasskey})

	got, err := d.parseSearch(body)
	if err != nil {
		t.Fatalf("parseSearch: %v", err)
	}

	want := []*normalizer.Release{
		{
			Title:                "Cool Game (2018) [Director's Cut 2020] [Rip FitGirl / Some Studio / DLC / Trumpable] [Update]",
			Link:                 "https://gazellegames.net/torrents.php?action=download&authkey=prowlarr&id=70002&torrent_pass=SYNTHETICPASSKEY",
			Details:              "https://gazellegames.net/torrents.php?id=1001&torrentid=70002",
			Categories:           []int{4010},
			Size:                 2147483648,
			Files:                1,
			Grabs:                0,
			Seeders:              5,
			Leechers:             1,
			Peers:                6,
			PublishDate:          "2020-01-02T00:00:00Z",
			DownloadVolumeFactor: 0,
			UploadVolumeFactor:   0,
			MinimumSeedTime:      minimumSeedTimeSeconds,
		},
		{
			Title:                "Cool Game (2018) [ISO Clone / Some Studio / English / Region Free]",
			Link:                 "https://gazellegames.net/torrents.php?action=download&authkey=prowlarr&id=70001&torrent_pass=SYNTHETICPASSKEY",
			Details:              "https://gazellegames.net/torrents.php?id=1001&torrentid=70001",
			Categories:           []int{4050},
			Size:                 1073741824,
			Files:                12,
			Grabs:                42,
			Seeders:              10,
			Leechers:             3,
			Peers:                13,
			PublishDate:          "2018-05-04T10:11:12Z",
			DownloadVolumeFactor: 0,
			UploadVolumeFactor:   1,
			MinimumSeedTime:      minimumSeedTimeSeconds,
		},
		{
			Title:                "A Manual 2016 [EPUB Retail / Book House]",
			Link:                 "https://gazellegames.net/torrents.php?action=download&authkey=prowlarr&id=80001&torrent_pass=SYNTHETICPASSKEY",
			Details:              "https://gazellegames.net/torrents.php?id=1003&torrentid=80001",
			Categories:           []int{7020},
			Size:                 1048576,
			Files:                1,
			Grabs:                7,
			Seeders:              2,
			Leechers:             0,
			Peers:                2,
			PublishDate:          "2016-06-06T06:06:06Z",
			DownloadVolumeFactor: 0,
			UploadVolumeFactor:   1,
			MinimumSeedTime:      minimumSeedTimeSeconds,
		},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d releases, want %d:\n%#v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("release[%d]:\n got = %#v\nwant = %#v", i, got[i], want[i])
		}
	}
}

// TestParseSearchEmpty proves a success body whose response is not a group object ([])
// yields zero releases and no error (Prowlarr's "Response is not JObject" guard).
func TestParseSearchEmpty(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, map[string]string{"apikey": credAPIKey, "passkey": credPasskey})
	got, err := d.parseSearch(readFixture(t, "testdata/empty.json"))
	if err != nil {
		t.Fatalf("parseSearch: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d releases, want 0", len(got))
	}
}

// TestParseSearchErrors proves a non-success body maps to the right sentinel: a numeric
// 401 status is a login failure; a generic "failure" status is a parse error.
func TestParseSearchErrors(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, map[string]string{"apikey": credAPIKey, "passkey": credPasskey})

	cases := []struct {
		name    string
		fixture string
		want    error
	}{
		{"unauthorized", "testdata/unauthorized.json", login.ErrLoginFailed},
		{"failure", "testdata/failure.json", search.ErrParseError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := d.parseSearch(readFixture(t, c.fixture))
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

// TestParseSearchMalformed proves a non-JSON body is a parse error.
func TestParseSearchMalformed(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, map[string]string{"apikey": credAPIKey})
	_, err := d.parseSearch([]byte("not json"))
	if !errors.Is(err, search.ErrParseError) {
		t.Fatalf("err = %v, want %v", err, search.ErrParseError)
	}
}

// TestScrubAPIKey proves the configured apikey is redacted out of any surfaced message so
// a server echo cannot leak it.
func TestScrubAPIKey(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, map[string]string{"apikey": credAPIKey})
	if got := d.scrubAPIKey("token " + credAPIKey + " seen"); got != "token [redacted] seen" {
		t.Fatalf("scrubAPIKey = %q, did not redact the key", got)
	}
}
