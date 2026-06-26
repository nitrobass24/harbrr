package torznab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// feedClock is the fixed reference time for the conditional-GET handler tests.
var feedClock = time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC)

func TestHasNoCacheDirective(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"no-cache", true},
		{"no-store", true},
		{"No-Cache", true},
		{"private, no-cache", true},
		{"max-age=0", false},
		{"max-age=0, no-store", true},
	}
	for _, tt := range tests {
		if got := hasNoCacheDirective(tt.in); got != tt.want {
			t.Errorf("hasNoCacheDirective(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestRequestNoCache(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		set  func(*http.Request)
		want bool
	}{
		{"none", func(*http.Request) {}, false},
		{"cache-control no-cache", func(r *http.Request) { r.Header.Set("Cache-Control", "no-cache") }, true},
		{"pragma no-cache", func(r *http.Request) { r.Header.Set("Pragma", "no-cache") }, true},
		{"cache-control max-age", func(r *http.Request) { r.Header.Set("Cache-Control", "max-age=60") }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
			tt.set(r)
			if got := requestNoCache(r); got != tt.want {
				t.Errorf("requestNoCache = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIfNoneMatchMatches(t *testing.T) {
	t.Parallel()
	const etag = `"abc123"`
	tests := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"*", true},
		{`"abc123"`, true},
		{`W/"abc123"`, true},
		{`"zzz"`, false},
		{`"zzz", "abc123"`, true},
		{`"zzz", W/"abc123"`, true},
	}
	for _, tt := range tests {
		if got := ifNoneMatchMatches(tt.header, etag); got != tt.want {
			t.Errorf("ifNoneMatchMatches(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func TestCacheInfoSinkRoundTrip(t *testing.T) {
	t.Parallel()
	ctx, ci := WithCacheInfoSink(context.Background())
	RecordCacheInfo(ctx, CacheInfo{ETag: `"x"`, ExpiresAt: feedClock})
	if ci.ETag != `"x"` || !ci.ExpiresAt.Equal(feedClock) {
		t.Fatalf("sink not filled: %+v", ci)
	}
	// Recording into a ctx without a sink must be a no-op (no panic).
	RecordCacheInfo(context.Background(), CacheInfo{ETag: `"y"`})
}

// feedDo drives a feed request against a cache-recording indexer, with optional
// request headers, returning the recorder.
func feedDo(t *testing.T, idx *fakeIndexer, rawQuery string, hdr http.Header) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(fakeProvider{"rich": idx}, WithAPIKey(testAPIKey),
		WithClock(func() time.Time { return feedClock }))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v2.0/indexers/rich/results/torznab?"+rawQuery+"&apikey="+testAPIKey, nil)
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// cachingIndexer is a rich indexer that reports a cached response with the given etag,
// expiring 5 minutes after the fixed feed clock.
func cachingIndexer(t *testing.T, etag string) *fakeIndexer {
	t.Helper()
	idx := richIndexer(t)
	idx.recordInfo = &CacheInfo{ETag: etag, ExpiresAt: feedClock.Add(5 * time.Minute)}
	return idx
}

// TestFeedEmitsValidators proves a cache-backed feed response carries ETag +
// Cache-Control with the entry's remaining TTL as max-age.
func TestFeedEmitsValidators(t *testing.T) {
	t.Parallel()
	rec := feedDo(t, cachingIndexer(t, `"abc"`), "t=search&q=x", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("ETag"); got != `"abc"` {
		t.Errorf("ETag = %q, want %q", got, `"abc"`)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=300" {
		t.Errorf("Cache-Control = %q, want private, max-age=300", got)
	}
	if rec.Body.Len() == 0 {
		t.Error("200 response should have a feed body")
	}
}

// TestFeedConditionalGet304 proves a matching If-None-Match yields 304 with no body
// and the validators still set; a non-matching one yields a normal 200.
func TestFeedConditionalGet304(t *testing.T) {
	t.Parallel()

	rec := feedDo(t, cachingIndexer(t, `"abc"`), "t=search&q=x",
		http.Header{"If-None-Match": {`"abc"`}})
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 body = %q, want empty", rec.Body.String())
	}
	if rec.Header().Get("ETag") != `"abc"` {
		t.Error("304 should still carry the ETag")
	}

	rec = feedDo(t, cachingIndexer(t, `"abc"`), "t=search&q=x",
		http.Header{"If-None-Match": {`"stale"`}})
	if rec.Code != http.StatusOK {
		t.Fatalf("non-matching If-None-Match status = %d, want 200", rec.Code)
	}
}

// TestFeedNoCacheHeaderForcesFresh proves a `Cache-Control: no-cache` request header
// bypasses the cache (forces a live fetch) and suppresses the 304 even when the
// client's If-None-Match would otherwise match.
func TestFeedNoCacheHeaderForcesFresh(t *testing.T) {
	t.Parallel()
	idx := cachingIndexer(t, `"abc"`)
	rec := feedDo(t, idx, "t=search&q=x",
		http.Header{"If-None-Match": {`"abc"`}, "Cache-Control": {"no-cache"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no-cache suppresses 304)", rec.Code)
	}
	if !CacheBypass(idx.gotCtx) {
		t.Error("a no-cache request header must set cache bypass on the search ctx")
	}
}

// TestFeedNoValidatorsWhenUncached proves a response that did not come through the
// cache emits no ETag/Cache-Control (the sink stays empty).
func TestFeedNoValidatorsWhenUncached(t *testing.T) {
	t.Parallel()
	idx := richIndexer(t) // recordInfo nil => no sink fill
	rec := feedDo(t, idx, "t=search&q=x", http.Header{"If-None-Match": {`"abc"`}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("ETag"); got != "" {
		t.Errorf("ETag = %q, want empty (uncached)", got)
	}
}
