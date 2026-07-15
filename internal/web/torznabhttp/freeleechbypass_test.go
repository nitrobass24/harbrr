package torznabhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
		{"honor", "", false, "", "http://h.test/api/indexers/tt/results/torznab"},
		{"bypass appends /full", "", true, "", "http://h.test/api/indexers/tt/results/torznab/full"},
		{"base path", "/harbrr", false, "", "http://h.test/harbrr/api/indexers/tt/results/torznab"},
		{"forwarded https", "", true, "https", "https://h.test/api/indexers/tt/results/torznab/full"},
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
		{"honor feed", "/api/indexers/demo/results/torznab", false},
		{"honor /api alias", "/api/indexers/demo/results/torznab/api", false},
		{"bypass feed", "/api/indexers/demo/results/torznab/full", true},
		{"bypass /api alias", "/api/indexers/demo/results/torznab/full/api", true},
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

// TestFreeleechBypassETagDistinct proves the honor feed and the /full bypass feed —
// which share ONE cached entry — emit DISTINCT conditional-GET validators even when they
// serve byte-identical bodies (the worst case: an all-freeleech page, so the honor view
// equals the full view). Folding the bypass variant into the ETag is what stops a 304 on
// one feed from serving the other variant's body: a cross-variant revalidation must come
// back 200, not 304.
func TestFreeleechBypassETagDistinct(t *testing.T) {
	t.Parallel()

	cachingDemo := func() *fakeIndexer {
		idx := demoIndexer(t)
		idx.recordInfo = &CacheInfo{Cached: true, ExpiresAt: feedClock.Add(5 * time.Minute)}
		return idx
	}
	mk := func(idx *fakeIndexer) http.Handler {
		return NewHandler(fakeProvider{"demo": idx}, WithAPIKey(testAPIKey),
			WithClock(func() time.Time { return feedClock }))
	}

	honor := doPath(t, mk(cachingDemo()), "/api/indexers/demo/results/torznab", "t=search&q=x")
	full := doPath(t, mk(cachingDemo()), "/api/indexers/demo/results/torznab/full", "t=search&q=x")
	if honor.Code != http.StatusOK || full.Code != http.StatusOK {
		t.Fatalf("statuses = %d / %d, want 200 / 200", honor.Code, full.Code)
	}
	eHonor, eFull := honor.Header().Get("ETag"), full.Header().Get("ETag")
	if eHonor == "" || eFull == "" {
		t.Fatalf("both variants must emit an ETag (honor=%q full=%q)", eHonor, eFull)
	}
	if eHonor == eFull {
		t.Errorf("honor and /full share an ETag (%s); the bypass variant must fold into the validator", eHonor)
	}

	// Cross-variant revalidation: the honor ETag against /full must NOT be answered 304 —
	// the /full body (full catalog) is a different variant from what the honor ETag covers.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/indexers/demo/results/torznab/full?t=search&q=x&apikey="+testAPIKey, nil)
	req.Header.Set("If-None-Match", eHonor)
	rec := httptest.NewRecorder()
	mk(cachingDemo()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cross-variant revalidation status = %d, want 200 (honor ETag must not 304 the /full feed)", rec.Code)
	}

	// Same-variant revalidation still works: /full with its own ETag is 304.
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/indexers/demo/results/torznab/full?t=search&q=x&apikey="+testAPIKey, nil)
	req2.Header.Set("If-None-Match", eFull)
	rec2 := httptest.NewRecorder()
	mk(cachingDemo()).ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("same-variant revalidation status = %d, want 304 (caching must still work)", rec2.Code)
	}
}
