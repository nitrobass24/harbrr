package core

import (
	"context"
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
			if got := WantsNoCache(tt.q); got != tt.want {
				t.Errorf("WantsNoCache(%v) = %v, want %v", tt.q, got, tt.want)
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
