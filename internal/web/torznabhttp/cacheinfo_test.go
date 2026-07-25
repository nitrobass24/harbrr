package torznabhttp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/core"
	tzn "github.com/autobrr/harbrr/internal/torznab"
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

// revalidateHandler is a minimal handler for driving revalidate directly (no provider,
// no HTTP routing) — only the clock it reads for Cache-Control's max-age matters.
func revalidateHandler() *handler {
	return &handler{clock: func() time.Time { return feedClock }}
}

// TestRevalidateWrongVariantOrPageGuard is the DIRECT test for the documented
// "never answer a 304 with the wrong feed-variant or page body" hazard (handler.go's
// writeResults doc comment) — the composed decision that, before the revalidator
// extraction, was reachable only end-to-end (full handler + fake indexer + cache
// decorator, as TestFreeleechBypassETagDistinct and TestFeedConditionalGetPagingAware
// still exercise). It drives h.revalidate in isolation to prove the guard directly.
func TestRevalidateWrongVariantOrPageGuard(t *testing.T) {
	t.Parallel()
	h := revalidateHandler()
	ci := core.CacheInfo{Cached: true, ExpiresAt: feedClock.Add(5 * time.Minute)}
	releases := []*normalizer.Release{
		demoRelease("P0", "https://rich.test/dl/0.torrent", []int{2000}),
		demoRelease("P1", "https://rich.test/dl/1.torrent", []int{2000}),
	}
	honorPage0 := servedPage{releases: releases, offset: 0, limit: 1, total: 2}
	honorPage1 := servedPage{releases: releases, offset: 1, limit: 1, total: 2}
	honorPage0DiffTotal := servedPage{releases: releases, offset: 0, limit: 1, total: 3}

	// Capture the honor variant's page-0 served ETag (no If-None-Match yet, so this
	// call only sets validators and reports handled=false).
	rec := httptest.NewRecorder()
	if handled := h.revalidate(rec, http.Header{}, ci, honorPage0, false, false); handled {
		t.Fatal("capture call unexpectedly answered 304 (no If-None-Match to match)")
	}
	honorPage0ETag := rec.Header().Get("ETag")
	if honorPage0ETag == "" {
		t.Fatal("expected a served ETag to be set")
	}
	ifNoneMatch := http.Header{"If-None-Match": {honorPage0ETag}}

	tests := []struct {
		name string
		page servedPage
		// bypass selects the /full freeleech-bypass variant for the revalidate call.
		bypass bool
		// wantHandled is whether the call may answer 304: only the SAME variant and
		// page as the captured ETag — a cross-variant or cross-page match must fall
		// through to a 200 with the correct body instead.
		wantHandled bool
		// wantSameETag is whether the served ETag emitted by this call must equal the
		// captured one (it must differ for a different variant or page — the folds are
		// what keep the validators from cross-matching).
		wantSameETag bool
	}{
		{
			// The bypass fold keeps the two variants distinct even though they hash
			// the same underlying releases slice.
			name:   "cross-variant: bypass with the honor ETag is not 304",
			page:   honorPage0,
			bypass: true,
		},
		{
			// The page-window fold keeps the two pages distinct.
			name: "cross-page: page 1 with page 0's ETag is not 304",
			page: honorPage1,
		},
		{
			// The total fold: same releases/offset/limit but a changed reported total
			// (e.g. the catalog grew beyond the served window, moving pagedResult's
			// has-more floor) must not 304 — a stale total/has-more signal would pin
			// the *arrs to a wrong page count even though the page bytes are identical.
			name: "cross-total: changed total with the honor ETag is not 304",
			page: honorPage0DiffTotal,
		},
		{
			// Sanity: the guard rejects only a cross-variant/cross-page/cross-total
			// match, not every match.
			name:         "same variant, page, and total with its own ETag is 304",
			page:         honorPage0,
			wantHandled:  true,
			wantSameETag: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			if handled := h.revalidate(rec, ifNoneMatch, ci, tt.page, tt.bypass, false); handled != tt.wantHandled {
				t.Errorf("revalidate handled = %v, want %v", handled, tt.wantHandled)
			}
			if sameETag := rec.Header().Get("ETag") == honorPage0ETag; sameETag != tt.wantSameETag {
				t.Errorf("served ETag == captured ETag is %v, want %v", sameETag, tt.wantSameETag)
			}
		})
	}
}

// feedDo drives a feed request against a cache-recording indexer, with optional
// request headers, returning the recorder.
func feedDo(t *testing.T, idx *fakeIndexer, rawQuery string, hdr http.Header) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(fakeProvider{"rich": idx}, WithAPIKey(testAPIKey),
		WithClock(func() time.Time { return feedClock }))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/indexers/rich/results/torznab?"+rawQuery+"&apikey="+testAPIKey, nil)
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// cachingIndexer is a rich indexer that reports a cached response, expiring 5 minutes
// after the fixed feed clock. The served ETag header value is always derived by the
// handler from the served page (servedPayloadETag+pagedETag), never from CacheInfo —
// this only arranges for the cache-came-from signal to be true so validators are emitted.
func cachingIndexer(t *testing.T) *fakeIndexer {
	t.Helper()
	idx := richIndexer(t)
	idx.recordInfo = &core.CacheInfo{Cached: true, ExpiresAt: feedClock.Add(5 * time.Minute)}
	return idx
}

// TestFeedEmitsValidators proves a cache-backed feed response carries ETag +
// Cache-Control with the entry's remaining TTL as max-age. The emitted ETag is the
// served validator: the POST-filter served page hashed (servedPayloadETag, honor variant)
// folded with this page's window (offset=0, limit=tzn.LimitsMax for a window-less request).
func TestFeedEmitsValidators(t *testing.T) {
	t.Parallel()
	idx := cachingIndexer(t)
	rec := feedDo(t, idx, "t=search&q=x", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	view, ok := servedPayloadETag(idx.releases, false)
	if !ok {
		t.Fatal("servedPayloadETag failed to hash the served page")
	}
	want := pagedETag(view, 0, tzn.LimitsMax, len(idx.releases))
	if got := rec.Header().Get("ETag"); got != want {
		t.Errorf("ETag = %q, want %q", got, want)
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

	// Capture the served validator from a normal request, then revalidate with it.
	first := feedDo(t, cachingIndexer(t), "t=search&q=x", nil)
	served := first.Header().Get("ETag")
	if served == "" {
		t.Fatal("first response emitted no ETag to revalidate against")
	}
	rec := feedDo(t, cachingIndexer(t), "t=search&q=x",
		http.Header{"If-None-Match": {served}})
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 body = %q, want empty", rec.Body.String())
	}
	if rec.Header().Get("ETag") != served {
		t.Error("304 should still carry the served ETag")
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=300" {
		t.Errorf("304 Cache-Control = %q, want private, max-age=300", got)
	}

	rec = feedDo(t, cachingIndexer(t), "t=search&q=x",
		http.Header{"If-None-Match": {`"stale"`}})
	if rec.Code != http.StatusOK {
		t.Fatalf("non-matching If-None-Match status = %d, want 200", rec.Code)
	}
}

// TestFeedConditionalGetPagingAware proves the served ETag folds in the page window,
// so a client revalidating one page with a different page's ETag is NOT answered 304
// with the wrong page's body. The cached payload ETag is page-independent (one engine
// fetch serves every page), so without the fold the two pages would share a validator.
func TestFeedConditionalGetPagingAware(t *testing.T) {
	t.Parallel()
	newIdx := func() *fakeIndexer {
		idx := cachingIndexer(t)
		idx.releases = []*normalizer.Release{
			demoRelease("P0", "https://rich.test/dl/0.torrent", []int{2000}),
			demoRelease("P1", "https://rich.test/dl/1.torrent", []int{2000}),
		}
		return idx
	}
	// Page 1 (offset=0, limit=1): capture its served ETag and confirm it holds P0.
	page1 := feedDo(t, newIdx(), "t=search&q=x&offset=0&limit=1", nil)
	etag1 := page1.Header().Get("ETag")
	if etag1 == "" {
		t.Fatal("page 1 emitted no ETag")
	}
	if !strings.Contains(page1.Body.String(), "<title>P0</title>") {
		t.Fatalf("page 1 should contain P0:\n%s", page1.Body.String())
	}
	// Revalidate page 2 (offset=1, limit=1) with page 1's ETag → must NOT be 304; it
	// must serve page 2's body, and carry a distinct ETag.
	page2 := feedDo(t, newIdx(), "t=search&q=x&offset=1&limit=1",
		http.Header{"If-None-Match": {etag1}})
	if page2.Code != http.StatusOK {
		t.Fatalf("page 2 with page-1 ETag status = %d, want 200 (paging-aware ETag must not 304)", page2.Code)
	}
	if !strings.Contains(page2.Body.String(), "<title>P1</title>") {
		t.Fatalf("page 2 should contain P1:\n%s", page2.Body.String())
	}
	etag2 := page2.Header().Get("ETag")
	if etag2 == etag1 {
		t.Errorf("page 2 ETag == page 1 ETag (%s); distinct pages must get distinct validators", etag2)
	}
	// Caching still works: revalidating page 2 with page 2's own ETag yields 304.
	again := feedDo(t, newIdx(), "t=search&q=x&offset=1&limit=1",
		http.Header{"If-None-Match": {etag2}})
	if again.Code != http.StatusNotModified {
		t.Fatalf("page 2 with its own ETag status = %d, want 304 (caching must still work)", again.Code)
	}
}

// TestFeedNoCacheHeaderForcesFresh proves a `Cache-Control: no-cache` request header
// bypasses the cache (forces a live fetch) and suppresses the 304 even when the
// client's If-None-Match would otherwise match.
func TestFeedNoCacheHeaderForcesFresh(t *testing.T) {
	t.Parallel()
	idx := cachingIndexer(t)
	rec := feedDo(t, idx, "t=search&q=x",
		http.Header{"If-None-Match": {`"abc"`}, "Cache-Control": {"no-cache"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no-cache suppresses 304)", rec.Code)
	}
	if !core.CacheBypass(idx.gotCtx) {
		t.Error("a no-cache request header must set cache bypass on the search ctx")
	}
}

// feedGUIDRe / feedTotalRe extract the rendered <guid>s and the <newznab:response
// total> from a served feed body. The rich test indexer is direct (no /dl rewrite), so
// each <guid> is the release's raw link — unique per release in the paging fixtures.
var (
	feedGUIDRe  = regexp.MustCompile(`<guid[^>]*>([^<]+)</guid>`)
	feedTotalRe = regexp.MustCompile(`<newznab:response[^>]*\btotal="(\d+)"`)
)

func feedGUIDs(body string) []string {
	m := feedGUIDRe.FindAllStringSubmatch(body, -1)
	out := make([]string, 0, len(m))
	for _, g := range m {
		out = append(out, g[1])
	}
	return out
}

func feedTotal(t *testing.T, body string) int {
	t.Helper()
	m := feedTotalRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no <newznab:response total> in feed body:\n%s", body)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("non-numeric total %q: %v", m[1], err)
	}
	return n
}

// pagingIndexer is a rich (direct-link) indexer holding n synthetic releases with
// unique links, for the cross-page paging tests.
func pagingIndexer(t *testing.T, n int) *fakeIndexer {
	t.Helper()
	idx := cachingIndexer(t) // Cached=true so validators are emitted
	rels := make([]*normalizer.Release, n)
	for i := range rels {
		rels[i] = demoRelease(
			fmt.Sprintf("R%03d", i),
			fmt.Sprintf("https://rich.test/dl/%d.torrent", i),
			[]int{2000},
		)
	}
	idx.releases = rels
	return idx
}

// TestFeedCrossPageNoDuplicate is the executable form of "harbrr never re-serves
// page 0" — the bug Prowlarr ships (#1428: a request for the next 100 results re-returns
// the first 100). With a >100-result fetch, page 0 (offset=0,limit=100) and page 1
// (offset=100,limit=100) must render DISJOINT guids, both must report the same honest
// <newznab:response total>, and the two pages' served ETags must differ (the pagedETag
// fold) while a same-window revalidation still 304s.
func TestFeedCrossPageNoDuplicate(t *testing.T) {
	t.Parallel()
	const total = 150

	page0 := feedDo(t, pagingIndexer(t, total), "t=search&q=x&offset=0&limit=100", nil)
	page1 := feedDo(t, pagingIndexer(t, total), "t=search&q=x&offset=100&limit=100", nil)
	if page0.Code != http.StatusOK || page1.Code != http.StatusOK {
		t.Fatalf("page statuses = %d / %d, want 200 / 200", page0.Code, page1.Code)
	}

	g0 := feedGUIDs(page0.Body.String())
	g1 := feedGUIDs(page1.Body.String())
	if len(g0) != 100 {
		t.Fatalf("page 0 rendered %d items, want 100", len(g0))
	}
	if len(g1) != 50 {
		t.Fatalf("page 1 rendered %d items, want 50 (150 - 100)", len(g1))
	}
	// The property Prowlarr violates: no result appears on both pages.
	seen := make(map[string]bool, len(g0))
	for _, g := range g0 {
		seen[g] = true
	}
	for _, g := range g1 {
		if seen[g] {
			t.Errorf("guid %q served on BOTH page 0 and page 1 (Prowlarr #1428 re-serve)", g)
		}
	}
	// Honest, identical total on every page (the full match count, not the page length).
	if got0, got1 := feedTotal(t, page0.Body.String()), feedTotal(t, page1.Body.String()); got0 != total || got1 != total {
		t.Errorf("<newznab:response total> = %d (page 0) / %d (page 1), want %d on both", got0, got1, total)
	}

	// Distinct windows -> distinct served validators...
	e0, e1 := page0.Header().Get("ETag"), page1.Header().Get("ETag")
	if e0 == "" || e1 == "" {
		t.Fatal("both pages must emit an ETag")
	}
	if e0 == e1 {
		t.Errorf("page 0 and page 1 share an ETag (%s); the page window must fold into the validator", e0)
	}
	// The fold covers the LIMIT dimension too: same offset, different limit -> distinct
	// validator (so a client revalidating offset=0&limit=50 is never 304'd against a
	// cached offset=0&limit=100 body).
	half := feedDo(t, pagingIndexer(t, total), "t=search&q=x&offset=0&limit=50", nil)
	if eh := half.Header().Get("ETag"); eh == e0 {
		t.Errorf("offset=0&limit=50 shares the offset=0&limit=100 ETag (%s); limit must fold into the validator", eh)
	}
	// ...while a same-window revalidation still 304s (caching is intact).
	reval := feedDo(t, pagingIndexer(t, total), "t=search&q=x&offset=100&limit=100",
		http.Header{"If-None-Match": {e1}})
	if reval.Code != http.StatusNotModified {
		t.Errorf("page 1 revalidated with its own ETag = %d, want 304", reval.Code)
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
	if got := rec.Header().Get("Cache-Control"); got != "" {
		t.Errorf("Cache-Control = %q, want empty (uncached)", got)
	}
}
