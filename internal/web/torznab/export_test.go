package torznab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

// TestSearchReleasesPipeline proves the exported SearchReleases runs the shared
// read pipeline (query mapping + dedupe-by-guid + paging), so the management API's
// JSON search and the Torznab feed return the same processed result set.
func TestSearchReleasesPipeline(t *testing.T) {
	t.Parallel()
	idx := &fakeIndexer{
		info: IndexerInfo{ID: "demo"},
		caps: testCaps(t),
		releases: []*normalizer.Release{
			{Title: "A", Link: "https://demo.test/a"},
			{Title: "A", Link: "https://demo.test/a"}, // duplicate guid -> deduped
			{Title: "B", Link: "https://demo.test/b"},
		},
	}
	got, err := SearchReleases(context.Background(), idx, url.Values{"q": {"x"}})
	if err != nil {
		t.Fatalf("SearchReleases: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 releases after dedupe, got %d", len(got))
	}
	if idx.gotQuery.Keywords != "x" {
		t.Errorf("query mapping did not reach the engine: keywords=%q", idx.gotQuery.Keywords)
	}
}

// TestSearchReleasesPropagatesError surfaces a search failure to the caller (so the
// JSON handler can classify it), matching the feed.
func TestSearchReleasesPropagatesError(t *testing.T) {
	t.Parallel()
	idx := &fakeIndexer{info: IndexerInfo{ID: "demo"}, caps: testCaps(t), searchErr: context.DeadlineExceeded}
	if _, err := SearchReleases(context.Background(), idx, url.Values{}); err == nil {
		t.Fatal("expected the search error to propagate")
	}
}

// TestDLBaseURL builds the externally-visible /dl base, honoring X-Forwarded-Proto
// and the configured base path, and escaping the indexer id.
func TestDLBaseURL(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://h.test/api/indexers/demo/search", nil)
	r.Host = "h.test"
	if got, want := DLBaseURL(r, "/harbrr", "demo"), "http://h.test/harbrr/api/v2.0/indexers/demo/dl"; got != want {
		t.Errorf("DLBaseURL = %q, want %q", got, want)
	}
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := DLBaseURL(r, "", "de mo"); got != "https://h.test/api/v2.0/indexers/de%20mo/dl" {
		t.Errorf("DLBaseURL (https/escaped) = %q", got)
	}
}

// TestNewDLRewriterDisabled returns nil when the proxy is off or the indexer needs
// no resolution — the caller then serves the raw link.
func TestNewDLRewriterDisabled(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	direct := &fakeIndexer{info: IndexerInfo{ID: "demo"}, needsResolver: false}
	if NewDLRewriter(kr, direct, "http://h/dl", "k") != nil {
		t.Error("expected a nil rewriter for a direct-link indexer")
	}
	resolver := &fakeIndexer{info: IndexerInfo{ID: "demo"}, needsResolver: true}
	if NewDLRewriter(nil, resolver, "http://h/dl", "k") != nil {
		t.Error("expected a nil rewriter when the keyring is nil")
	}
}

// TestNewDLRewriterSealsLink proves a resolver-needing indexer's passkey-bearing
// link is replaced with an opaque /dl URL (passkey absent), a magnet is left as-is,
// and the token round-trips back to the original link under the same indexer id.
func TestNewDLRewriterSealsLink(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	idx := &fakeIndexer{info: IndexerInfo{ID: "demo"}, needsResolver: true}
	rw := NewDLRewriter(kr, idx, "http://h.test/api/v2.0/indexers/demo/dl", "callerkey")
	if rw == nil {
		t.Fatal("expected a rewriter")
	}
	const raw = "https://demo.test/download?passkey=SECRETPASSKEY123" //nolint:gosec // G101: synthetic test passkey
	link, guid, ok := rw(raw)
	if !ok {
		t.Fatal("expected the link to be rewritten")
	}
	if strings.Contains(link, "SECRETPASSKEY123") {
		t.Fatalf("passkey leaked into the /dl link: %q", link)
	}
	if !strings.HasPrefix(link, "http://h.test/api/v2.0/indexers/demo/dl?") {
		t.Errorf("unexpected /dl base: %q", link)
	}
	if !strings.HasPrefix(guid, "harbrr-") {
		t.Errorf("expected a stable harbrr- guid, got %q", guid)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse /dl link: %v", err)
	}
	back, err := decodeDLToken(kr, "demo", u.Query().Get("token"))
	if err != nil {
		t.Fatalf("decodeDLToken: %v", err)
	}
	if back != raw {
		t.Errorf("token round-trip = %q, want %q", back, raw)
	}
	if _, _, ok := rw("magnet:?xt=urn:btih:abc"); ok {
		t.Error("expected a magnet to be served as-is (ok=false)")
	}
}
