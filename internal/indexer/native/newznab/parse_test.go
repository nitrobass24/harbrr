package newznab

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// parseDriver builds a driver for the response-parse tests (no HTTP — parseReleases is
// called directly on a golden body). The placeholder caps supply the CategoryMap.
func parseDriver(t *testing.T) *driver {
	t.Helper()
	d, err := New(native.Params{Def: GenericDefinition(), BaseURL: "https://news.example.test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %q: %v", name, err)
	}
	return b
}

// TestParseReleases is the parity gate for the Newznab RSS -> Release mapping: enclosure
// url -> Link, newznab:attr size over enclosure length, category via the CategoryMap,
// grabs/files, ids, usenetdate overriding pubDate, coverurl -> Poster, the comments/details
// split, the no-enclosure skip, and the usenet zero-fields invariant.
func TestParseReleases(t *testing.T) {
	t.Parallel()
	d := parseDriver(t)
	releases, err := d.parseReleases(readGolden(t, "search.xml"), d.caps.CategoryMap)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	// Three items in the feed, but the third has no enclosure and is skipped.
	if len(releases) != 2 {
		t.Fatalf("releases = %d, want 2 (third item has no nzb enclosure)", len(releases))
	}

	movie := releases[0]
	if movie.Title != "Example.Movie.2023.1080p.WEB-DL" {
		t.Errorf("title = %q", movie.Title)
	}
	if movie.Link == "" || movie.Link != "https://news.example.test/getnzb/abc123.nzb&i=1&r=APIKEYPLACEHOLDER" {
		t.Errorf("Link = %q, want the enclosure nzb url", movie.Link)
	}
	// The upstream <guid> is carried as the stable dedup identity (here a passkey-free
	// details permalink); the apikey rides only the enclosure url in Link above.
	if movie.GUID != "https://news.example.test/details/abc123" {
		t.Errorf("GUID = %q, want the upstream <guid> permalink", movie.GUID)
	}
	// newznab:attr size (2147483648) is preferred over enclosure length (1500000000).
	if movie.Size != 2147483648 {
		t.Errorf("Size = %d, want 2147483648 (newznab:attr size over enclosure length)", movie.Size)
	}
	if len(movie.Categories) != 1 || movie.Categories[0] != 2000 {
		t.Errorf("Categories = %v, want [2000]", movie.Categories)
	}
	if movie.Grabs != 42 || movie.Files != 7 {
		t.Errorf("grabs/files = %d/%d, want 42/7", movie.Grabs, movie.Files)
	}
	// usenetdate overrides pubDate.
	if movie.PublishDate != "Sun, 01 Jan 2023 10:00:00 +0000" {
		t.Errorf("PublishDate = %q, want the usenetdate value", movie.PublishDate)
	}
	if movie.IMDBID != "0133093" || movie.TMDBID != 603 {
		t.Errorf("ids = %q/%d, want 0133093/603", movie.IMDBID, movie.TMDBID)
	}
	if movie.Poster != "https://news.example.test/covers/abc123.jpg" {
		t.Errorf("Poster = %q, want the coverurl (NOT the usenet poster attr)", movie.Poster)
	}
	if movie.Comments != "https://news.example.test/details/abc123#comments" {
		t.Errorf("Comments = %q, want the raw comments url", movie.Comments)
	}
	if movie.Details != "https://news.example.test/details/abc123" {
		t.Errorf("Details = %q, want comments with #comments trimmed", movie.Details)
	}
	assertUsenetZeroFields(t, movie)

	show := releases[1]
	if show.GUID != "def456" {
		t.Errorf("show GUID = %q, want the upstream bare-id <guid> def456", show.GUID)
	}
	// No newznab:attr size; falls back to the enclosure length.
	if show.Size != 734003200 {
		t.Errorf("show Size = %d, want enclosure length fallback 734003200", show.Size)
	}
	if len(show.Categories) != 1 || show.Categories[0] != 5000 {
		t.Errorf("show Categories = %v, want [5000]", show.Categories)
	}
	if show.TVDBID != 81189 || show.RageID != 2930 {
		t.Errorf("show ids = %d/%d, want 81189/2930", show.TVDBID, show.RageID)
	}
	// No usenetdate: falls back to pubDate.
	if show.PublishDate != "Tue, 03 Jan 2023 12:00:00 +0000" {
		t.Errorf("show PublishDate = %q, want the pubDate fallback", show.PublishDate)
	}
	assertUsenetZeroFields(t, show)
}

// assertUsenetZeroFields proves the torrent-only fields stay zero for a usenet release.
func assertUsenetZeroFields(t *testing.T, r *releaseT) {
	t.Helper()
	if r.Seeders != 0 || r.Leechers != 0 || r.Peers != 0 {
		t.Errorf("seeders/leechers/peers = %d/%d/%d, want all 0 (usenet)", r.Seeders, r.Leechers, r.Peers)
	}
	if r.Magnet != "" || r.InfoHash != "" {
		t.Errorf("magnet/infohash = %q/%q, want empty (usenet)", r.Magnet, r.InfoHash)
	}
	if r.DownloadVolumeFactor != 0 || r.UploadVolumeFactor != 0 {
		t.Errorf("volume factors = %v/%v, want 0/0 (usenet)", r.DownloadVolumeFactor, r.UploadVolumeFactor)
	}
}

// TestParseRedactsSecretInGUID proves the defense-in-depth redaction of the upstream
// <guid>: a misbehaving server that puts an apikey-bearing download URL in <guid> must not
// leak it into the served feed (the guid is emitted verbatim). The secret is stripped while
// the stable path survives, so dedup stays churn-immune.
func TestParseRedactsSecretInGUID(t *testing.T) {
	t.Parallel()
	d := parseDriver(t)
	body := []byte(`<?xml version="1.0"?><rss><channel><item>` +
		`<title>Example.Release</title>` +
		`<guid isPermaLink="true">https://news.example.test/getnzb/abc.nzb?apikey=SUPERSECRET</guid>` +
		`<enclosure url="https://news.example.test/getnzb/abc.nzb?apikey=SUPERSECRET" length="1000" type="application/x-nzb"/>` +
		`</item></channel></rss>`)
	rels, err := d.parseReleases(body, d.caps.CategoryMap)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("releases = %d, want 1", len(rels))
	}
	if strings.Contains(rels[0].GUID, "SUPERSECRET") {
		t.Errorf("apikey leaked into Release.GUID: %q", rels[0].GUID)
	}
	if !strings.Contains(rels[0].GUID, "getnzb/abc.nzb") {
		t.Errorf("GUID should retain the stable path after redaction: %q", rels[0].GUID)
	}
}

// TestParseErrorEnvelopeAuth proves a 100-199 error envelope (even on HTTP 200) is detected
// before item parsing and surfaces as a login failure.
func TestParseErrorEnvelopeAuth(t *testing.T) {
	t.Parallel()
	d := parseDriver(t)
	_, err := d.parseReleases(readGolden(t, "error_auth.xml"), d.caps.CategoryMap)
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
}

// TestParseErrorEnvelopeAPIKey proves a 200-range "Missing parameter (apikey)" envelope is
// promoted to a login failure (Prowlarr behaviour).
func TestParseErrorEnvelopeAPIKey(t *testing.T) {
	t.Parallel()
	d := parseDriver(t)
	_, err := d.parseReleases(readGolden(t, "error_apikey.xml"), d.caps.CategoryMap)
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed (apikey error promoted to auth)", err)
	}
}

// TestParseErrorRateLimit proves a "Request limit reached" envelope is a rate-limit error so
// the registry backs off rather than recording an auth failure.
func TestParseErrorRateLimit(t *testing.T) {
	t.Parallel()
	d := parseDriver(t)
	body := []byte(`<?xml version="1.0"?><error code="500" description="Request limit reached" />`)
	_, err := d.parseReleases(body, d.caps.CategoryMap)
	if !errors.Is(err, search.ErrRateLimited) {
		t.Fatalf("err = %v, want a rate-limit error", err)
	}
}

// TestParseMalformedBody proves a non-XML body is an ErrParseError, not a panic.
func TestParseMalformedBody(t *testing.T) {
	t.Parallel()
	d := parseDriver(t)
	_, err := d.parseReleases([]byte("<<<not xml"), d.caps.CategoryMap)
	if !errors.Is(err, search.ErrParseError) {
		t.Fatalf("err = %v, want ErrParseError", err)
	}
	// The decode error is now enriched with an actionable, payload-free diagnostic.
	if !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("err = %q, want an actionable decode detail containing %q", err.Error(), "invalid")
	}
}
