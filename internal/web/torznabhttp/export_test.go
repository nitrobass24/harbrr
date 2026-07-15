package torznabhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/core"
)

// TestDLBaseURL builds the externally-visible /dl base, honoring X-Forwarded-Proto
// and the configured base path, and escaping the indexer id.
func TestDLBaseURL(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://h.test/api/indexers/demo/search", nil)
	r.Host = "h.test"
	if got, want := DLBaseURL(r, "/harbrr", "demo"), "http://h.test/harbrr/api/indexers/demo/dl"; got != want {
		t.Errorf("DLBaseURL = %q, want %q", got, want)
	}
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := DLBaseURL(r, "", "de mo"); got != "https://h.test/api/indexers/de%20mo/dl" {
		t.Errorf("DLBaseURL (https/escaped) = %q", got)
	}
}

// TestNewDLRewriterDisabled returns nil when the proxy is off or the indexer needs
// no resolution — the caller then serves the raw link.
func TestNewDLRewriterDisabled(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	direct := &fakeIndexer{info: core.IndexerInfo{ID: "demo"}, needsResolver: false}
	if NewDLRewriter(kr, direct, "http://h/dl", "k") != nil {
		t.Error("expected a nil rewriter for a direct-link indexer")
	}
	resolver := &fakeIndexer{info: core.IndexerInfo{ID: "demo"}, needsResolver: true}
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
	idx := &fakeIndexer{info: core.IndexerInfo{ID: "demo"}, needsResolver: true}
	rw := NewDLRewriter(kr, idx, "http://h.test/api/indexers/demo/dl", "callerkey")
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
	if !strings.HasPrefix(link, "http://h.test/api/indexers/demo/dl?") {
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

// TestNewDLRewriterSealsLoginAuthLink proves a login-auth indexer with NO download
// block (NeedsResolver=false, DownloadNeedsAuth=true) still gets its link sealed
// behind /dl — the cookie/header-auth grab gap. A plain direct-link indexer (both
// false) is left bare.
func TestNewDLRewriterSealsLoginAuthLink(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	loginAuth := &fakeIndexer{info: core.IndexerInfo{ID: "demo"}, needsResolver: false, downloadNeedsAuth: true}
	rw := NewDLRewriter(kr, loginAuth, "http://h.test/api/indexers/demo/dl", "callerkey")
	if rw == nil {
		t.Fatal("expected a rewriter for a login-auth indexer")
	}
	const raw = "https://demo.test/download/9/Release.torrent"
	link, _, ok := rw(raw)
	if !ok || !strings.HasPrefix(link, "http://h.test/api/indexers/demo/dl?") {
		t.Fatalf("expected the login-auth link sealed behind /dl, got ok=%v link=%q", ok, link)
	}

	direct := &fakeIndexer{info: core.IndexerInfo{ID: "demo"}, needsResolver: false, downloadNeedsAuth: false}
	if NewDLRewriter(kr, direct, "http://h/dl", "k") != nil {
		t.Error("expected a nil rewriter for a plain direct-link indexer")
	}
}
