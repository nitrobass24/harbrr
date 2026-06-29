package torznabhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestWantsNoCache covers the nocache=1 param predicate.
func TestWantsNoCache(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		q    url.Values
		want bool
	}{
		{"set", url.Values{"nocache": {"1"}}, true},
		{"zero", url.Values{"nocache": {"0"}}, false},
		{"other", url.Values{"nocache": {"true"}}, false},
		{"absent", url.Values{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := wantsNoCache(tt.q); got != tt.want {
				t.Errorf("wantsNoCache(%v) = %v, want %v", tt.q, got, tt.want)
			}
		})
	}
}

// TestCacheBypassRoundTrip confirms WithCacheBypass/CacheBypass round-trip and a
// bare context reports false.
func TestCacheBypassRoundTrip(t *testing.T) {
	t.Parallel()
	if CacheBypass(context.Background()) {
		t.Error("bare context should not report cache bypass")
	}
	if !CacheBypass(WithCacheBypass(context.Background())) {
		t.Error("WithCacheBypass context should report cache bypass")
	}
}

// TestSearchReleasesSetsCacheBypass proves the JSON path (SearchReleases) threads
// nocache=1 into the context handed to Search, and absent/0 does not.
func TestSearchReleasesSetsCacheBypass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		q    url.Values
		want bool
	}{
		{"nocache=1", url.Values{"q": {"x"}, "nocache": {"1"}}, true},
		{"nocache=0", url.Values{"q": {"x"}, "nocache": {"0"}}, false},
		{"absent", url.Values{"q": {"x"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			idx := &fakeIndexer{info: IndexerInfo{ID: "demo"}, caps: testCaps(t)}
			if _, err := SearchReleases(context.Background(), idx, tt.q); err != nil {
				t.Fatalf("SearchReleases: %v", err)
			}
			if got := CacheBypass(idx.gotCtx); got != tt.want {
				t.Errorf("CacheBypass(Search ctx) = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWriteResultsSetsCacheBypass proves the Torznab feed path (writeResults)
// threads nocache=1 into the context handed to Search, and absent does not.
func TestWriteResultsSetsCacheBypass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		rawQuery string
		want     bool
	}{
		{"nocache=1", "t=search&q=x&nocache=1", true},
		{"absent", "t=search&q=x", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			idx := demoIndexer(t)
			h := newTestHandler(t, idx)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
				"/api/v2.0/indexers/demo/results/torznab?"+tt.rawQuery+"&apikey="+testAPIKey, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
			}
			if got := CacheBypass(idx.gotCtx); got != tt.want {
				t.Errorf("CacheBypass(Search ctx) = %v, want %v", got, tt.want)
			}
		})
	}
}
