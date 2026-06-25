package hdbits

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// Synthetic credentials — they exist only to prove the download URL carries the passkey and
// that error messages scrub both secrets. They live only in this _test.go file.
const (
	credUser = "the-user"
	credPass = "PASSKEY-SECRET-9f8e"
)

// parseDriver builds a full driver (caps + cfg) for the parser tests with a fixed base URL,
// so the rebuilt download.php/details.php URLs are stable.
func parseDriver(t *testing.T, cfg map[string]string) *driver {
	t.Helper()
	def := Families()[0].Definition
	d, err := New(native.Params{Def: def, Cfg: cfg})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	drv := d.(*driver)
	drv.baseURL = "https://hdbits.test/"
	return drv
}

func creds() map[string]string {
	return map[string]string{"username": credUser, "passkey": credPass}
}

// TestParseReleasesGolden parses the synthetic multi-result fixture and asserts the full
// row->Release mapping: the filename/name title rules, the single newznab category, size/
// seeders/peers/grabs/files, the ISO `added`->UTC publish date, imdb/tvdb ids, and the
// freeleech/XXX/half-leech volume factors. The goldens are derived from Prowlarr's HDBits
// parse contract, not a live capture.
func TestParseReleasesGolden(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	d := parseDriver(t, creds())
	got, err := d.parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}

	dl := func(id string) string {
		return "https://hdbits.test/download.php?id=" + id + "&passkey=" + credPass
	}
	details := func(id string) string { return "https://hdbits.test/details.php?id=" + id }

	want := []*normalizer.Release{
		{
			Title:                "The Matrix 1999 1080p BluRay REMUX", // full disc (medium 1) -> name
			InfoHash:             "ABC123DEF456",
			Link:                 dl("100001"),
			Details:              details("100001"),
			Categories:           []int{2000},
			Size:                 42949672960,
			Files:                3,
			Grabs:                120,
			Seeders:              50,
			Leechers:             2,
			Peers:                52,
			PublishDate:          "2023-11-14T22:13:20Z",
			DownloadVolumeFactor: 0.5, // medium 1 (Bluray) is half-leech
			UploadVolumeFactor:   1,
			IMDBID:               "tt0133093",
			Year:                 1999,
		},
		{
			Title:                "Some.Show.S01E01.1080p.WEB-DL", // filename, .torrent stripped
			InfoHash:             "FFEE0011",
			Link:                 dl("100002"),
			Details:              details("100002"),
			Categories:           []int{5000},
			Size:                 2147483648,
			Files:                1,
			Grabs:                7,
			Seeders:              10,
			Leechers:             1,
			Peers:                11,
			PublishDate:          "2023-11-16T03:00:00Z",
			DownloadVolumeFactor: 0, // freeleech yes
			UploadVolumeFactor:   1,
			TVDBID:               81189,
		},
		{
			Title:                "XXX Release Name", // cat 7 forces name
			InfoHash:             "99AA77",
			Link:                 dl("100003"),
			Details:              details("100003"),
			Categories:           []int{6000},
			Size:                 1073741824,
			Files:                2,
			Seeders:              3,
			Leechers:             0,
			Peers:                3,
			PublishDate:          "2023-11-17T12:00:00Z",
			DownloadVolumeFactor: 0, // XXX neutral
			UploadVolumeFactor:   0, // XXX neutral
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

// TestParseLinkCarriesPasskeyRedactsViaProxy proves the stored Link is the rebuilt
// download.php URL with the passkey (the parser's job — NeedsResolver routes it through /dl),
// the details URL carries no passkey, and apphttp.RedactURL redacts the passkey when the URL
// is logged/served.
func TestParseLinkCarriesPasskeyRedactsViaProxy(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, creds())
	got, err := d.parseReleases([]byte(`{"status":0,"data":[{"id":"42","name":"x","added":"2024-01-01T00:00:00+00:00","type_category":1}]}`))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	wantLink := "https://hdbits.test/download.php?id=42&passkey=" + credPass
	if got[0].Link != wantLink {
		t.Errorf("Link = %q, want the rebuilt download.php URL with passkey", got[0].Link)
	}
	if got[0].Details != "https://hdbits.test/details.php?id=42" {
		t.Errorf("Details = %q, want details.php?id=42 (no passkey)", got[0].Details)
	}
	if red := apphttp.RedactURL(got[0].Link); strings.Contains(red, credPass) {
		t.Errorf("RedactURL leaks the passkey: %q", red)
	}
}

// TestParseStringNumericsTolerated proves the defensive flexInt decode tolerates a fixture
// whose numerics are JSON strings, yielding the same parsed values as the bare-number form.
func TestParseStringNumericsTolerated(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search_string_numerics.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	d := parseDriver(t, creds())
	got, err := d.parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("parsed %d releases, want 1", len(got))
	}
	r := got[0]
	if r.Size != 42949672960 || r.Seeders != 50 || r.Leechers != 2 || r.Peers != 52 ||
		r.Grabs != 120 || r.Files != 3 || r.Categories[0] != 2000 || r.IMDBID != "tt0133093" ||
		r.Year != 1999 || r.DownloadVolumeFactor != 0.5 {
		t.Errorf("string-encoded numerics not decoded as the numeric form: %+v", r)
	}
}

// TestParseReleasesEmpty proves an empty data[] yields zero releases and no error.
func TestParseReleasesEmpty(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search_empty.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := parseDriver(t, creds()).parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d releases, want 0", len(got))
	}
}

// TestParseStatusSentinels proves the status->sentinel mapping: AuthDataMissing(4)/
// AuthFailed(5) -> login.ErrLoginFailed; every other non-zero status -> search.ErrParseError;
// and a malformed body -> search.ErrParseError.
func TestParseStatusSentinels(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, creds())
	cases := []struct {
		name    string
		file    string
		body    string
		wantErr error
	}{
		{name: "auth failed (5)", file: "testdata/auth_failed.json", wantErr: login.ErrLoginFailed},
		{name: "auth data missing (4)", body: `{"status":4,"message":"missing","data":null}`, wantErr: login.ErrLoginFailed},
		{name: "generic error (7)", file: "testdata/error.json", wantErr: search.ErrParseError},
		{name: "malformed json", body: `{not json`, wantErr: search.ErrParseError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := []byte(tc.body)
			if tc.file != "" {
				b, err := os.ReadFile(tc.file)
				if err != nil {
					t.Fatalf("read fixture: %v", err)
				}
				body = b
			}
			if _, err := d.parseReleases(body); !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestParseStatusErrorScrubsSecrets proves a non-zero status whose message echoes the
// username/passkey cannot leak either (scrubSecrets redacts them before the error surfaces).
func TestParseStatusErrorScrubsSecrets(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, creds())
	body := `{"status":5,"message":"bad creds ` + credUser + ` / ` + credPass + ` rejected","data":null}`
	_, err := d.parseReleases([]byte(body))
	if err == nil {
		t.Fatal("want a login error")
	}
	if strings.Contains(err.Error(), credPass) || strings.Contains(err.Error(), credUser) {
		t.Errorf("error leaks a credential: %v", err)
	}
}

// TestParseFreeleechOnlyFilter proves a non-freeleech row is dropped when freeleech_only is
// set, and kept otherwise (Prowlarr's parser-side filter).
func TestParseFreeleechOnlyFilter(t *testing.T) {
	t.Parallel()
	body := `{"status":0,"data":[
	  {"id":"1","name":"free","added":"2024-01-01T00:00:00+00:00","type_category":1,"freeleech":"yes"},
	  {"id":"2","name":"paid","added":"2024-01-02T00:00:00+00:00","type_category":1,"freeleech":"no"}
	]}`
	on := creds()
	on["freeleech_only"] = "True"
	got, err := parseDriver(t, on).parseReleases([]byte(body))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 1 || got[0].Title != "free" {
		t.Fatalf("freeleech_only on: got %d releases (%v), want 1 (free only)", len(got), got)
	}

	got, err = parseDriver(t, creds()).parseReleases([]byte(body))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("freeleech_only off: got %d releases, want 2", len(got))
	}
}

// TestUseFilenamesToggle proves use_filenames defaults to ON (filename title) and that an
// explicit falsy setting falls back to the name field.
func TestUseFilenamesToggle(t *testing.T) {
	t.Parallel()
	body := `{"status":0,"data":[{"id":"1","name":"The Name","filename":"the.file.torrent","added":"2024-01-01T00:00:00+00:00","type_category":2,"type_medium":3}]}`

	on, err := parseDriver(t, creds()).parseReleases([]byte(body))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if on[0].Title != "the.file" {
		t.Errorf("default use_filenames: Title = %q, want the.file", on[0].Title)
	}

	cfg := creds()
	cfg["use_filenames"] = "false"
	off, err := parseDriver(t, cfg).parseReleases([]byte(body))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if off[0].Title != "The Name" {
		t.Errorf("use_filenames off: Title = %q, want The Name", off[0].Title)
	}
}
