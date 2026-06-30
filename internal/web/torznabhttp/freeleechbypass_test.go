package torznabhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFeedURL covers the externally-visible feed URL builder: scheme derivation, the
// base path, and the /full bypass suffix.
func TestFeedURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		basePath string
		bypass   bool
		fwdProto string
		want     string
	}{
		{"honor", "", false, "", "http://h.test/api/v2.0/indexers/tt/results/torznab"},
		{"bypass appends /full", "", true, "", "http://h.test/api/v2.0/indexers/tt/results/torznab/full"},
		{"base path", "/harbrr", false, "", "http://h.test/harbrr/api/v2.0/indexers/tt/results/torznab"},
		{"forwarded https", "", true, "https", "https://h.test/api/v2.0/indexers/tt/results/torznab/full"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://h.test/whatever", nil)
			req.Host = "h.test"
			if tt.fwdProto != "" {
				req.Header.Set("X-Forwarded-Proto", tt.fwdProto)
			}
			if got := FeedURL(req, tt.basePath, "tt", tt.bypass); got != tt.want {
				t.Errorf("FeedURL = %q, want %q", got, tt.want)
			}
		})
	}
}

// doPath issues a GET to an arbitrary feed path (so the bypass-variant routes can be
// exercised), appending the test apikey.
func doPath(t *testing.T, h http.Handler, path, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		path+"?"+rawQuery+"&apikey="+testAPIKey, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestFreeleechBypassRouteSetsQueryFlag proves the /results/torznab/full variant (and
// its /api alias) tags the engine query with FreeleechBypass, while the honor routes
// leave it false — the signal the registry's freeleech view reads to serve the full
// catalog. The handler itself does no filtering; it only routes the flag.
func TestFreeleechBypassRouteSetsQueryFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		wantBypass bool
	}{
		{"honor feed", "/api/v2.0/indexers/demo/results/torznab", false},
		{"honor /api alias", "/api/v2.0/indexers/demo/results/torznab/api", false},
		{"bypass feed", "/api/v2.0/indexers/demo/results/torznab/full", true},
		{"bypass /api alias", "/api/v2.0/indexers/demo/results/torznab/full/api", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			idx := demoIndexer(t)
			rec := doPath(t, newTestHandler(t, idx), tt.path, "t=search&q=movie")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			if idx.gotQuery.FreeleechBypass != tt.wantBypass {
				t.Errorf("FreeleechBypass = %v, want %v", idx.gotQuery.FreeleechBypass, tt.wantBypass)
			}
		})
	}
}
