package registry_test

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// pagingDoer fronts the Newznab driver and returns DISTINCT items per requested page so a
// deep-page fetch is observable end-to-end: an offset=N request yields items whose guids
// embed N. The caps request returns the saved golden so the driver's lazy Capabilities()
// resolves. It records nothing secret — the test only inspects served guids.
type pagingDoer struct {
	caps string
	mu   sync.Mutex
	seen []string // offset labels the driver requested, in order
}

func (d *pagingDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	if req.URL.Query().Get("t") == "caps" {
		return mkResp(stdhttp.StatusOK, d.caps, "application/xml"), nil
	}
	offset := req.URL.Query().Get("offset")
	if offset == "" {
		offset = "0"
	}
	d.mu.Lock()
	d.seen = append(d.seen, offset)
	d.mu.Unlock()
	return mkResp(stdhttp.StatusOK, pageRSS(offset, 3), "application/rss+xml"), nil
}

// pageRSS builds an RSS body of n items whose guids/titles embed the page offset, so a
// page-0 body and a page-100 body share no guid. Every item carries an enclosure (the
// driver skips enclosure-less items).
func pageRSS(offset string, n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>`)
	for i := 1; i <= n; i++ {
		guid := fmt.Sprintf("p%si%d", offset, i)
		fmt.Fprintf(&b,
			`<item><title>P%s-%d</title><guid isPermaLink="false">%s</guid>`+
				`<pubDate>Mon, 02 Jan 2023 15:04:05 +0000</pubDate>`+
				`<enclosure url="https://news.example.test/getnzb/%s.nzb" length="1000" type="application/x-nzb" />`+
				`<newznab:attr name="category" value="2000" /></item>`,
			offset, i, guid, guid)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

// TestNewznabDeepPagingThroughCache is the blocker-catching, production-shape test: it
// drives the shared read pipeline (torznabhttp.SearchReleases) over the REAL generic
// Newznab driver served through the registry's flattened *indexerAdapter with caching
// ENABLED (via reg.Indexer, the actual served value — not a test scaffold). The blocker it
// guards: the served value must satisfy torznabhttp.OffsetPager and report true, or the
// pipeline takes the local-slice branch — re-offsetting a driver that already paged
// upstream and serving an EMPTY page. The flattened adapter implements OffsetPager directly
// (compile-time assured in adapter.go); this test asserts CONTENT: the offset=100 page's
// guids must appear in res.Releases.
func TestNewznabDeepPagingThroughCache(t *testing.T) {
	caps, err := os.ReadFile("../native/newznab/testdata/caps.xml")
	if err != nil {
		t.Fatalf("read caps golden: %v", err)
	}
	doer := &pagingDoer{caps: string(caps)}
	reg, _ := newCachingRegistry(t, doer)
	addNewznab(t, reg, "nzb-paging", "newznab", "https://news.example.test")

	ctx := context.Background()
	// reg.Indexer returns the REAL flattened *indexerAdapter wired to the search cache (the
	// production serve shape), so this drives the actual served value end-to-end.
	cached, ok := reg.Indexer(ctx, "nzb-paging")
	if !ok {
		t.Fatal("nzb-paging should resolve")
	}

	// The blocker tripwire: the served adapter MUST promote the paging capability directly.
	pager, ok := cached.(torznabhttp.OffsetPager)
	if !ok || !pager.SupportsOffsetPaging() {
		t.Fatal("blocker: the served *indexerAdapter must satisfy torznabhttp.OffsetPager and report true")
	}

	res, err := torznabhttp.SearchReleases(ctx, cached,
		url.Values{"q": {"x"}, "offset": {"100"}, "limit": {"100"}})
	if err != nil {
		t.Fatalf("SearchReleases(offset=100): %v", err)
	}

	guids := make(map[string]bool, len(res.Releases))
	for _, r := range res.Releases {
		guids[r.GUID] = true
	}
	// CONTENT assertion: the offset=100 page's guids reached the served result set.
	for _, want := range []string{"p100i1", "p100i2", "p100i3"} {
		if !guids[want] {
			t.Errorf("page-2 guid %q missing from res.Releases; got %v", want, servedGUIDs(res.Releases))
		}
	}
	// And the first page must NOT leak into a deep-page request.
	if guids["p0i1"] {
		t.Errorf("page-1 guid leaked into the offset=100 result: %v", servedGUIDs(res.Releases))
	}
	if res.Offset != 100 {
		t.Errorf("res.Offset = %d, want 100", res.Offset)
	}

	// The driver must have actually paged upstream at offset=100 (not 0).
	doer.mu.Lock()
	seen := append([]string(nil), doer.seen...)
	doer.mu.Unlock()
	if len(seen) == 0 || seen[len(seen)-1] != "100" {
		t.Errorf("driver did not forward offset=100 upstream; saw %v", seen)
	}
}

// servedGUIDs lists the guids of the served releases for failure messages.
func servedGUIDs(rels []*normalizer.Release) []string {
	out := make([]string, len(rels))
	for i, r := range rels {
		out[i] = r.GUID
	}
	return out
}

// fixedReleasesIndexer is a non-paging control Indexer: it returns its full release set
// for ANY query and does NOT implement torznabhttp.OffsetPager, so the pipeline must slice
// the page locally (today's behavior for every Cardigann def). It proves the offset-paging
// branch is gated on the capability, not applied unconditionally.
type fixedReleasesIndexer struct {
	caps     *mapper.Capabilities
	releases []*normalizer.Release

	mu       sync.Mutex
	searches int // number of live Search calls (to prove cache coalescing)
}

func (f *fixedReleasesIndexer) Info() torznabhttp.IndexerInfo {
	return torznabhttp.IndexerInfo{ID: "fixed"}
}
func (f *fixedReleasesIndexer) Capabilities() *mapper.Capabilities { return f.caps }
func (f *fixedReleasesIndexer) Search(_ context.Context, _ search.Query) ([]*normalizer.Release, error) {
	f.mu.Lock()
	f.searches++
	f.mu.Unlock()
	return f.releases, nil
}

func (f *fixedReleasesIndexer) searchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.searches
}
func (f *fixedReleasesIndexer) NeedsResolver() bool     { return false }
func (f *fixedReleasesIndexer) DownloadNeedsAuth() bool { return false }
func (f *fixedReleasesIndexer) Grab(_ context.Context, _ string) (*search.GrabResult, error) {
	return &search.GrabResult{}, nil
}

// TestNonPagingControlLocalSlices is the control for the blocker test: a non-paging
// indexer served through the cache (the cacheProbe scaffold, since fixedReleasesIndexer is
// a torznabhttp.Indexer fake, not a native.Driver, so it can't traverse reg.Indexer) must
// local-slice at offset=100 (the driver returned the full set), so the deep page is the
// [100:150] slice — never an upstream-paged page. This proves the cache/handler report
// SupportsOffsetPaging()=false for an indexer that is not an OffsetPager, and the pipeline
// keeps the unchanged behavior.
func TestNonPagingControlLocalSlices(t *testing.T) {
	t.Parallel()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const total = 150
	rels := make([]*normalizer.Release, total)
	for i := range rels {
		rels[i] = &normalizer.Release{
			GUID:  fmt.Sprintf("fixed-%03d", i),
			Title: fmt.Sprintf("F%03d", i),
			Link:  fmt.Sprintf("https://fixed.test/%d", i),
		}
	}
	fixed := &fixedReleasesIndexer{caps: minimalCaps(t), releases: rels}

	instID := insertRegInstance(t, db)
	sc := registry.NewSearchCacheForTest(db, fixedClock)
	cached := registry.WrapForTest(sc, fixed, instID)

	if pager, ok := cached.(torznabhttp.OffsetPager); !ok || pager.SupportsOffsetPaging() {
		t.Fatalf("non-paging control must report SupportsOffsetPaging()=false (ok=%v)", ok)
	}

	res, err := torznabhttp.SearchReleases(context.Background(), cached,
		url.Values{"q": {"x"}, "offset": {"100"}, "limit": {"100"}})
	if err != nil {
		t.Fatalf("SearchReleases: %v", err)
	}
	if len(res.Releases) != 50 {
		t.Fatalf("non-paging deep page = %d releases, want 50 (local slice [100:150])", len(res.Releases))
	}
	if res.Releases[0].GUID != "fixed-100" {
		t.Errorf("first served guid = %q, want fixed-100 (the local slice start)", res.Releases[0].GUID)
	}
	if res.Total != total {
		t.Errorf("non-paging Total = %d, want %d (full match count, pre-slice)", res.Total, total)
	}
}

// TestPagingFetchPerPageVsNonPagingShared pins the cache-key consequence of step 4/5: a
// paging-capable driver keys per page, so two distinct pages drive TWO upstream fetches; a
// non-paging driver keys page-free, so two pages share ONE fetch (the single-fetch superset
// the local-slice design preserves). The paging half runs through the real flattened
// adapter (reg.Indexer, caching enabled); the non-paging half uses the cacheProbe scaffold
// over a fake torznabhttp.Indexer.
func TestPagingFetchPerPageVsNonPagingShared(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Paging: two pages => two upstream search fetches (distinct cache keys).
	caps, err := os.ReadFile("../native/newznab/testdata/caps.xml")
	if err != nil {
		t.Fatalf("read caps golden: %v", err)
	}
	doer := &pagingDoer{caps: string(caps)}
	reg, _ := newCachingRegistry(t, doer)
	addNewznab(t, reg, "nzb-fetchcount", "newznab", "https://news.example.test")
	pcache, ok := reg.Indexer(ctx, "nzb-fetchcount")
	if !ok {
		t.Fatal("nzb-fetchcount should resolve")
	}

	for _, off := range []string{"0", "100"} {
		if _, err := torznabhttp.SearchReleases(ctx, pcache,
			url.Values{"q": {"x"}, "offset": {off}, "limit": {"100"}}); err != nil {
			t.Fatalf("paging SearchReleases(offset=%s): %v", off, err)
		}
	}
	doer.mu.Lock()
	searchFetches := len(doer.seen)
	doer.mu.Unlock()
	if searchFetches != 2 {
		t.Errorf("paging driver search fetches = %d, want 2 (one per page; per-page cache key)", searchFetches)
	}

	// Non-paging: two pages share one cache key => one fetch (superset reused, local-sliced).
	db2, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	if err := db2.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	rels := make([]*normalizer.Release, 150)
	for i := range rels {
		rels[i] = &normalizer.Release{GUID: fmt.Sprintf("fx-%03d", i), Link: fmt.Sprintf("https://fixed.test/%d", i)}
	}
	fixed := &fixedReleasesIndexer{caps: minimalCaps(t), releases: rels}
	instID := insertRegInstance(t, db2)
	ncache := registry.WrapForTest(registry.NewSearchCacheForTest(db2, fixedClock), fixed, instID)

	for _, off := range []string{"0", "100"} {
		if _, err := torznabhttp.SearchReleases(ctx, ncache,
			url.Values{"q": {"x"}, "offset": {off}, "limit": {"100"}}); err != nil {
			t.Fatalf("non-paging SearchReleases(offset=%s): %v", off, err)
		}
	}
	if got := fixed.searchCount(); got != 1 {
		t.Errorf("non-paging driver search fetches = %d, want 1 (page-free key; one fetch serves every page)", got)
	}
}

// minimalCaps builds a caps doc with a single Movies category so buildQuery/filterResults
// have a non-nil capabilities to map against.
func minimalCaps(t *testing.T) *mapper.Capabilities {
	t.Helper()
	def := &loader.Definition{
		ID:    "fixed",
		Links: []string{"https://fixed.test/"},
		Caps: loader.Caps{
			CategoryMappings: []loader.CategoryMapping{
				{ID: loader.Scalar{Value: "1", Set: true}, Cat: "Movies"},
			},
			Modes: loader.Modes{Search: []string{"q"}},
		},
	}
	caps, err := mapper.Build(def)
	if err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}
	return caps
}
