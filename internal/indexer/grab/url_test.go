package grab

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
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

func TestSealedDLURLNameMetadata(t *testing.T) {
	t.Parallel()
	kr := encryptedKeyring(t)
	tests := []struct {
		name     string
		title    string
		wantName string
	}{
		// The announce path passes the release name it has in hand, so the
		// eventual grab is named after the release, not the indexer.
		{name: "release title seals its stem", title: "Release.Name.2026", wantName: "Release.Name.2026"},
		{name: "titleless caller stays empty for the fallback", title: "", wantName: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			link, err := SealedDLURL(kr, "demo", "http://h.test/api/indexers/demo/dl", "callerkey", tt.title, "https://demo.test/download/1")
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
			if payload.Name != tt.wantName {
				t.Errorf("name metadata = %q, want %q", payload.Name, tt.wantName)
			}
		})
	}
}
