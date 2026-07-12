package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// keyLink is a synthetic passkey-bearing download link (test only).
const keyLink = "https://demo.test/dl?passkey=SECRETPASSKEY777" //nolint:gosec // G101: synthetic test passkey

// fakeSearchIndexer is a torznabhttp.Indexer for the link-resolution unit test.
type fakeSearchIndexer struct {
	id                string
	needsResolver     bool
	downloadNeedsAuth bool
}

func (f fakeSearchIndexer) Info() torznabhttp.IndexerInfo      { return torznabhttp.IndexerInfo{ID: f.id} }
func (f fakeSearchIndexer) Capabilities() *mapper.Capabilities { return nil }

func (f fakeSearchIndexer) Search(context.Context, search.Query) ([]*normalizer.Release, error) {
	return nil, nil
}
func (f fakeSearchIndexer) NeedsResolver() bool     { return f.needsResolver }
func (f fakeSearchIndexer) DownloadNeedsAuth() bool { return f.downloadNeedsAuth }

func (f fakeSearchIndexer) Grab(context.Context, string) (*search.GrabResult, error) {
	return &search.GrabResult{}, nil // unused by the link-resolution tests
}

func testKeyring(t *testing.T) *secrets.Keyring {
	t.Helper()
	kr, err := secrets.OpenKeyring(
		secrets.KeyringOptions{EncryptionKey: "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"},
		zerolog.Nop(),
	)
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return kr
}

func searchReq(t *testing.T) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://h.test/api/indexers/demo/search", nil)
}

// TestNewSearchResponse pins the qui-shaped JSON envelope and the HasMore boundaries:
// true while the match set extends past this page, false on the last page, an
// offset==total empty page, and an empty result set. Results passes through unchanged
// (the same page the shared pipeline produced — the feed/JSON parity guarantee).
func TestNewSearchResponse(t *testing.T) {
	t.Parallel()
	rel := func(n string) *normalizer.Release { return &normalizer.Release{Title: n} }
	tests := []struct {
		name        string
		res         torznabhttp.SearchResult
		wantTotal   int
		wantHasMore bool
		wantLimit   int
		wantOffset  int
	}{
		{
			name:      "mid set has more",
			res:       torznabhttp.SearchResult{Releases: []*normalizer.Release{rel("a"), rel("b")}, Total: 10, Offset: 0, Limit: 2},
			wantTotal: 10, wantHasMore: true, wantLimit: 2, wantOffset: 0,
		},
		{
			name:      "last full page no more",
			res:       torznabhttp.SearchResult{Releases: []*normalizer.Release{rel("i"), rel("j")}, Total: 10, Offset: 8, Limit: 2},
			wantTotal: 10, wantHasMore: false, wantLimit: 2, wantOffset: 8,
		},
		{
			name:      "partial last page no more",
			res:       torznabhttp.SearchResult{Releases: []*normalizer.Release{rel("x")}, Total: 5, Offset: 4, Limit: 10},
			wantTotal: 5, wantHasMore: false, wantLimit: 10, wantOffset: 4,
		},
		{
			name:      "offset at total empty no more",
			res:       torznabhttp.SearchResult{Releases: nil, Total: 10, Offset: 10, Limit: 2},
			wantTotal: 10, wantHasMore: false, wantLimit: 2, wantOffset: 10,
		},
		{
			name:      "empty result set",
			res:       torznabhttp.SearchResult{Releases: nil, Total: 0, Offset: 0, Limit: 100},
			wantTotal: 0, wantHasMore: false, wantLimit: 100, wantOffset: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := newSearchResponse(tt.res, tt.res.Releases)
			if got.Total != tt.wantTotal {
				t.Errorf("Total = %d, want %d", got.Total, tt.wantTotal)
			}
			if got.HasMore != tt.wantHasMore {
				t.Errorf("HasMore = %v, want %v", got.HasMore, tt.wantHasMore)
			}
			if got.Limit != tt.wantLimit {
				t.Errorf("Limit = %d, want %d", got.Limit, tt.wantLimit)
			}
			if got.Offset != tt.wantOffset {
				t.Errorf("Offset = %d, want %d", got.Offset, tt.wantOffset)
			}
			if len(got.Results) != len(tt.res.Releases) {
				t.Errorf("Results length = %d, want %d (page passes through unchanged)", len(got.Results), len(tt.res.Releases))
			}
		})
	}

	// Results must be the SECOND argument (the link-resolved page), not res.Releases —
	// the sealed-link copies are what reach the client, while the paging metadata still
	// derives from res. Use a distinct resolved slice so the two cannot be confused.
	t.Run("results come from the resolved arg", func(t *testing.T) {
		t.Parallel()
		res := torznabhttp.SearchResult{Releases: []*normalizer.Release{rel("raw")}, Total: 1, Offset: 0, Limit: 10}
		resolved := []*normalizer.Release{rel("resolved-a"), rel("resolved-b")}
		got := newSearchResponse(res, resolved)
		if len(got.Results) != 2 || got.Results[0].Title != "resolved-a" || got.Results[1].Title != "resolved-b" {
			t.Errorf("Results = %+v, want the resolved slice [resolved-a resolved-b]", got.Results)
		}
		// Paging metadata is unaffected by the resolved slice — it comes from res.
		if got.Total != 1 || got.Offset != 0 || got.Limit != 10 || got.HasMore {
			t.Errorf("paging metadata should derive from res, got %+v", got)
		}
	})
}

// pagedAPIIndexer is a direct-link torznabhttp.Indexer with real caps and synthetic
// releases, for the JSON cross-page test. The JSON search runs the same
// torznabhttp.SearchReleases pipeline the feed does, so this proves feed/JSON paging parity.
type pagedAPIIndexer struct {
	caps     *mapper.Capabilities
	releases []*normalizer.Release
}

func (p *pagedAPIIndexer) Info() torznabhttp.IndexerInfo      { return torznabhttp.IndexerInfo{ID: "demo"} }
func (p *pagedAPIIndexer) Capabilities() *mapper.Capabilities { return p.caps }

func (p *pagedAPIIndexer) Search(context.Context, search.Query) ([]*normalizer.Release, error) {
	return p.releases, nil
}
func (p *pagedAPIIndexer) NeedsResolver() bool     { return false }
func (p *pagedAPIIndexer) DownloadNeedsAuth() bool { return false }

func (p *pagedAPIIndexer) Grab(context.Context, string) (*search.GrabResult, error) {
	return &search.GrabResult{}, nil
}

func pagedAPICaps(t *testing.T) *mapper.Capabilities {
	t.Helper()
	caps, err := mapper.Build(&loader.Definition{
		ID: "demo", Links: []string{"https://demo.test/"},
		Caps: loader.Caps{
			CategoryMappings: []loader.CategoryMapping{
				{ID: loader.Scalar{Value: "1", Set: true}, Cat: "Movies"},
			},
			Modes: loader.Modes{Search: []string{"q"}},
		},
	})
	if err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}
	return caps
}

// TestSearchJSONEnvelopeCrossPage proves the qui-shaped JSON envelope pages a
// >100-result fetch into DISJOINT windows with a stable Total and correct HasMore —
// identical to the feed, since both call torznabhttp.SearchReleases. HasMore is true on
// page 0 (results remain) and false on page 1 (the partial last page), and no release
// appears on both pages — the property Prowlarr violates (#1428: it re-serves page 0).
func TestSearchJSONEnvelopeCrossPage(t *testing.T) {
	t.Parallel()
	const total = 150
	idx := &pagedAPIIndexer{caps: pagedAPICaps(t)}
	idx.releases = make([]*normalizer.Release, total)
	for i := range idx.releases {
		idx.releases[i] = &normalizer.Release{
			Title: fmt.Sprintf("R%03d", i),
			Link:  fmt.Sprintf("https://demo.test/dl/%d", i),
		}
	}

	page := func(offset string) searchResponse {
		t.Helper()
		res, err := torznabhttp.SearchReleases(context.Background(), idx,
			url.Values{"q": {"x"}, "limit": {"100"}, "offset": {offset}})
		if err != nil {
			t.Fatalf("SearchReleases(offset=%s): %v", offset, err)
		}
		return newSearchResponse(res, res.Releases)
	}

	p0, p1 := page("0"), page("100")
	if p0.Total != total || p1.Total != total {
		t.Errorf("Total = %d / %d, want %d on both pages", p0.Total, p1.Total, total)
	}
	if len(p0.Results) != 100 || !p0.HasMore {
		t.Errorf("page 0: results=%d hasMore=%v, want 100 / true", len(p0.Results), p0.HasMore)
	}
	if len(p1.Results) != 50 || p1.HasMore {
		t.Errorf("page 1: results=%d hasMore=%v, want 50 / false", len(p1.Results), p1.HasMore)
	}
	seen := make(map[string]bool, len(p0.Results))
	for _, r := range p0.Results {
		seen[r.Link] = true
	}
	for _, r := range p1.Results {
		if seen[r.Link] {
			t.Errorf("release %q in BOTH JSON pages (Prowlarr #1428 re-serve)", r.Link)
		}
	}
}

// TestResolveSearchLinksSealsResolverLink proves a resolver-needing indexer's
// passkey-bearing link is replaced with the session-authed management download route
// (/api/indexers/{slug}/download/{token}) — the passkey is absent, the source release
// is not mutated (the #1 redaction risk for JSON search), and the link carries a
// NON-EMPTY opaque token but NO apikey. Crucially this holds for a SESSION caller that
// presents no key (the web UI): the previous /dl+apikey sealing minted an unusable
// apikey= empty link for exactly that caller (the #7 Part A bug).
func TestResolveSearchLinksSealsResolverLink(t *testing.T) {
	t.Parallel()
	rt := &router{dlToken: testKeyring(t)}
	idx := fakeSearchIndexer{id: "demo", needsResolver: true}
	const dlBase = "/api/indexers/demo/download/"

	// A session caller presents no key; a programmatic caller may present one. Both must
	// seal an identical, usable management-route link (the key no longer shapes the URL).
	for _, src := range []struct {
		name string
		req  func(t *testing.T) *http.Request
	}{
		{"session (no key)", func(t *testing.T) *http.Request { t.Helper(); return searchReq(t) }},
		{"x-api-key header", func(t *testing.T) *http.Request {
			t.Helper()
			r := searchReq(t)
			r.Header.Set("X-API-Key", "FEEDKEY123")
			return r
		}},
	} {
		t.Run(src.name, func(t *testing.T) {
			t.Parallel()
			rels := []*normalizer.Release{{Title: "X", Link: keyLink}}
			out := rt.resolveSearchLinks(src.req(t), idx, rels)
			if len(out) != 1 {
				t.Fatalf("got %d releases", len(out))
			}
			if strings.Contains(out[0].Link, "SECRETPASSKEY777") {
				t.Fatalf("passkey leaked into the JSON link: %q", out[0].Link)
			}
			i := strings.Index(out[0].Link, dlBase)
			if i < 0 {
				t.Fatalf("link not routed through the management download route: %q", out[0].Link)
			}
			if token := out[0].Link[i+len(dlBase):]; token == "" {
				t.Errorf("sealed link missing the opaque token: %q", out[0].Link)
			}
			if strings.Contains(out[0].Link, "apikey=") {
				t.Errorf("management link must carry no apikey: %q", out[0].Link)
			}
			if rels[0].Link != keyLink {
				t.Error("source release was mutated (expected a copy)")
			}
		})
	}
}

// TestResolveSearchLinksSealsLoginAuthLink: a login-auth indexer with no download
// block (DownloadNeedsAuth=true, NeedsResolver=false) is sealed behind the management
// download route too, so JSON search matches the Torznab feed for the cookie/header-auth
// grab gap.
func TestResolveSearchLinksSealsLoginAuthLink(t *testing.T) {
	t.Parallel()
	rt := &router{dlToken: testKeyring(t)}
	rels := []*normalizer.Release{{Title: "X", Link: keyLink}}
	out := rt.resolveSearchLinks(searchReq(t), fakeSearchIndexer{id: "demo", downloadNeedsAuth: true}, rels)
	if strings.Contains(out[0].Link, "SECRETPASSKEY777") {
		t.Fatalf("passkey leaked into the JSON link: %q", out[0].Link)
	}
	if !strings.Contains(out[0].Link, "/api/indexers/demo/download/") {
		t.Errorf("login-auth link not routed through the management download route: %q", out[0].Link)
	}
}

// TestResolveSearchLinksDirectServedAsIs: a direct-link indexer's link is unchanged
// (direct trackers carry the passkey in the link by design — same as the feed).
func TestResolveSearchLinksDirectServedAsIs(t *testing.T) {
	t.Parallel()
	rt := &router{dlToken: testKeyring(t)}
	rels := []*normalizer.Release{{Title: "X", Link: keyLink}}
	out := rt.resolveSearchLinks(searchReq(t), fakeSearchIndexer{id: "demo", needsResolver: false}, rels)
	if out[0].Link != keyLink {
		t.Errorf("direct link altered: %q", out[0].Link)
	}
}

// TestResolveSearchLinksWithholdsWhenProxyOff: a resolver-needing indexer with no
// keyring withholds the link rather than leak the passkey.
func TestResolveSearchLinksWithholdsWhenProxyOff(t *testing.T) {
	t.Parallel()
	rt := &router{} // dlToken nil -> proxy disabled
	rels := []*normalizer.Release{{Title: "X", Link: keyLink, Magnet: "magnet:?xt=urn:btih:abc"}}
	out := rt.resolveSearchLinks(searchReq(t), fakeSearchIndexer{id: "demo", needsResolver: true}, rels)
	if out[0].Link != "" || out[0].Magnet != "" {
		t.Errorf("expected the link withheld, got link=%q magnet=%q", out[0].Link, out[0].Magnet)
	}
}

// TestResolveSearchLinksMagnetAsIs: a magnet (public) is served unchanged even for a
// resolver-needing indexer.
func TestResolveSearchLinksMagnetAsIs(t *testing.T) {
	t.Parallel()
	rt := &router{dlToken: testKeyring(t)}
	const m = "magnet:?xt=urn:btih:abc"
	rels := []*normalizer.Release{{Title: "X", Magnet: m}}
	out := rt.resolveSearchLinks(searchReq(t), fakeSearchIndexer{id: "demo", needsResolver: true}, rels)
	if out[0].Magnet != m {
		t.Errorf("magnet altered: %q", out[0].Magnet)
	}
}
