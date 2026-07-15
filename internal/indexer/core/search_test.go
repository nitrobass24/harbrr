package core

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
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
	if len(got.Releases) != 2 {
		t.Fatalf("expected 2 releases after dedupe, got %d", len(got.Releases))
	}
	if got.Total != 2 {
		t.Errorf("Total = %d, want 2 (post-dedupe, pre-slice)", got.Total)
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

// TestSearchReleasesCrossPageDisjoint proves the shared pipeline both surfaces page
// over (the Torznab feed and the JSON API both call SearchReleases) splits a >100-result
// fetch into DISJOINT windows — page 0 and page 1 share no release — with a stable,
// honest Total on every page. This is the parity root for the per-surface tests and the
// executable form of the property Prowlarr violates (#1428: it re-serves page 0 when
// asked for the next 100). Each release has a unique link, so its link is its guid.
func TestSearchReleasesCrossPageDisjoint(t *testing.T) {
	t.Parallel()
	const total = 150
	idx := &fakeIndexer{info: IndexerInfo{ID: "demo"}, caps: testCaps(t)}
	idx.releases = make([]*normalizer.Release, total)
	for i := range idx.releases {
		idx.releases[i] = demoRelease(
			fmt.Sprintf("R%03d", i),
			fmt.Sprintf("https://demo.test/dl/%d", i),
			[]int{2000},
		)
	}

	page := func(offset int) SearchResult {
		t.Helper()
		q := url.Values{"q": {"x"}, "limit": {"100"}, "offset": {strconv.Itoa(offset)}}
		res, err := SearchReleases(context.Background(), idx, q)
		if err != nil {
			t.Fatalf("SearchReleases(offset=%d): %v", offset, err)
		}
		return res
	}

	p0, p1 := page(0), page(100)
	if len(p0.Releases) != 100 || len(p1.Releases) != 50 {
		t.Fatalf("page lengths = %d / %d, want 100 / 50", len(p0.Releases), len(p1.Releases))
	}
	if p0.Total != total || p1.Total != total {
		t.Errorf("Total = %d / %d, want %d on both (full match count, not page length)", p0.Total, p1.Total, total)
	}
	seen := make(map[string]bool, len(p0.Releases))
	for _, r := range p0.Releases {
		seen[r.Link] = true
	}
	for _, r := range p1.Releases {
		if seen[r.Link] {
			t.Errorf("release %q served on BOTH pages (Prowlarr #1428 re-serve)", r.Link)
		}
	}
}

// TestSearchReleasesTotalIsHonest pins that Total is the REAL match count of harbrr's
// single fetch — measured after dedupe-by-guid and category filtering, before the page
// slice — never the raw upstream count nor a speculative estimate. A future change that
// inflated Total would reintroduce the wasted client requests this design avoids
// (Sonarr/Radarr stop paging on a short page, so an honest Total must reflect what
// harbrr actually has).
func TestSearchReleasesTotalIsHonest(t *testing.T) {
	t.Parallel()
	idx := &fakeIndexer{
		info: IndexerInfo{ID: "demo"}, caps: testCaps(t),
		releases: []*normalizer.Release{
			demoRelease("A", "https://demo.test/a", []int{2000}),
			demoRelease("A-dup", "https://demo.test/a", []int{2000}), // same link -> same guid -> deduped
			demoRelease("B", "https://demo.test/b", []int{2000}),
			demoRelease("C-tv", "https://demo.test/c", []int{5000}), // TV -> dropped by the cat=2000 filter
		},
	}
	// Raw Search returns 4; after dedupe (-1) and the category filter (-1) the honest
	// match count is 2.
	res, err := SearchReleases(context.Background(), idx, url.Values{"q": {"x"}, "cat": {"2000"}})
	if err != nil {
		t.Fatalf("SearchReleases: %v", err)
	}
	if res.Total != 2 {
		t.Errorf("Total = %d, want 2 (post-dedupe, post-filter; the raw fetch was 4)", res.Total)
	}
	if len(res.Releases) != 2 {
		t.Errorf("Releases = %d, want 2", len(res.Releases))
	}

	// An offset at/past Total yields an empty page but reports the SAME honest Total:
	// the client sees results exist (just none in this window), never a re-serve.
	past, err := SearchReleases(context.Background(), idx, url.Values{"q": {"x"}, "cat": {"2000"}, "offset": {"5"}})
	if err != nil {
		t.Fatalf("SearchReleases(offset past end): %v", err)
	}
	if len(past.Releases) != 0 {
		t.Errorf("offset past end: Releases = %d, want 0", len(past.Releases))
	}
	if past.Total != 2 {
		t.Errorf("offset past end: Total = %d, want 2 (unchanged)", past.Total)
	}
}

// pagingFakeIndexer is a fakeIndexer that overrides SupportsOffsetPaging (the Newznab
// shape): it reports that it forwards offset/limit upstream, so the pipeline treats the
// returned slice as the already-paged window and must NOT re-offset it locally.
type pagingFakeIndexer struct {
	*fakeIndexer
}

func (p *pagingFakeIndexer) SupportsOffsetPaging() bool { return true }

func makePagingFake(t *testing.T, n int) *pagingFakeIndexer {
	t.Helper()
	f := &fakeIndexer{info: IndexerInfo{ID: "demo"}, caps: testCaps(t)}
	f.releases = make([]*normalizer.Release, n)
	for i := range f.releases {
		f.releases[i] = demoRelease(
			fmt.Sprintf("R%03d", i),
			fmt.Sprintf("https://demo.test/dl/%d", i),
			[]int{2000},
		)
	}
	return &pagingFakeIndexer{fakeIndexer: f}
}

// TestSearchReleasesPagingFullPageHasMoreFloor pins the paging branch's total-honesty for a
// FULL upstream page: the driver already skipped `offset` upstream, so the returned slice is
// served as-is (no local re-offset), and Total is the has-more floor offset+limit+1 — the
// "+1" past the requested window telling *arr a next page probably exists (Newznab reports
// no grand total). The served page must start at the driver's first returned item (NOT
// re-offset).
func TestSearchReleasesPagingFullPageHasMoreFloor(t *testing.T) {
	t.Parallel()
	idx := makePagingFake(t, 100) // a full page (== limit)

	res, err := SearchReleases(context.Background(), idx,
		url.Values{"q": {"x"}, "offset": {"100"}, "limit": {"100"}})
	if err != nil {
		t.Fatalf("SearchReleases: %v", err)
	}
	if len(res.Releases) != 100 {
		t.Fatalf("served = %d, want 100 (the returned page, NOT re-offset)", len(res.Releases))
	}
	if res.Releases[0].Title != "R000" {
		t.Errorf("first served = %q, want R000 (the driver's first item, no local offset re-apply)", res.Releases[0].Title)
	}
	if res.Total != 201 {
		t.Errorf("Total = %d, want 201 (offset 100 + limit 100 + has-more floor 1)", res.Total)
	}
	if res.Offset != 100 {
		t.Errorf("Offset = %d, want 100", res.Offset)
	}
	// The driver must have been asked to page upstream (offset/limit forwarded into the query).
	if idx.gotQuery.Offset != 100 || idx.gotQuery.Limit != 100 {
		t.Errorf("query offset/limit = %d/%d, want 100/100 forwarded upstream", idx.gotQuery.Offset, idx.gotQuery.Limit)
	}
}

// TestPagedResultFullPageFloorUsesLimit is the regression for the has-more floor when
// dedupe/category filtering shrinks a FULL upstream page below the limit. The floor must be
// offset+limit+1 (the REQUESTED width), never offset+len(served)+1: with served < limit the
// latter can fall at/under offset+limit, so *arr would compute "no next page" and stop before
// the genuine deep page — silently defeating deep-set paging.
func TestPagedResultFullPageFloorUsesLimit(t *testing.T) {
	t.Parallel()
	// Full raw upstream page (rawCount == limit == 100), but filtering left only 80 served.
	served := make([]*normalizer.Release, 80)
	for i := range served {
		served[i] = demoRelease(fmt.Sprintf("R%03d", i), fmt.Sprintf("https://demo.test/dl/%d", i), []int{2000})
	}
	res := pagedResult(served, paging{offset: 100, limit: 100}, 100)
	if res.Total != 201 {
		t.Errorf("Total = %d, want 201 (offset 100 + limit 100 + 1; the floor must use limit, not served=80 -> 181)", res.Total)
	}
	if len(res.Releases) != 80 {
		t.Errorf("served = %d, want 80 (the page is not re-padded)", len(res.Releases))
	}
}

// TestSearchReleasesPagingShortPageExactTotal pins the paging branch for a SHORT/last page
// (fewer than limit returned): Total is the EXACT offset+served, no has-more floor, so *arr
// stops paging.
func TestSearchReleasesPagingShortPageExactTotal(t *testing.T) {
	t.Parallel()
	idx := makePagingFake(t, 30) // short page (< limit)

	res, err := SearchReleases(context.Background(), idx,
		url.Values{"q": {"x"}, "offset": {"100"}, "limit": {"100"}})
	if err != nil {
		t.Fatalf("SearchReleases: %v", err)
	}
	if len(res.Releases) != 30 {
		t.Fatalf("served = %d, want 30", len(res.Releases))
	}
	if res.Total != 130 {
		t.Errorf("Total = %d, want 130 (offset 100 + served 30, no floor on a short page)", res.Total)
	}
}
