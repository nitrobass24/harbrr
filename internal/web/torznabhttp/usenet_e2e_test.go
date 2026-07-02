package torznabhttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
	"github.com/autobrr/harbrr/internal/indexer/native/newznab"
	"github.com/autobrr/harbrr/internal/secrets"
)

// usenetUpstreamAPIKey is the synthetic apikey the stub Newznab server requires and
// embeds in its .nzb enclosure URLs — exactly as a real Newznab indexer does. The
// whole point of this leaf is to prove this value is used server-side (to fetch caps,
// search, and the .nzb) yet NEVER appears in the Torznab feed harbrr serves to *arr.
// Built by concatenation so secret scanners do not flag the literal.
const usenetUpstreamAPIKey = "abcd" + "1234" + "deadbeef" + "5678" + "cafef00d"

// usenetCapsXML is the canned ?t=caps document the stub serves. It advertises the
// search + movie-search modes the e2e search exercises, plus a small category tree.
const usenetCapsXML = `<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <server version="1.1" title="Stub News" url="https://stub.news.test/"/>
  <limits max="100" default="100"/>
  <searching>
    <search available="yes" supportedParams="q"/>
    <tv-search available="yes" supportedParams="q,season,ep,tvdbid"/>
    <movie-search available="yes" supportedParams="q,imdbid"/>
    <music-search available="no" supportedParams="q"/>
    <book-search available="no" supportedParams="q"/>
  </searching>
  <categories>
    <category id="2000" name="Movies">
      <subcat id="2040" name="HD"/>
    </category>
    <category id="5000" name="TV"/>
  </categories>
</caps>`

// usenetSearchXMLTemplate is the canned ?t=search response. {{BASE}} is replaced with
// the stub server's real base URL and {{KEY}} with the upstream apikey, reproducing a
// real Newznab feed whose download URLs point back at the indexer and are apikey-
// bearing secrets that harbrr must proxy, never expose.
const usenetSearchXMLTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
  <channel>
    <title>Stub Newznab</title>
    <description>stub results</description>
    <item>
      <title>Example.Movie.2023.1080p.WEB-DL</title>
      <guid isPermaLink="false">abc123</guid>
      <link>{{BASE}}/getnzb/abc123.nzb?i=1&amp;r={{KEY}}</link>
      <pubDate>Mon, 02 Jan 2023 15:04:05 +0000</pubDate>
      <enclosure url="{{BASE}}/getnzb/abc123.nzb?i=1&amp;r={{KEY}}" length="1500000000" type="application/x-nzb" />
      <newznab:attr name="category" value="2000" />
      <newznab:attr name="size" value="1500000000" />
    </item>
    <item>
      <title>Example.Movie.2.2024.2160p.WEB-DL</title>
      <guid isPermaLink="false">def456</guid>
      <link>{{BASE}}/getnzb/def456.nzb?r={{KEY}}</link>
      <pubDate>Tue, 03 Jan 2023 12:00:00 +0000</pubDate>
      <enclosure url="{{BASE}}/getnzb/def456.nzb?r={{KEY}}" length="3000000000" type="application/x-nzb" />
      <newznab:attr name="category" value="2000" />
      <newznab:attr name="size" value="3000000000" />
    </item>
  </channel>
</rss>`

// usenetNZBBody is the canned .nzb the stub serves for a getnzb request (the body the
// /dl proxy must stream back unchanged with content-type application/x-nzb).
const usenetNZBBody = `<?xml version="1.0" encoding="iso-8859-1" ?>
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
  <file poster="uploader@stub.test" date="1672660800" subject="Example.Movie.2023 yEnc (1/1)">
    <segments><segment bytes="100" number="1">seg1@stub.test</segment></segments>
  </file>
</nzb>`

// usenetStub is the offline upstream Newznab server plus a record of whether the
// apikey-gated getnzb (download) endpoint was actually hit server-side.
type usenetStub struct {
	server      *httptest.Server
	gotDownload bool // a getnzb (.nzb) request with the right apikey reached the stub
}

// newUsenetStub stands up an httptest server that answers the three Newznab endpoints
// this leaf exercises — ?t=caps, ?t=search, and the apikey-bearing getnzb .nzb
// download — and rejects any request missing the upstream apikey (so the test proves
// the apikey is genuinely used server-side, not merely accepted).
func newUsenetStub(t *testing.T) *usenetStub {
	t.Helper()
	stub := &usenetStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if strings.Contains(r.URL.Path, "/getnzb/") {
			if q.Get("r") != usenetUpstreamAPIKey {
				http.Error(w, "missing apikey", http.StatusUnauthorized)
				return
			}
			stub.gotDownload = true
			w.Header().Set("Content-Type", "application/x-nzb")
			_, _ = io.WriteString(w, usenetNZBBody)
			return
		}
		if q.Get("apikey") != usenetUpstreamAPIKey {
			http.Error(w, "missing apikey", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		switch q.Get("t") {
		case "caps":
			_, _ = io.WriteString(w, usenetCapsXML)
		default:
			_, _ = io.WriteString(w, stub.searchXML())
		}
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

// searchXML interpolates the stub's own base URL and the upstream apikey into the
// canned search feed, the same way a real Newznab server bakes both into every nzb URL
// (the getnzb URLs point back at the indexer and carry the caller's apikey).
func (s *usenetStub) searchXML() string {
	x := strings.ReplaceAll(usenetSearchXMLTemplate, "{{BASE}}", s.server.URL)
	return strings.ReplaceAll(x, "{{KEY}}", usenetUpstreamAPIKey)
}

// usenetIndexer adapts a real native.Driver (the generic Newznab driver) to the
// handler's Indexer interface for the offline end-to-end proof: the four engine-shaped
// methods come straight from the driver, and Info() carries Protocol=usenet so the
// serializer renders an NZB enclosure and suppresses the torrent stat/factor attrs.
type usenetIndexer struct {
	info   IndexerInfo
	driver native.Driver
}

func (u *usenetIndexer) Info() IndexerInfo                  { return u.info }
func (u *usenetIndexer) Capabilities() *mapper.Capabilities { return u.driver.Capabilities() }

func (u *usenetIndexer) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	return u.driver.Search(ctx, q)
}

func (u *usenetIndexer) NeedsResolver() bool     { return u.driver.NeedsResolver() }
func (u *usenetIndexer) DownloadNeedsAuth() bool { return u.driver.DownloadNeedsAuth() }

func (u *usenetIndexer) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	return u.driver.Grab(ctx, link)
}

// newUsenetE2EHandler wires the full offline chain: the real generic Newznab driver
// (newznab.New) pointed at the stub via the real Params path, adapted to a torznab
// Indexer (Protocol=usenet), served by the real torznab handler with the /dl download
// proxy enabled (WithDLToken) so apikey-bearing .nzb links are routed through /dl.
func newUsenetE2EHandler(t *testing.T, stub *usenetStub) http.Handler {
	t.Helper()
	def := newznab.GenericDefinition()
	drv, err := newznab.New(native.Params{
		Def:     def,
		Cfg:     map[string]string{"apikey": usenetUpstreamAPIKey, "apiPath": "/api"},
		Doer:    stub.server.Client(),
		BaseURL: stub.server.URL,
		Clock:   func() time.Time { return time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("newznab.New: %v", err)
	}
	idx := &usenetIndexer{
		info: IndexerInfo{
			ID: "usenetdemo", Name: def.Name, Description: def.Description,
			SiteLink: stub.server.URL, Type: def.Type, Protocol: def.EffectiveProtocol(),
		},
		driver: drv,
	}
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: dlTestKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return NewHandler(
		fakeProvider{"usenetdemo": idx},
		WithAPIKey(testAPIKey),
		WithClock(func() time.Time { return time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC) }),
		WithDLToken(kr),
	)
}

// usenetGet drives the handler at the usenet indexer's torznab/dl routes and returns
// the response recorder. The CALLER's apikey is harbrr's own testAPIKey, never the
// upstream Newznab apikey — proving the two key spaces are separate.
func usenetGet(t *testing.T, h http.Handler, path, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	url := "http://harbrr.test/api/indexers/usenetdemo/" + path + "?" + rawQuery + "&apikey=" + testAPIKey
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestUsenetEndToEndOffline is Leaf 9: the full offline HTTP end-to-end proof for a
// usenet (Newznab) indexer. It ties the generic Newznab driver (caps fetch, search
// parse, server-side .nzb grab) to harbrr's actual served Torznab + /dl HTTP layer
// against a stub upstream, with NO live network, and asserts the usenet rendering and
// the apikey-redaction guarantee end to end.
func TestUsenetEndToEndOffline(t *testing.T) {
	t.Parallel()
	stub := newUsenetStub(t)
	h := newUsenetE2EHandler(t, stub)

	// 1) t=caps flows through the handler over a live ?t=caps fetch from the stub.
	caps := usenetGet(t, h, "results/torznab", "t=caps")
	if caps.Code != http.StatusOK {
		t.Fatalf("caps status = %d, want 200; body:\n%s", caps.Code, caps.Body.String())
	}
	if ct := caps.Header().Get("Content-Type"); ct != contentTypeFeed {
		t.Errorf("caps content-type = %q, want %q", ct, contentTypeFeed)
	}
	if !strings.Contains(caps.Body.String(), `<movie-search available="yes"`) {
		t.Errorf("caps did not reflect the stub's advertised modes:\n%s", caps.Body.String())
	}

	// 2) t=search flows through the handler over a live ?t=search fetch from the stub.
	results := usenetGet(t, h, "results/torznab", "t=search&q=example")
	if results.Code != http.StatusOK {
		t.Fatalf("search status = %d, want 200; body:\n%s", results.Code, results.Body.String())
	}
	feed := results.Body.String()

	dlLink := assertUsenetFeed(t, feed)

	// 3) GET the proxied /dl link: the .nzb is fetched server-side (with the upstream
	// apikey) and streamed back as application/x-nzb. The caller never saw the apikey.
	assertUsenetDownload(t, h, stub, dlLink)
}

// assertUsenetFeed makes the served-feed assertions and returns the proxied /dl link
// (relative path + query) extracted from the first item's enclosure.
func assertUsenetFeed(t *testing.T, feed string) string {
	t.Helper()

	// Results present.
	if n := strings.Count(feed, "<item>"); n != 2 {
		t.Fatalf("item count = %d, want 2:\n%s", n, feed)
	}

	// Each enclosure is an NZB enclosure (usenet rendering), never a torrent.
	if n := strings.Count(feed, `type="application/x-nzb"`); n != 2 {
		t.Errorf("nzb enclosure count = %d, want 2:\n%s", n, feed)
	}
	if strings.Contains(feed, "application/x-bittorrent") {
		t.Errorf("usenet feed must not emit a torrent enclosure:\n%s", feed)
	}

	// No torrent stat/factor torznab:attrs on a usenet feed.
	for _, attr := range []string{"seeders", "peers", "downloadvolumefactor", "uploadvolumefactor"} {
		if strings.Contains(feed, `name="`+attr+`"`) {
			t.Errorf("usenet feed leaked a torrent attr %q:\n%s", attr, feed)
		}
	}

	// The upstream apikey NEVER appears in the served feed — neither in <link>,
	// <enclosure url>, nor anywhere else. The served link is a /dl proxy URL.
	if strings.Contains(feed, usenetUpstreamAPIKey) {
		t.Errorf("upstream apikey LEAKED into the served feed:\n%s", feed)
	}
	if !strings.Contains(feed, "/api/indexers/usenetdemo/dl?") || !strings.Contains(feed, "token=") {
		t.Errorf("apikey-bearing nzb links should route through the /dl proxy with a token:\n%s", feed)
	}

	return extractDLLink(t, feed)
}

// extractDLLink pulls the first <enclosure url="..."> /dl path+query out of the served
// feed and returns it as "path?query" (XML-unescaped), so the test can drive it back
// through the same handler.
func extractDLLink(t *testing.T, feed string) string {
	t.Helper()
	const marker = `url="`
	encStart := strings.Index(feed, "<enclosure ")
	if encStart < 0 {
		t.Fatalf("no <enclosure> in feed:\n%s", feed)
	}
	urlStart := strings.Index(feed[encStart:], marker)
	if urlStart < 0 {
		t.Fatalf("enclosure has no url attr:\n%s", feed)
	}
	rest := feed[encStart+urlStart+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		t.Fatalf("unterminated enclosure url:\n%s", feed)
	}
	raw := rest[:end]
	// The serializer XML-escapes "&" in the query to "&amp;"; undo it for the request.
	return strings.ReplaceAll(raw, "&amp;", "&")
}

// assertUsenetDownload drives the proxied /dl link back through the handler and asserts
// the .nzb body is served as application/x-nzb, that the upstream apikey was used
// server-side (the stub's apikey-gated getnzb endpoint was hit) yet never reached the
// caller, and that the served body is the canned nzb.
func assertUsenetDownload(t *testing.T, h http.Handler, stub *usenetStub, dlLink string) {
	t.Helper()
	// dlLink is the absolute /dl proxy URL from the feed, already carrying the
	// caller's apikey + opaque token; drive it back through the same handler verbatim.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, dlLink, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/dl status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-nzb" {
		t.Errorf("/dl content-type = %q, want application/x-nzb", ct)
	}
	if rec.Body.String() != usenetNZBBody {
		t.Errorf("/dl body = %q, want the canned nzb", rec.Body.String())
	}
	if !stub.gotDownload {
		t.Errorf("the upstream apikey-gated getnzb endpoint was never hit server-side")
	}
	if strings.Contains(rec.Body.String(), usenetUpstreamAPIKey) {
		t.Errorf("upstream apikey leaked into the served .nzb body")
	}
	// The served link is harbrr's /dl proxy, never the upstream getnzb URL.
	if strings.Contains(dlLink, "getnzb") {
		t.Errorf("served link should be harbrr's /dl proxy, not the upstream getnzb path: %q", dlLink)
	}
}
