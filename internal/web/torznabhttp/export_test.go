package torznabhttp

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/core"
)

// TestDLBaseURL builds the externally-visible /dl base, honoring X-Forwarded-Proto
// only from a trusted proxy peer, preferring external_url when set, and escaping the
// indexer id.
func TestDLBaseURL(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://h.test/api/indexers/demo/search", nil)
	r.Host = "h.test"
	if got, want := DLBaseURL(r, URLConfig{BasePath: "/harbrr"}, "demo"), "http://h.test/harbrr/api/indexers/demo/dl"; got != want {
		t.Errorf("DLBaseURL = %q, want %q", got, want)
	}
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := DLBaseURL(r, URLConfig{}, "de mo"); got != "http://h.test/api/indexers/de%20mo/dl" {
		t.Errorf("DLBaseURL (untrusted peer ignores X-Forwarded-Proto) = %q", got)
	}
	trusted := URLConfig{TrustedProxies: func(net.IP) bool { return true }}
	if got := DLBaseURL(r, trusted, "de mo"); got != "https://h.test/api/indexers/de%20mo/dl" {
		t.Errorf("DLBaseURL (https/escaped, trusted proxy) = %q", got)
	}
	extURL := URLConfig{ExternalOrigin: "https://ext.example.com", BasePath: "/harbrr"}
	if got, want := DLBaseURL(r, extURL, "demo"), "https://ext.example.com/harbrr/api/indexers/demo/dl"; got != want {
		t.Errorf("DLBaseURL (external_url) = %q, want %q", got, want)
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
	const title = "ReleaseTitleSentinel"
	link, guid, ok := rw(raw, title, []int{2000})
	if !ok {
		t.Fatal("expected the link to be rewritten")
	}
	if strings.Contains(link, "SECRETPASSKEY123") || strings.Contains(link, title) {
		t.Fatalf("secret or title leaked into the /dl link: %q", link)
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
	payload, err := decodeDLToken(kr, "demo", u.Query().Get("token"))
	if err != nil {
		t.Fatalf("decodeDLToken: %v", err)
	}
	if payload.name != title {
		t.Errorf("token name = %q, want %q", payload.name, title)
	}
	if payload.categoryID != 2000 {
		t.Errorf("token category = %d, want 2000", payload.categoryID)
	}
	if payload.link != raw {
		t.Error("token round-trip differs from the input (values withheld: link-shaped)")
	}
	if _, _, ok := rw("magnet:?xt=urn:btih:abc", title, nil); ok {
		t.Error("expected a magnet to be served as-is (ok=false)")
	}
}

func TestNewManagementDLRewriterSealsTitle(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	idx := &fakeIndexer{info: core.IndexerInfo{ID: "demo"}, needsResolver: true}
	rw := NewManagementDLRewriter(kr, idx, "http://h.test/api/indexers/demo/download")
	if rw == nil {
		t.Fatal("expected a rewriter")
	}
	const raw = "https://demo.test/download?passkey=SECRETPASSKEY456" //nolint:gosec // G101: synthetic test passkey
	const title = "ManagementReleaseSentinel"
	link, _, ok := rw(raw, title, []int{2000})
	if !ok {
		t.Fatal("expected the link to be rewritten")
	}
	if strings.Contains(link, "SECRETPASSKEY456") || strings.Contains(link, title) || strings.Contains(link, "apikey=") {
		t.Fatalf("management link exposed sealed metadata: %q", link)
	}
	token := strings.TrimPrefix(link, "http://h.test/api/indexers/demo/download/")
	payload, err := decodeDLToken(kr, "demo", token)
	if err != nil {
		t.Fatalf("decodeDLToken: %v", err)
	}
	if payload.categoryID != 2000 || payload.name != title || payload.link != raw {
		t.Error("management token payload differs from its source metadata (link-shaped value withheld)")
	}
}

func TestSealedDLURLHasEmptyNameMetadata(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	link, err := SealedDLURL(kr, "demo", "http://h.test/api/indexers/demo/dl", "callerkey", "https://demo.test/download/1")
	if err != nil {
		t.Fatalf("SealedDLURL: %v", err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse sealed URL: %v", err)
	}
	payload, err := decodeDLToken(kr, "demo", u.Query().Get("token"))
	if err != nil {
		t.Fatalf("decodeDLToken: %v", err)
	}
	if payload.name != "" {
		t.Errorf("name metadata = %q, want empty", payload.name)
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
	link, _, ok := rw(raw, "Release.Name.2026", nil)
	if !ok || !strings.HasPrefix(link, "http://h.test/api/indexers/demo/dl?") {
		t.Fatalf("expected the login-auth link sealed behind /dl, got ok=%v link=%q", ok, link)
	}

	direct := &fakeIndexer{info: core.IndexerInfo{ID: "demo"}, needsResolver: false, downloadNeedsAuth: false}
	if NewDLRewriter(kr, direct, "http://h/dl", "k") != nil {
		t.Error("expected a nil rewriter for a plain direct-link indexer")
	}
}
