package torznab

import (
	"errors"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// parseDriver builds a driver for the response-parse tests (no HTTP — parseReleases
// is called directly on a golden body). The preset caps supply the CategoryMap.
func parseDriver(t *testing.T) *driver {
	t.Helper()
	d, err := New(native.Params{
		Def:     presetDefinition(presets[0]),
		Cfg:     map[string]string{"apikey": testAPIKey},
		BaseURL: "https://www.morethantv.me",
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver)
}

// TestParseReleases_RealCapture is the primary parity golden: the real (secret-
// scrubbed) MoreThanTV capture, decoded field-for-field against Jackett's
// ResultFromFeedItem + MoreThanTVAPI overrides.
func TestParseReleases_RealCapture(t *testing.T) {
	t.Parallel()
	d := parseDriver(t)
	releases, err := d.parseReleases(readGolden(t, "torznab_morethantv.xml"), d.Caps.CategoryMap)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases = %d, want 2", len(releases))
	}

	r0 := releases[0]
	if r0.Title != "Out of the Past 1947 720p BluRay FLAC2.0 x264-CtrlHD.mkv" {
		t.Errorf("title = %q", r0.Title)
	}
	// The x-bittorrent enclosure overrides <link> — both carry the same URL in this
	// capture, but Link must come from the enclosure per MoreThanTVAPI's override.
	wantLink := "https://www.morethantv.me/torrents.php?action=download&id=(removed)&authkey=(removed)&torrent_pass=(removed)"
	if r0.Link != wantLink {
		t.Errorf("Link = %q, want the enclosure url %q", r0.Link, wantLink)
	}
	if !strings.Contains(r0.GUID, "torrentid=836164") {
		t.Errorf("GUID = %q, want it to retain the torrentid identity", r0.GUID)
	}
	if r0.Size != 5412993028 {
		t.Errorf("Size = %d, want 5412993028 (torznab:attr size)", r0.Size)
	}
	if r0.Files != 1 {
		t.Errorf("Files = %d, want 1", r0.Files)
	}
	if r0.Grabs != 2 {
		t.Errorf("Grabs = %d, want 2", r0.Grabs)
	}
	// Two <category> elements (2000, 2040) -> last numeric wins -> 2040 -> mapped 1:1.
	if len(r0.Categories) != 1 || r0.Categories[0] != 2040 {
		t.Errorf("Categories = %v, want [2040] (last <category> element)", r0.Categories)
	}
	if r0.Seeders != 3 || r0.Leechers != 0 || r0.Peers != 3 {
		t.Errorf("seeders/leechers/peers = %d/%d/%d, want 3/0/3", r0.Seeders, r0.Leechers, r0.Peers)
	}
	if r0.DownloadVolumeFactor != 1 || r0.UploadVolumeFactor != 1 {
		t.Errorf("DVF/UVF = %v/%v, want 1/1", r0.DownloadVolumeFactor, r0.UploadVolumeFactor)
	}
	if r0.IMDBID != "tt0039689" {
		t.Errorf("IMDBID = %q, want tt0039689", r0.IMDBID)
	}
	if r0.PublishDate != "2022-12-20T21:32:17Z" {
		t.Errorf("PublishDate = %q, want the RFC1123Z pubDate normalized to RFC3339", r0.PublishDate)
	}
	if r0.Details != "https://www.morethantv.me/torrents.php?id=(removed)&torrentid=836164" {
		t.Errorf("Details = %q, want the comments url (no #comments to trim here)", r0.Details)
	}

	r1 := releases[1]
	if r1.Grabs != 0 {
		t.Errorf("second release Grabs = %d, want 0", r1.Grabs)
	}
	if r1.Files != 78 {
		t.Errorf("second release Files = %d, want 78", r1.Files)
	}
	if r1.Size != 30524085127 {
		t.Errorf("second release Size = %d, want 30524085127", r1.Size)
	}
}

// TestParseReleases_Synthetic exercises the branches the real capture does not:
// magneturl, no <category> elements (falls back to the torznab:attr), and the
// MoreThanTVAPI seeders/peers/DVF/UVF absent-attr defaults.
func TestParseReleases_Synthetic(t *testing.T) {
	t.Parallel()
	d := parseDriver(t)
	releases, err := d.parseReleases(readGolden(t, "synthetic.xml"), d.Caps.CategoryMap)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases = %d, want 1", len(releases))
	}
	r := releases[0]

	if r.Magnet != "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Synthetic.Release.2024.1080p.WEB-DL" {
		t.Errorf("Magnet = %q", r.Magnet)
	}
	if r.InfoHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("InfoHash = %q", r.InfoHash)
	}
	// No <category> elements at all -> falls back to the torznab:attr "category"=2050.
	if len(r.Categories) != 1 || r.Categories[0] != 2050 {
		t.Errorf("Categories = %v, want [2050] (attr fallback, no category elements)", r.Categories)
	}
	// No seeders/peers/DVF/UVF attrs at all -> the MoreThanTVAPI absent-attr defaults.
	if r.Seeders != 0 || r.Peers != 0 {
		t.Errorf("seeders/peers = %d/%d, want 0/0 (absent-attr default)", r.Seeders, r.Peers)
	}
	if r.DownloadVolumeFactor != 0 {
		t.Errorf("DownloadVolumeFactor = %v, want 0 (absent-attr default)", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor != 1 {
		t.Errorf("UploadVolumeFactor = %v, want 1 (absent-attr default)", r.UploadVolumeFactor)
	}
	// No x-bittorrent enclosure at all -> Link falls back to the plain <link>.
	if r.Link != "https://torznab.example.test/torrents.php?action=download&id=999&authkey=SYNTHKEY&torrent_pass=SYNTHPASS" {
		t.Errorf("Link = %q, want the plain <link> (no enclosure present)", r.Link)
	}
	if r.IMDBID != "tt1234567" {
		t.Errorf("IMDBID = %q, want tt1234567", r.IMDBID)
	}
	if r.Details != "https://torznab.example.test/torrents.php?id=999&torrentid=999" {
		t.Errorf("Details = %q, want #comments trimmed", r.Details)
	}
}

// TestParseNonXMLBody proves a non-XML, non-error 200 body (an HTML page, say) is an
// ErrParseError when fed directly to parseReleases (the higher-level non-XML guard,
// checkXMLBody, is exercised in search_test.go — this proves the XML decoder itself
// degrades safely rather than panicking).
func TestParseNonXMLBody(t *testing.T) {
	t.Parallel()
	d := parseDriver(t)
	_, err := d.parseReleases([]byte("<<<not xml"), d.Caps.CategoryMap)
	if !errors.Is(err, search.ErrParseError) {
		t.Fatalf("err = %v, want ErrParseError", err)
	}
}

// TestParseErrorEnvelopeAuth proves a 100-199 <error> envelope (even on HTTP 200) is a
// login failure.
func TestParseErrorEnvelopeAuth(t *testing.T) {
	t.Parallel()
	d := parseDriver(t)
	body := []byte(`<?xml version="1.0"?><error code="100" description="Incorrect user credentials" />`)
	_, err := d.parseReleases(body, d.Caps.CategoryMap)
	if !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("err = %v, want login.ErrLoginFailed", err)
	}
}

// TestParseErrorEnvelopeRateLimit proves a "Request limit reached" envelope is a
// rate-limit error so the registry backs off rather than recording an auth failure.
func TestParseErrorEnvelopeRateLimit(t *testing.T) {
	t.Parallel()
	d := parseDriver(t)
	body := []byte(`<?xml version="1.0"?><error code="500" description="Request limit reached" />`)
	_, err := d.parseReleases(body, d.Caps.CategoryMap)
	if !errors.Is(err, search.ErrRateLimited) {
		t.Fatalf("err = %v, want a rate-limit error", err)
	}
}

// TestParseErrorEnvelopeScrubsAPIKey proves a server-echoed <error description> that
// reflects the submitted apikey as free text is value-scrubbed before it reaches the
// error.
func TestParseErrorEnvelopeScrubsAPIKey(t *testing.T) {
	t.Parallel()
	d := parseDriver(t)
	body := []byte(`<?xml version="1.0"?><error code="100" description="key ` + testAPIKey + ` rejected"/>`)
	_, err := d.parseReleases(body, d.Caps.CategoryMap)
	if err == nil {
		t.Fatal("want an error from an <error> envelope")
	}
	assertNoAPIKey(t, "error envelope", err.Error())
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("want the [redacted] placeholder, got %q", err.Error())
	}
}

// TestParseSkipsItemWithNoLink proves an item with neither an x-bittorrent enclosure
// nor a <link> is skipped rather than emitted with an empty acquisition link.
func TestParseSkipsItemWithNoLink(t *testing.T) {
	t.Parallel()
	d := parseDriver(t)
	body := []byte(`<?xml version="1.0"?><rss xmlns:torznab="http://torznab.com/schemas/2015/feed">` +
		`<channel><item><title>No Link Release</title></item></channel></rss>`)
	releases, err := d.parseReleases(body, d.Caps.CategoryMap)
	if err != nil {
		t.Fatalf("parseReleases: %v", err)
	}
	if len(releases) != 0 {
		t.Fatalf("releases = %d, want 0 (item with no link must be skipped)", len(releases))
	}
}
