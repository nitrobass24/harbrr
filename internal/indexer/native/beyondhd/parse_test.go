package beyondhd

import (
	"encoding/json"
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

// Synthetic credentials — they exist only to prove the api_key (URL path) and the rsskey
// (download URL / error echo) are scrubbed. They live only in this _test.go file.
const (
	credAPIKey = "APIKEY-SECRET-00000000000000000"
	credRSSKey = "RSSKEY00000000000000000000000000"
)

// parseDriver builds a full driver (caps + cfg) for the parser tests.
func parseDriver(t *testing.T, cfg map[string]string) *driver {
	t.Helper()
	def := Families()[0].Definition
	d, err := New(native.Params{Def: def, Cfg: cfg})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	return d.(*driver)
}

func creds() map[string]string {
	return map[string]string{"api_key": credAPIKey, "rsskey": credRSSKey}
}

// TestParseReleasesGolden parses the synthetic multi-result fixture and asserts the full
// row->Release mapping: the verbatim name title, the single newznab category by the
// description string, size/seeders/peers/grabs, the created_at->UTC publish date, the
// imdb/tmdb ids, and the freeleech/promo volume factors. The goldens are derived from
// Prowlarr's BeyondHD parse contract, not a live capture. Releases are ordered by
// descending publish date, so the 2024-03-15 movie precedes the 2024-03-10 show.
func TestParseReleasesGolden(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := parseDriver(t, creds()).parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}

	want := []*normalizer.Release{
		{
			Title:                "Some Movie 2021 1080p BluRay DD5.1 x264-GRP",
			InfoHash:             "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			Link:                 "https://beyond-hd.me/torrent/download/auto.12345.RSSKEY00000000000000000000000000",
			Details:              "https://beyond-hd.me/details/12345",
			Categories:           []int{2000},
			Size:                 6543210000,
			Grabs:                42,
			Seeders:              25,
			Leechers:             3,
			Peers:                28,
			PublishDate:          "2024-03-15T12:30:45Z",
			DownloadVolumeFactor: 0, // freeleech
			UploadVolumeFactor:   1,
			MinimumRatio:         1,
			MinimumSeedTime:      172800,
			IMDBID:               "tt1234567",
			TMDBID:               603,
		},
		{
			Title:                "Some Show S01 720p WEB-DL DDP5.1 H264-GRP",
			InfoHash:             "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
			Link:                 "https://beyond-hd.me/torrent/download/auto.67890.RSSKEY00000000000000000000000000",
			Details:              "https://beyond-hd.me/details/67890",
			Categories:           []int{5000},
			Size:                 1234567890,
			Grabs:                5,
			Seeders:              10,
			Leechers:             2,
			Peers:                12,
			PublishDate:          "2024-03-10T08:00:00Z",
			DownloadVolumeFactor: 0.5, // promo50
			UploadVolumeFactor:   1,
			MinimumRatio:         1,
			MinimumSeedTime:      172800,
			IMDBID:               "tt7654321",
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

// TestParseLinkCarriesRSSKeyRoutedViaResolver proves the stored Link is the download_url
// embedding the rsskey verbatim and the details URL carries no secret. BeyondHD embeds the
// rsskey in the URL *path* (auto.<id>.<rsskey>), NOT a query param, so apphttp.RedactURL —
// which only redacts query values — cannot hide it. The protection is therefore structural:
// NeedsResolver()==true keeps this secret-bearing URL out of the served feed entirely (it is
// only ever fetched server-side by Grab via /dl), so it must never reach a log/feed as-is.
func TestParseLinkCarriesRSSKeyRoutedViaResolver(t *testing.T) {
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
	if !strings.Contains(got[0].Link, credRSSKey) {
		t.Fatalf("Link should be the download_url embedding the rsskey, got %q", got[0].Link)
	}
	if strings.Contains(got[0].Details, credRSSKey) {
		t.Errorf("Details URL leaks the rsskey: %q", got[0].Details)
	}
	if !d.NeedsResolver() {
		t.Error("NeedsResolver must be true: the path-embedded rsskey is only safe behind /dl")
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
	got, err := parseDriver(t, creds()).parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("parsed %d releases, want 1", len(got))
	}
	r := got[0]
	if r.Size != 6543210000 || r.Seeders != 25 || r.Leechers != 3 || r.Peers != 28 ||
		r.Grabs != 42 || r.Categories[0] != 2000 || r.TMDBID != 603 {
		t.Errorf("string-encoded numerics not decoded as the numeric form: %+v", r)
	}
}

// TestParseReleasesEmpty proves an empty results[] yields zero releases and no error.
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

// TestParseStatusSentinels proves the failure mapping: an "Invalid API Key" body (whether
// the JSON status_message or a bare text body) -> login.ErrLoginFailed; any other
// status_code==0 -> search.ErrParseError; and a malformed body -> search.ErrParseError.
func TestParseStatusSentinels(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, creds())
	cases := []struct {
		name string
		file string
		body string
		// wantToken, when set, must appear in err.Error(): the actionable decode
		// diagnostic DecodeErrorDetail now embeds (offset/invalid for malformed JSON,
		// "bytes" for a non-JSON body) while errors.Is(err, wantErr) still holds.
		wantToken string
		wantErr   error
	}{
		{name: "invalid api key (json)", file: "testdata/auth_failed.json", wantErr: login.ErrLoginFailed},
		{name: "invalid api key (bare body)", body: `Invalid API Key`, wantErr: login.ErrLoginFailed},
		{name: "generic error envelope", file: "testdata/error.json", wantErr: search.ErrParseError},
		{name: "malformed json", body: `{not json`, wantToken: "offset", wantErr: search.ErrParseError},
		{name: "non-json body", body: `<html>not json</html>`, wantToken: "bytes", wantErr: search.ErrParseError},
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
			_, err := d.parseReleases(body)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantToken != "" && !strings.Contains(err.Error(), tc.wantToken) {
				t.Errorf("err = %q, want it to contain actionable token %q", err, tc.wantToken)
			}
		})
	}
}

// TestParseStatusErrorScrubsSecrets proves a failure message that echoes the api_key or
// rsskey cannot leak either (scrubSecrets redacts them before the error surfaces).
func TestParseStatusErrorScrubsSecrets(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, creds())
	body := `{"status_code":0,"status_message":"rejected ` + credAPIKey + ` / ` + credRSSKey + `","results":null}`
	_, err := d.parseReleases([]byte(body))
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), credAPIKey) || strings.Contains(err.Error(), credRSSKey) {
		t.Errorf("error leaks a credential: %v", err)
	}
}

// TestDownloadVolumeFactor pins the freeleech/limited/promo matrix (Prowlarr
// GetDownloadVolumeFactor), including the descending-discount precedence.
func TestDownloadVolumeFactor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		row  bhdTorrent
		want float64
	}{
		{"freeleech", bhdTorrent{Freeleech: true}, 0},
		{"limited", bhdTorrent{Limited: true}, 0},
		{"promo75", bhdTorrent{Promo75: true}, 0.25},
		{"promo50", bhdTorrent{Promo50: true}, 0.5},
		{"promo25", bhdTorrent{Promo25: true}, 0.75},
		{"none", bhdTorrent{}, 1},
		{"freeleech wins over promo", bhdTorrent{Freeleech: true, Promo50: true}, 0},
		{"promo75 wins over promo25", bhdTorrent{Promo75: true, Promo25: true}, 0.25},
	}
	for _, c := range cases {
		if got := downloadVolumeFactor(&c.row); got != c.want {
			t.Errorf("%s: downloadVolumeFactor = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestFlexBoolUnmarshal proves flexBool tolerates every wire encoding BeyondHD (or an older
// server) might use — bare bool, 1/0, and their quoted forms — and, critically, that an
// UNEXPECTED token degrades to false rather than failing the whole-page decode (an
// UnmarshalJSON error would abort the entire search response, the bug this replaces).
func TestFlexBoolUnmarshal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{`true`, true},
		{`false`, false},
		{`1`, true},
		{`0`, false},
		{`"true"`, true},
		{`"false"`, false},
		{`"1"`, true},
		{`"0"`, false},
		{`null`, false},
		{`2`, false},     // unexpected number: degrade, do not fail the page
		{`"yes"`, false}, // unexpected string: degrade, do not fail the page
	}
	for _, c := range cases {
		var b flexBool
		if err := json.Unmarshal([]byte(c.in), &b); err != nil {
			t.Errorf("Unmarshal(%s): unexpected error %v", c.in, err)
			continue
		}
		if bool(b) != c.want {
			t.Errorf("Unmarshal(%s) = %v, want %v", c.in, bool(b), c.want)
		}
	}
}

// TestParseReleasesNumericBooleans proves a full page whose boolean fields arrive as numbers
// (freeleech=1) still decodes and applies the freeleech volume factor — the whole-page
// decode failure the strict boolean decode used to cause.
func TestParseReleasesNumericBooleans(t *testing.T) {
	t.Parallel()
	const body = `{"status_code":1,"results":[{"name":"Row","info_hash":"abc",` +
		`"category":"Movies","type":"BD Remux","size":1,"seeders":1,"leechers":0,` +
		`"created_at":"2024-01-01 00:00:00","url":"https://beyond-hd.me/torrents/1",` +
		`"download_url":"https://beyond-hd.me/dl/1","freeleech":1,"limited":0}]}`
	got, err := parseDriver(t, creds()).parseReleases([]byte(body))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d releases, want 1", len(got))
	}
	if got[0].DownloadVolumeFactor != 0 {
		t.Errorf("DownloadVolumeFactor = %v, want 0 (freeleech=1 must be honored)", got[0].DownloadVolumeFactor)
	}
}

// TestTmdbID proves the "movie/<id>" string is parsed to its bare numeric id and that blank
// or non-numeric forms yield 0.
func TestTmdbID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int64
	}{
		{"movie/603", 603},
		{"tv/1399", 1399},
		{"", 0},
		{"movie/", 0},
		{"603", 603},
	}
	for _, c := range cases {
		if got := tmdbID(c.in); got != c.want {
			t.Errorf("tmdbID(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestPublishDate proves created_at parses to UTC RFC3339 across the observed space form
// and the RFC3339 variants, and that an unparseable/empty value yields "".
func TestPublishDate(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"2024-03-15 12:30:45", "2024-03-15T12:30:45Z"},
		{"2024-03-15T12:30:45Z", "2024-03-15T12:30:45Z"},
		{"2024-03-15T12:30:45+02:00", "2024-03-15T10:30:45Z"},
		{"2024-03-15T12:30:45", "2024-03-15T12:30:45Z"},
		{"", ""},
		{"not a date", ""},
	}
	for _, c := range cases {
		if got := publishDate(c.in); got != c.want {
			t.Errorf("publishDate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCanonicalIMDB proves imdb_id is normalized to the canonical "tt"+7-digit form before
// it reaches the feed (the torznab serializer does not normalize): a bare numeric is
// tt-prefixed and zero-padded, an under-padded id is re-padded, an already-canonical value
// is preserved, and junk (a URL, non-numeric, zero, blank) yields "" rather than a
// malformed imdb that would serialize verbatim.
func TestCanonicalIMDB(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"0133093", "tt0133093"},                  // bare numeric -> tt-prefixed
		{"133093", "tt0133093"},                   // under-padded bare numeric
		{"tt133093", "tt0133093"},                 // under-padded tt id re-padded
		{"tt1234567", "tt1234567"},                // already canonical, preserved
		{"1234567", "tt1234567"},                  // bare 7-digit
		{"https://imdb.com/title/tt0133093/", ""}, // junk URL dropped
		{"not-an-id", ""},                         // non-numeric dropped
		{"0", ""},                                 // zero dropped (absent)
		{"tt0", ""},                               // tt-zero dropped
		{"", ""},                                  // blank dropped
		{"  tt0133093  ", "tt0133093"},            // surrounding whitespace
	}
	for _, c := range cases {
		if got := canonicalIMDB(c.in); got != c.want {
			t.Errorf("canonicalIMDB(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestToReleaseNormalizesIMDB proves the row->Release mapping routes imdb_id through
// canonicalIMDB so a raw wire value (here a bare, unpadded numeric) is stored in canonical
// form rather than verbatim.
func TestToReleaseNormalizesIMDB(t *testing.T) {
	t.Parallel()
	d := parseDriver(t, creds())
	rel := d.toRelease(&bhdTorrent{ImdbID: "0133093"})
	if rel.IMDBID != "tt0133093" {
		t.Errorf("toRelease IMDBID = %q, want %q", rel.IMDBID, "tt0133093")
	}
}
