package torznab

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

// fixedNow is the deterministic pubDate fallback clock for the results goldens.
func fixedNow() time.Time { return time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC) }

func demoFeed() FeedInfo {
	return FeedInfo{
		IndexerID:   "demo",
		Name:        "Demo Tracker",
		Description: "Synthetic tracker for the Torznab results goldens.",
		SiteLink:    "https://demo.test/",
		Type:        "public",
		SelfURL:     "https://harbrr.local/api/indexers/demo/results/torznab",
	}
}

// fullRelease exercises every emitted field: standard + custom categories, a
// download link carrying a (synthetic) passkey — intended served output — a
// freeleech downloadvolumefactor of 0, external ids, media fields, and a dated
// release with a non-UTC offset (to pin the RFC1123Z rendering).
func fullRelease() *normalizer.Release {
	return &normalizer.Release{
		Title:                "Example.Movie.2024.1080p.BluRay",
		Details:              "https://demo.test/torrent/1",
		Link:                 "https://demo.test/download/1.torrent?passkey=synthetic-demo-key",
		Size:                 5368709120,
		Categories:           []int{2040, 100001},
		Seeders:              12,
		Leechers:             3,
		Peers:                15,
		Grabs:                7,
		Files:                4,
		PublishDate:          "2024-03-14T17:10:42-04:00",
		DownloadVolumeFactor: 0, // freeleech: 0 must be emitted, not dropped
		UploadVolumeFactor:   1,
		MinimumRatio:         1.5,
		MinimumSeedTime:      172800,
		IMDBID:               "tt0903747",
		TMDBID:               1396,
		TVDBID:               81189,
		TVMazeID:             82701,
		TraktID:              1390,
		DoubanID:             1291543,
		RageID:               75682,
		Year:                 2024,
		Genre:                "Drama,Crime",
		Poster:               "https://demo.test/poster/1.jpg",
	}
}

// mediaRelease exercises the book/music descriptive torznab:attr fields
// (author/booktitle/publisher/artist/album/label/track) and their emission order.
func mediaRelease() *normalizer.Release {
	return &normalizer.Release{
		Title:                "Some Audiobook + Album Bundle",
		Link:                 "https://demo.test/download/3.torrent",
		Size:                 256000000,
		Categories:           []int{3030},
		Seeders:              3,
		Peers:                3,
		PublishDate:          "2024-05-01T00:00:00Z",
		DownloadVolumeFactor: 1,
		UploadVolumeFactor:   1,
		Author:               "Jane Author",
		BookTitle:            "The Book",
		Publisher:            "Pub House",
		Artist:               "The Artist",
		Album:                "The Album",
		Label:                "The Label",
		Track:                "Track One",
	}
}

// magnetOnlyRelease has no download link: guid/link/enclosure all fall back to
// the magnet (which carries an & that must XML-escape to &amp;), and size is 0
// (must render <size>0</size> and length="0").
func magnetOnlyRelease() *normalizer.Release {
	return &normalizer.Release{
		Title:                "Magnet Only Release",
		Magnet:               "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Magnet+Only+Release",
		InfoHash:             "0123456789ABCDEF0123456789ABCDEF01234567",
		Size:                 0,
		Categories:           []int{5070},
		Seeders:              0,
		Peers:                0,
		DownloadVolumeFactor: 1,
		UploadVolumeFactor:   1,
	}
}

// minimalBadCharRelease has only the required fields, no date (pubDate falls
// back to now), and a control char (0x1A) in the title that must be stripped.
func minimalBadCharRelease() *normalizer.Release {
	return &normalizer.Release{
		Title:                "Minimal\x1aRelease",
		Link:                 "https://demo.test/download/2.torrent",
		Size:                 1048576,
		Categories:           []int{2030},
		Seeders:              1,
		Peers:                1,
		DownloadVolumeFactor: 1,
		UploadVolumeFactor:   1,
	}
}

func TestMarshalResultsGolden(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		golden   string
		releases []*normalizer.Release
	}{
		{
			name:     "feed",
			golden:   "results/feed.xml",
			releases: []*normalizer.Release{fullRelease(), magnetOnlyRelease(), minimalBadCharRelease(), mediaRelease()},
		},
		{
			name:     "empty",
			golden:   "results/empty.xml",
			releases: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := MarshalResults(demoFeed(), tt.releases, fixedNow())
			if err != nil {
				t.Fatalf("MarshalResults: %v", err)
			}
			assertGolden(t, tt.golden, got)
			assertWellFormed(t, got)
		})
	}
}

// TestResultsGuidPrecedence pins the guid precedence: the upstream Release.GUID
// (when present) wins, then Jackett's FixResults order — Link, else Magnet, else
// Details.
func TestResultsGuidPrecedence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		r    *normalizer.Release
		want string
	}{
		{"upstream guid wins", &normalizer.Release{GUID: "G", Link: "L", Magnet: "M", Details: "D"}, "G"},
		{"link wins", &normalizer.Release{Link: "L", Magnet: "M", Details: "D"}, "L"},
		{"magnet when no link", &normalizer.Release{Magnet: "M", Details: "D"}, "M"},
		{"details last", &normalizer.Release{Details: "D"}, "D"},
	}
	for _, tt := range tests {
		if got := GUIDFor(tt.r); got != tt.want {
			t.Errorf("%s: GUIDFor = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestResultsZeroSizeAndFreeleech confirms <size>0</size>, enclosure length="0",
// and a freeleech downloadvolumefactor of 0 are all emitted (not dropped).
func TestResultsZeroSizeAndFreeleech(t *testing.T) {
	t.Parallel()
	got, err := MarshalResults(demoFeed(), []*normalizer.Release{magnetOnlyRelease()}, fixedNow())
	if err != nil {
		t.Fatalf("MarshalResults: %v", err)
	}
	s := string(got)
	for _, want := range []string{
		"<size>0</size>",
		`length="0"`,
		`name="seeders" value="0"`,
		`name="downloadvolumefactor" value="1"`,
		"&amp;dn=Magnet", // the magnet & is XML-escaped, not mangled
	} {
		if !strings.Contains(s, want) {
			t.Errorf("results missing %q in:\n%s", want, s)
		}
	}
}

// TestResultsStripsInvalidXMLChars confirms a control char in a title is removed
// before marshaling (parity with Jackett's RemoveInvalidXMLChars).
func TestResultsStripsInvalidXMLChars(t *testing.T) {
	t.Parallel()
	got, err := MarshalResults(demoFeed(), []*normalizer.Release{minimalBadCharRelease()}, fixedNow())
	if err != nil {
		t.Fatalf("MarshalResults: %v", err)
	}
	s := string(got)
	if strings.ContainsRune(s, 0x1A) {
		t.Error("results contain the raw 0x1A control char")
	}
	if !strings.Contains(s, "<title>MinimalRelease</title>") {
		t.Errorf("title not sanitized as expected:\n%s", s)
	}
}

// TestResultsEmptyFeedHasChannel confirms a no-results feed is a valid feed with
// a full <channel> header and zero items, not a bare/empty document.
func TestResultsEmptyFeedHasChannel(t *testing.T) {
	t.Parallel()
	got, err := MarshalResults(demoFeed(), nil, fixedNow())
	if err != nil {
		t.Fatalf("MarshalResults: %v", err)
	}
	s := string(got)
	for _, want := range []string{"<channel>", "<title>Demo Tracker</title>", "<language>en-US</language>"} {
		if !strings.Contains(s, want) {
			t.Errorf("empty feed missing %q", want)
		}
	}
	if strings.Contains(s, "<item>") {
		t.Error("empty feed should contain no <item>")
	}
	// Empty result still reports an honest, spec-correct paging element.
	if !strings.Contains(s, `<newznab:response offset="0" total="0">`) {
		t.Errorf("empty feed missing <newznab:response offset=0 total=0>:\n%s", s)
	}
}

// TestResultsNewznabResponse pins the spec-correct <newznab:response offset total>
// paging element harbrr emits (a superiority divergence — Jackett's ResultPage omits
// it). offset is this page's resolved offset; total is the full match count BEFORE the
// page slice, so it can exceed the number of items rendered. The newznab namespace must
// be declared on the feed for the prefixed element to be well-formed.
func TestResultsNewznabResponse(t *testing.T) {
	t.Parallel()
	// A 2-item page at offset 50 of a 137-result match set.
	got, err := MarshalResultsRewritten(demoFeed(),
		[]*normalizer.Release{fullRelease(), mediaRelease()},
		Page{Offset: 50, Total: 137}, fixedNow(), nil)
	if err != nil {
		t.Fatalf("MarshalResultsRewritten: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, `xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"`) {
		t.Errorf("newznab namespace not declared:\n%s", s)
	}
	if !strings.Contains(s, `<newznab:response offset="50" total="137">`) {
		t.Errorf("missing <newznab:response offset=50 total=137>:\n%s", s)
	}
	// total reflects the full match set, not the 2 items on this page.
	if strings.Count(s, "<item>") != 2 {
		t.Errorf("item count = %d, want 2 (the page), with total=137 in the response element", strings.Count(s, "<item>"))
	}
}

// TestRewrittenGuidPrefersUpstream pins the /dl-proxy guid precedence in buildItem: the
// rewriter ALWAYS seals the credential-bearing link, but its synthesized passkey-free
// guid is used only when the release carries no upstream id. A release WITH an upstream
// GUID keeps that stable id as <guid> (churn-immunity); one WITHOUT falls back to the
// rewriter's synthesized guid. The original (passkey-bearing) link never reaches the feed
// either way.
func TestRewrittenGuidPrefersUpstream(t *testing.T) {
	t.Parallel()
	// A rewriter that seals every link behind /dl and offers a synthesized guid.
	rewrite := func(string) (link, guid string, ok bool) {
		return "https://harbrr.test/dl/sealed", "harbrr-synth", true
	}
	withGUID := &normalizer.Release{Title: "A", Link: "https://idx.test/get?id=1&apikey=SECRET", GUID: "upstream-id-1", Size: 1}
	noGUID := &normalizer.Release{Title: "B", Link: "https://idx.test/get?id=2&apikey=SECRET", Size: 1}

	got, err := MarshalResultsRewritten(demoFeed(),
		[]*normalizer.Release{withGUID, noGUID}, Page{Offset: 0, Total: 2}, fixedNow(), rewrite)
	if err != nil {
		t.Fatalf("MarshalResultsRewritten: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "<guid>upstream-id-1</guid>") {
		t.Errorf("upstream guid not preferred as <guid>:\n%s", s)
	}
	if !strings.Contains(s, "<guid>harbrr-synth</guid>") {
		t.Errorf("release without upstream guid should fall back to the synthesized guid:\n%s", s)
	}
	if strings.Contains(s, "apikey=SECRET") {
		t.Errorf("passkey-bearing link leaked into the feed:\n%s", s)
	}
}

// TestSanitizeXMLText pins the precise strip set: control chars, BOM and the
// U+FFFE/U+FFFF non-characters and invalid UTF-8 bytes are removed; tab/newline/
// CR and a genuine (3-byte) U+FFFD REPLACEMENT CHARACTER are preserved (Jackett's
// regex strips lone surrogates, not a well-formed U+FFFD).
func TestSanitizeXMLText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"control char", "Bad\x1aChar", "BadChar"},
		{"tab/newline/cr preserved", "a\tb\nc\rd", "a\tb\nc\rd"},
		{"bom stripped", "\uFEFFhello", "hello"},
		{"genuine U+FFFD preserved", "a\uFFFDb", "a\uFFFDb"},
		{"invalid byte stripped", "a\x80b", "ab"},
		{"noncharacter stripped", "a\uFFFFb", "ab"},
		{"clean string untouched", "Normal Title 2024", "Normal Title 2024"},
		{"astral preserved", "emoji \U0001F600 ok", "emoji \U0001F600 ok"},
	}
	for _, tt := range tests {
		if got := sanitizeXMLText(tt.in); got != tt.want {
			t.Errorf("%s: sanitizeXMLText(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

// TestResultsFutureDateClamp confirms a release dated after now is clamped to now
// in pubDate (Jackett's FixResults future-date clamp).
func TestResultsFutureDateClamp(t *testing.T) {
	t.Parallel()
	future := &normalizer.Release{
		Title: "Future Dated", Link: "https://demo.test/f.torrent", Size: 1,
		Categories: []int{2000}, Seeders: 1, Peers: 1,
		PublishDate:          "2099-01-01T00:00:00Z",
		DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
	}
	got, err := MarshalResults(demoFeed(), []*normalizer.Release{future}, fixedNow())
	if err != nil {
		t.Fatalf("MarshalResults: %v", err)
	}
	wantPub := "<pubDate>" + fixedNow().Format(time.RFC1123Z) + "</pubDate>"
	if !strings.Contains(string(got), wantPub) {
		t.Errorf("future pubDate not clamped to now; want %q in:\n%s", wantPub, got)
	}
}

// TestResultsGenreWireForm confirms the genre attr uses the ", " (comma+space)
// wire join Jackett's ResultPage emits, not harbrr's internal "," form.
func TestResultsGenreWireForm(t *testing.T) {
	t.Parallel()
	r := &normalizer.Release{
		Title: "G", Link: "https://demo.test/g.torrent", Size: 1,
		Categories: []int{2000}, Seeders: 1, Peers: 1, Genre: "Drama,Crime,Thriller",
		DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
	}
	got, err := MarshalResults(demoFeed(), []*normalizer.Release{r}, fixedNow())
	if err != nil {
		t.Fatalf("MarshalResults: %v", err)
	}
	if !strings.Contains(string(got), `name="genre" value="Drama, Crime, Thriller"`) {
		t.Errorf("genre not joined with comma+space:\n%s", got)
	}
}

// TestResultsNamespaceBinding parses the feed with the REAL Atom/Torznab
// namespace URIs (the way Sonarr/Radarr's XML reader binds attrs — by namespace
// URI, not literal prefix) and confirms the atom:link and torznab:attr elements
// bind correctly. This is the surest check that *arr will parse the feed.
func TestResultsNamespaceBinding(t *testing.T) {
	t.Parallel()
	got, err := MarshalResults(demoFeed(), []*normalizer.Release{fullRelease()}, fixedNow())
	if err != nil {
		t.Fatalf("MarshalResults: %v", err)
	}
	type nsAttr struct {
		Name  string `xml:"name,attr"`
		Value string `xml:"value,attr"`
	}
	type nsItem struct {
		Attrs []nsAttr `xml:"http://torznab.com/schemas/2015/feed attr"`
	}
	type nsLink struct {
		Href string `xml:"href,attr"`
	}
	type nsChannel struct {
		AtomLink nsLink   `xml:"http://www.w3.org/2005/Atom link"`
		Items    []nsItem `xml:"item"`
	}
	var feed struct {
		Channel nsChannel `xml:"channel"`
	}
	if err := xml.Unmarshal(got, &feed); err != nil {
		t.Fatalf("namespace-aware unmarshal failed (a real *arr would mis-parse): %v", err)
	}
	if feed.Channel.AtomLink.Href == "" {
		t.Error("atom:link did not bind to the Atom namespace")
	}
	if len(feed.Channel.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(feed.Channel.Items))
	}
	found := map[string]string{}
	for _, a := range feed.Channel.Items[0].Attrs {
		found[a.Name] = a.Value
	}
	for _, name := range []string{"category", "seeders", "peers", "imdbid", "downloadvolumefactor"} {
		if _, ok := found[name]; !ok {
			t.Errorf("torznab:attr %q did not bind to the torznab namespace; bound attrs: %v", name, found)
		}
	}
	if found["seeders"] != "12" {
		t.Errorf("seeders torznab:attr = %q, want 12", found["seeders"])
	}
}

// TestResultsPrivateIndexer confirms a private-indexer release (a download link
// with an info hash but no public magnet) emits <type>private</type> and an
// infohash attr, and does NOT emit a magneturl attr.
func TestResultsPrivateIndexer(t *testing.T) {
	t.Parallel()
	feed := demoFeed()
	feed.Type = "private"
	r := &normalizer.Release{
		Title: "Private Release", Link: "https://private.test/dl/9.torrent", Size: 100,
		InfoHash:   "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
		Categories: []int{5040}, Seeders: 9, Peers: 9,
		DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
	}
	got, err := MarshalResults(feed, []*normalizer.Release{r}, fixedNow())
	if err != nil {
		t.Fatalf("MarshalResults: %v", err)
	}
	s := string(got)
	for _, want := range []string{"<type>private</type>", `name="infohash" value="ABCDEF0123456789ABCDEF0123456789ABCDEF01"`} {
		if !strings.Contains(s, want) {
			t.Errorf("private feed missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, `name="magneturl"`) {
		t.Errorf("private release should not emit a magneturl attr:\n%s", s)
	}
}

// TestResultsUsenetProtocol pins the two serializer gates for a usenet feed:
// (A) the <enclosure> advertises application/x-nzb instead of the torrent
// application/x-bittorrent, and (B) the torrent-only torznab:attrs
// (seeders/peers/downloadvolumefactor/uploadvolumefactor) are suppressed, while
// the protocol-agnostic category/external-id/media attrs remain. The same
// release rendered as a torrent feed keeps the torrent enclosure and attrs,
// proving torrent behavior is unchanged.
func TestResultsUsenetProtocol(t *testing.T) {
	t.Parallel()
	// A release carrying torrent-shaped stats; for usenet these must be dropped.
	rel := func() *normalizer.Release {
		return &normalizer.Release{
			Title:                "Usenet.Release.2024.1080p",
			Link:                 "https://demo.test/getnzb/42.nzb",
			Size:                 1073741824,
			Categories:           []int{2040},
			Seeders:              7,
			Peers:                9,
			Grabs:                3,
			IMDBID:               "tt0903747",
			Year:                 2024,
			DownloadVolumeFactor: 1,
			UploadVolumeFactor:   1,
		}
	}

	suppressed := []string{
		`name="seeders"`,
		`name="peers"`,
		`name="downloadvolumefactor"`,
		`name="uploadvolumefactor"`,
	}
	kept := []string{
		`name="category" value="2040"`,
		`name="imdbid" value="tt0903747"`,
		`name="year" value="2024"`,
	}

	tests := []struct {
		name        string
		protocol    string
		wantEncType string
		wantStats   bool // whether the torrent stat/factor attrs are present
	}{
		{name: "usenet", protocol: "usenet", wantEncType: "application/x-nzb", wantStats: false},
		{name: "torrent default", protocol: "", wantEncType: "application/x-bittorrent", wantStats: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			feed := demoFeed()
			feed.Protocol = tt.protocol
			got, err := MarshalResults(feed, []*normalizer.Release{rel()}, fixedNow())
			if err != nil {
				t.Fatalf("MarshalResults: %v", err)
			}
			s := string(got)
			assertWellFormed(t, got)

			wantEnc := `type="` + tt.wantEncType + `"`
			if !strings.Contains(s, wantEnc) {
				t.Errorf("%s: enclosure missing %q in:\n%s", tt.name, wantEnc, s)
			}

			for _, want := range kept {
				if !strings.Contains(s, want) {
					t.Errorf("%s: missing protocol-agnostic attr %q in:\n%s", tt.name, want, s)
				}
			}

			for _, attr := range suppressed {
				present := strings.Contains(s, attr)
				if present != tt.wantStats {
					t.Errorf("%s: attr %q present=%v, want %v in:\n%s", tt.name, attr, present, tt.wantStats, s)
				}
			}
		})
	}
}

// assertWellFormed confirms the bytes parse as XML (no malformed output).
func assertWellFormed(t *testing.T, b []byte) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(string(b)))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				return
			}
			t.Fatalf("not well-formed XML: %v", err)
		}
	}
}
