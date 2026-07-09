package myanonamouse

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

// goldenDriver is the family driver (full caps) for parse tests that need the category
// map.
func goldenDriver(t *testing.T) *driver {
	t.Helper()
	for _, f := range Families() {
		if f.Definition.ID == "myanonamouse" {
			d, err := New(native.Params{Def: f.Definition})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			return d.(*driver)
		}
	}
	t.Fatal("no myanonamouse family")
	return nil
}

// TestParseReleasesGolden parses the synthetic search response and asserts the full
// DTO->Release mapping (title with appended author, the human-readable size in bytes,
// the freeleech download factor, the category, the explicit download.php Link) and the
// descending publish-date sort. The goldens are derived from Prowlarr's parse contract,
// not a live capture.
func TestParseReleasesGolden(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/search_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := goldenDriver(t).parseReleases(body)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}

	// Sorted by publish date descending: Project Hail Mary (Mar) > The Silent Patient (Jan).
	want := []*normalizer.Release{
		{
			Title: "Project Hail Mary by Andy Weir, Ray Porter", Author: "Andy Weir, Ray Porter",
			Link:    "https://www.myanonamouse.net/tor/download.php/DLHASH-BBBB?tid=202",
			Details: "https://www.myanonamouse.net/t/202",
			// cat 47 -> Audio/Audiobook (3030) + custom 1:1 (100047).
			Categories: []int{3030, 100047}, Size: 1385126953, Files: 1, Grabs: 8,
			Seeders: 90, Leechers: 10, Peers: 100, PublishDate: "2024-03-01T08:00:00Z",
			DownloadVolumeFactor: 1, UploadVolumeFactor: 1, MinimumRatio: 1, MinimumSeedTime: 259200,
		},
		{
			Title: "The Silent Patient by Alex Michaelides", Author: "Alex Michaelides",
			Link:    "https://www.myanonamouse.net/tor/download.php/DLHASH-AAAA?tid=101",
			Details: "https://www.myanonamouse.net/t/101",
			// cat 13 -> Audio/Audiobook (3030) + custom 1:1 (100013).
			Categories: []int{3030, 100013}, Size: 268959744, Files: 2, Grabs: 120,
			Seeders: 47, Leechers: 3, Peers: 50, PublishDate: "2024-01-15T10:30:00Z",
			DownloadVolumeFactor: 0, UploadVolumeFactor: 1, MinimumRatio: 1, MinimumSeedTime: 259200,
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

func TestParseReleasesEmptyAndNothingReturned(t *testing.T) {
	t.Parallel()
	d := builderDriver(nil)
	// A legitimate empty result.
	got, err := d.parseReleases([]byte(`{"error":"","data":[]}`))
	if err != nil || len(got) != 0 {
		t.Errorf("empty data: got %d err=%v, want 0/nil", len(got), err)
	}
	// MAM's "Nothing returned, out of …" Error is no-results, not a parse error.
	got, err = d.parseReleases([]byte(`{"error":"Nothing returned, out of 0 results","data":null}`))
	if err != nil || len(got) != 0 {
		t.Errorf("nothing-returned: got %d err=%v, want 0/nil", len(got), err)
	}
}

func TestParseReleasesErrors(t *testing.T) {
	t.Parallel()
	d := goldenDriver(t)
	cases := []struct {
		name, body string
		// wantToken, when non-empty, is an actionable diagnostic substring the enriched
		// decode-error message must carry (F1) — proving DecodeErrorDetail is threaded
		// through while errors.Is(…, ErrParseError) still holds.
		wantToken string
	}{
		{name: "malformed json", body: `{"data": [`, wantToken: "invalid"},
		{name: "non-json html body", body: `<html>not json</html>`, wantToken: "bytes"},
		{name: "no data array (empty object)", body: `{}`},
		{name: "explicit null data", body: `{"error":"","data":null}`},
		{name: "server error string", body: `{"error":"Something broke","data":[]}`},
		{name: "bad size", body: `{"error":"","data":[{"id":1,"title":"x","size":"NOTASIZE","added":"2024-01-01 00:00:00","dl":"h"}]}`},
		{name: "bad date", body: `{"error":"","data":[{"id":1,"title":"x","size":"1 MB","added":"not-a-date","dl":"h"}]}`},
	}
	for _, tc := range cases {
		_, err := d.parseReleases([]byte(tc.body))
		if !errors.Is(err, search.ErrParseError) {
			t.Errorf("%s: err = %v, want search.ErrParseError", tc.name, err)
			continue
		}
		if tc.wantToken != "" && !strings.Contains(err.Error(), tc.wantToken) {
			t.Errorf("%s: err = %q, want actionable token %q", tc.name, err, tc.wantToken)
		}
	}
}

// TestParseReleasesScrubsMamID proves the server-echoed `error` string cannot leak the
// configured mam_id session cookie: a server that reflects the submitted mam_id back into its
// error text is value-scrubbed before the error reaches the health-event / webhook surface.
// Fail-before: parseReleases surfaced resp.Error verbatim (mam_id leaked); pass-after: the raw
// value is replaced with the placeholder while the surrounding message is preserved.
func TestParseReleasesScrubsMamID(t *testing.T) {
	t.Parallel()
	const mamID = "MAMID-SESSION-SECRET-9911"
	var d *driver
	for _, f := range Families() {
		if f.Definition.ID == "myanonamouse" {
			drv, err := New(native.Params{Def: f.Definition, Cfg: map[string]string{"mam_id": mamID}})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			d = drv.(*driver)
		}
	}
	if d == nil {
		t.Fatal("no myanonamouse family")
	}
	body := []byte(`{"error":"Bad session for mam_id=` + mamID + `","data":[]}`)
	_, err := d.parseReleases(body)
	if !errors.Is(err, search.ErrParseError) {
		t.Fatalf("err = %v, want search.ErrParseError", err)
	}
	if strings.Contains(err.Error(), mamID) {
		t.Fatalf("parseReleases leaked mam_id: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("expected [redacted] placeholder, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "Bad session") {
		t.Errorf("scrub removed non-secret context: %q", err.Error())
	}
}

func TestParseSize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int64
	}{
		{"256.50 MB", 268959744},
		{"1.29 GB", 1385126953},
		{"1,001.6 MiB", 1050253722}, // comma thousands separator (sizes ≥ 1000)
		{"1 B", 1},
		{"1 KiB", 1024},
		{"2 TB", 2199023255552},
		{"512 b", 512}, // case-insensitive unit
	}
	for _, tc := range cases {
		got, err := parseSize(tc.in)
		if err != nil {
			t.Errorf("parseSize(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseSize(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"", "GB", "1.2", "10 ZB", "abc MB"} {
		if _, err := parseSize(bad); !errors.Is(err, search.ErrParseError) {
			t.Errorf("parseSize(%q) err = %v, want search.ErrParseError", bad, err)
		}
	}
}

// TestAuthorNames proves the stringified author_info dict is parsed defensively: a well
// formed dict yields the (sorted) names, a malformed one yields none (never a panic).
func TestAuthorNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"single", `{"55":"Alex Michaelides"}`, []string{"Alex Michaelides"}},
		{"multiple sorted", `{"7":"Andy Weir","8":"Ray Porter"}`, []string{"Andy Weir", "Ray Porter"}},
		{"empty string", "", nil},
		{"malformed json", `{"7":"Andy Weir"`, nil},
		{"not an object", `["nope"]`, nil},
		{"blank names dropped", `{"1":"  ","2":"Real"}`, []string{"Real"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := authorNames(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("authorNames(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestDownloadVolumeFactor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		row  mamRelease
		want float64
	}{
		{mamRelease{}, 1},
		{mamRelease{Free: true}, 0},
		{mamRelease{PersonalFreeleech: true}, 0},
		{mamRelease{FlVIP: true}, 0},
	}
	for _, tc := range cases {
		if got := downloadVolumeFactor(&tc.row); got != tc.want {
			t.Errorf("downloadVolumeFactor(%+v) = %v, want %v", tc.row, got, tc.want)
		}
	}
}

// TestParseReleasesLiveIntegerShape locks the fix for MAM's live API returning
// integers where the documented contract used string ids and boolean flags
// (category/main_cat as numbers, free/personal_freeleech/fl_vip as 0/1) — the
// strict struct previously failed json.Unmarshal ("decode search response").
func TestParseReleasesLiveIntegerShape(t *testing.T) {
	t.Parallel()
	const body = `{"data":[{` +
		`"id":202,"title":"Live Book","author_info":"{\"1\":\"Author X\"}",` +
		`"category":47,"main_cat":13,"added":"2024-03-01 08:00:00","size":"1.29 GiB",` +
		`"seeders":5,"leechers":1,"numfiles":3,"free":1,"personal_freeleech":0,"fl_vip":0,"dl":"HASH"}]}`
	got, err := goldenDriver(t).parseReleases([]byte(body))
	if err != nil {
		t.Fatalf("parseReleases (integer shape): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("releases = %d, want 1", len(got))
	}
	r := got[0]
	// category 47 maps the same as the golden (number vs string id is transparent).
	if want := []int{3030, 100047}; !reflect.DeepEqual(r.Categories, want) {
		t.Errorf("Categories = %v, want %v", r.Categories, want)
	}
	// free:1 (integer) must yield a freeleech download factor of 0.
	if r.DownloadVolumeFactor != 0 {
		t.Errorf("DownloadVolumeFactor = %v, want 0 (free:1)", r.DownloadVolumeFactor)
	}
	if r.Title != "Live Book by Author X" {
		t.Errorf("Title = %q", r.Title)
	}
}
