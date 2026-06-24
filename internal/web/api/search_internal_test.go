package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/web/torznab"
)

// keyLink is a synthetic passkey-bearing download link (test only).
const keyLink = "https://demo.test/dl?passkey=SECRETPASSKEY777" //nolint:gosec // G101: synthetic test passkey

// fakeSearchIndexer is a torznab.Indexer for the link-resolution unit test.
type fakeSearchIndexer struct {
	id                string
	needsResolver     bool
	downloadNeedsAuth bool
}

func (f fakeSearchIndexer) Info() torznab.IndexerInfo          { return torznab.IndexerInfo{ID: f.id} }
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

// TestResolveSearchLinksSealsResolverLink proves a resolver-needing indexer's
// passkey-bearing link is replaced with a /dl proxy URL — the passkey is absent and
// the source release is not mutated (the #1 redaction risk for JSON search).
func TestResolveSearchLinksSealsResolverLink(t *testing.T) {
	t.Parallel()
	rt := &router{dlToken: testKeyring(t)}
	rels := []*normalizer.Release{{Title: "X", Link: keyLink}}
	out := rt.resolveSearchLinks(searchReq(t), fakeSearchIndexer{id: "demo", needsResolver: true}, rels)
	if len(out) != 1 {
		t.Fatalf("got %d releases", len(out))
	}
	if strings.Contains(out[0].Link, "SECRETPASSKEY777") {
		t.Fatalf("passkey leaked into the JSON link: %q", out[0].Link)
	}
	if !strings.Contains(out[0].Link, "/api/v2.0/indexers/demo/dl?") {
		t.Errorf("link not routed through /dl: %q", out[0].Link)
	}
	if rels[0].Link != keyLink {
		t.Error("source release was mutated (expected a copy)")
	}
}

// TestResolveSearchLinksSealsLoginAuthLink: a login-auth indexer with no download
// block (DownloadNeedsAuth=true, NeedsResolver=false) is sealed behind /dl too, so
// JSON search matches the Torznab feed for the cookie/header-auth grab gap.
func TestResolveSearchLinksSealsLoginAuthLink(t *testing.T) {
	t.Parallel()
	rt := &router{dlToken: testKeyring(t)}
	rels := []*normalizer.Release{{Title: "X", Link: keyLink}}
	out := rt.resolveSearchLinks(searchReq(t), fakeSearchIndexer{id: "demo", downloadNeedsAuth: true}, rels)
	if strings.Contains(out[0].Link, "SECRETPASSKEY777") {
		t.Fatalf("passkey leaked into the JSON link: %q", out[0].Link)
	}
	if !strings.Contains(out[0].Link, "/api/v2.0/indexers/demo/dl?") {
		t.Errorf("login-auth link not routed through /dl: %q", out[0].Link)
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
