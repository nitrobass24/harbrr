package nzbindex

import (
	"errors"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestParseReleases is the parity gate for the NZBIndex JSON -> Release mapping: the quoted
// title extraction (with the trailing extension stripped), the details/guid + .nzb link
// construction, the Other category, size/files, the RFC3339 publish date from the unix
// `posted`, the usenet zero-fields invariant, and the skip of a row whose name has no
// parseable quoted title.
func TestParseReleases(t *testing.T) {
	t.Parallel()
	d := testDriver(t, nil, nil)
	releases, err := d.parseReleases(readGolden(t, "search.json"))
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	// Three rows in the fixture; the third has no quoted subject and is skipped.
	if len(releases) != 2 {
		t.Fatalf("releases = %d, want 2 (third row has no parseable title)", len(releases))
	}

	first := releases[0]
	if first.Title != "Ubuntu Gubuntu 11.10 Unity Edition (64bit)" {
		t.Errorf("title = %q, want the quoted subject with .rar stripped", first.Title)
	}
	if first.Details != testBaseURL+"/collection/a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Errorf("Details = %q", first.Details)
	}
	if first.GUID != testBaseURL+"/collection/a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Errorf("GUID = %q, want the details permalink (uuid preserved, not redacted)", first.GUID)
	}
	if first.Link != testBaseURL+"/api/download/a1b2c3d4-e5f6-7890-abcd-ef1234567890.nzb" {
		t.Errorf("Link = %q, want the .nzb download url", first.Link)
	}
	if len(first.Categories) != 1 || first.Categories[0] != categoryOther {
		t.Errorf("Categories = %v, want [%d]", first.Categories, categoryOther)
	}
	if first.Size != 8589934592 {
		t.Errorf("Size = %d, want 8589934592", first.Size)
	}
	if first.Files != 42 {
		t.Errorf("Files = %d, want 42", first.Files)
	}
	if first.PublishDate != "2023-11-14T22:13:20Z" {
		t.Errorf("PublishDate = %q, want RFC3339 from unix posted", first.PublishDate)
	}
	assertUsenetZeroFields(t, first)

	// The second row's .mkv extension is stripped from the title.
	if releases[1].Title != "Some.TV.Show.S01E01.720p.WEB-DL-GROUP" {
		t.Errorf("second title = %q, want .mkv stripped", releases[1].Title)
	}
	// Distinct rows keep distinct guids (dedup identity preserved).
	if releases[0].GUID == releases[1].GUID {
		t.Errorf("distinct releases share a guid: %q", releases[0].GUID)
	}
}

// assertUsenetZeroFields checks the torrent-only fields are zero (usenet has no ratio economy).
func assertUsenetZeroFields(t *testing.T, r *releaseT) {
	t.Helper()
	if r.Seeders != 0 || r.Leechers != 0 || r.Peers != 0 {
		t.Errorf("usenet release carries torrent peer stats: s=%d l=%d p=%d", r.Seeders, r.Leechers, r.Peers)
	}
	if r.Magnet != "" || r.InfoHash != "" {
		t.Errorf("usenet release carries a magnet/infohash: %q/%q", r.Magnet, r.InfoHash)
	}
}

// TestParseReleasesErrorEnvelope proves an API error envelope (HTTP 200 with error:true)
// surfaces as a parse error and never echoes the configured apikey back.
func TestParseReleasesErrorEnvelope(t *testing.T) {
	t.Parallel()
	d := testDriver(t, map[string]string{"apikey": testAPIKey}, nil)
	body := `{"error":true,"errorMessage":"Invalid API key ` + testAPIKey + `","data":{"content":[]}}`
	_, err := d.parseReleases([]byte(body))
	if err == nil {
		t.Fatal("want an error for error:true envelope")
	}
	if !errors.Is(err, search.ErrParseError) {
		t.Errorf("err = %v, want ErrParseError", err)
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Errorf("error leaked the apikey: %q", err.Error())
	}
}

// TestParseReleasesMalformed proves a non-JSON body is an ErrParseError, not a panic.
func TestParseReleasesMalformed(t *testing.T) {
	t.Parallel()
	d := testDriver(t, nil, nil)
	if _, err := d.parseReleases([]byte("<html>not json</html>")); !errors.Is(err, search.ErrParseError) {
		t.Errorf("err = %v, want ErrParseError", err)
	}
}
