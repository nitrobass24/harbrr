package registry

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// relsFixture is a mixed set: two freeleech releases (dvf 0) and two non-free (dvf 1
// and a partial 0.5), so the filter must keep exactly the dvf==0 pair.
func relsFixture() []*normalizer.Release {
	return []*normalizer.Release{
		{Title: "free-a", DownloadVolumeFactor: 0},
		{Title: "paid", DownloadVolumeFactor: 1},
		{Title: "free-b", DownloadVolumeFactor: 0},
		{Title: "partial", DownloadVolumeFactor: 0.5},
	}
}

func titles(rels []*normalizer.Release) []string {
	out := make([]string, 0, len(rels))
	for _, r := range rels {
		out = append(out, r.Title)
	}
	return out
}

// fakeDriver is a minimal native.Driver test double returning a fixed release set. It is
// the engine-shaped core the adapter wraps, so the freeleech serve-time view can be
// exercised through the REAL indexerAdapter.Search (cache left nil ⇒ the live path).
type fakeDriver struct {
	releases []*normalizer.Release
}

func (d *fakeDriver) Capabilities() *mapper.Capabilities { return &mapper.Capabilities{} }
func (d *fakeDriver) NeedsResolver() bool                { return false }
func (d *fakeDriver) DownloadNeedsAuth() bool            { return false }
func (d *fakeDriver) SupportsOffsetPaging() bool         { return false }
func (d *fakeDriver) Test(context.Context) error         { return nil }

func (d *fakeDriver) Search(_ context.Context, _ search.Query) ([]*normalizer.Release, error) {
	return d.releases, nil
}

func (d *fakeDriver) Grab(context.Context, string) (*search.GrabResult, error) {
	return &search.GrabResult{}, nil
}

// pagingDriver is a fakeDriver that overrides SupportsOffsetPaging to true, so the
// adapter's direct call to the wrapped driver's method reports true.
type pagingDriver struct{ fakeDriver }

func (p *pagingDriver) SupportsOffsetPaging() bool { return true }

// newFreeleechAdapter builds a minimal cache-less adapter over inner. clock + stats are set
// because liveSearch samples the clock and records a query unconditionally; cache stays nil
// so Search takes the live branch, and info/health/db are only touched on a CLASSIFIED
// error, which these fakes never return.
func newFreeleechAdapter(inner native.Driver, freeleechOnly bool) *indexerAdapter {
	return &indexerAdapter{
		info:          core.IndexerInfo{ID: "fake"},
		inner:         inner,
		freeleechOnly: freeleechOnly,
		stats:         newIndexerStats(nil, time.Now, zerolog.Nop()),
		budget:        newRequestBudget(nil, time.Now, zerolog.Nop()),
		clock:         time.Now,
		log:           zerolog.Nop(),
	}
}

// TestFreeleechAdapter_Search exercises the serve-time freeleech view now inlined in
// indexerAdapter.Search: honor mode keeps only dvf==0, the bypass variant returns the full
// catalog, and freeleech-off is a no-op.
func TestFreeleechAdapter_Search(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		freeleechOnly bool
		bypass        bool
		wantTitles    []string
	}{
		{
			name:          "honor freeleech-only keeps dvf==0",
			freeleechOnly: true,
			bypass:        false,
			wantTitles:    []string{"free-a", "free-b"},
		},
		{
			name:          "bypass returns the full catalog even when freeleech-only",
			freeleechOnly: true,
			bypass:        true,
			wantTitles:    []string{"free-a", "paid", "free-b", "partial"},
		},
		{
			name:          "freeleech off is a no-op (full catalog)",
			freeleechOnly: false,
			bypass:        false,
			wantTitles:    []string{"free-a", "paid", "free-b", "partial"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			inner := &fakeDriver{releases: relsFixture()}
			idx := newFreeleechAdapter(inner, tt.freeleechOnly)

			got, err := idx.Search(context.Background(), search.Query{FreeleechBypass: tt.bypass})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if g := titles(got); !slices.Equal(g, tt.wantTitles) {
				t.Errorf("titles = %v, want %v", g, tt.wantTitles)
			}
		})
	}
}

// TestFreeleechAdapter_DoesNotMutateInner proves the filter allocates a fresh slice so the
// cached full set (shared with the bypass feed + announce tap) is never mutated.
func TestFreeleechAdapter_DoesNotMutateInner(t *testing.T) {
	t.Parallel()
	inner := &fakeDriver{releases: relsFixture()}
	idx := newFreeleechAdapter(inner, true)

	if _, err := idx.Search(context.Background(), search.Query{}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := titles(inner.releases); !slices.Equal(got, []string{"free-a", "paid", "free-b", "partial"}) {
		t.Errorf("inner.releases mutated to %v", got)
	}
}

// TestFreeleechAdapter_OffsetPaging proves the flattened adapter reports
// SupportsOffsetPaging directly off the wrapped driver — true for a paging driver,
// false for one that answers false. This replaces the old decorator's hand-forwarding
// test; the compile-time var _ core.Indexer assertion (adapter.go) is the static
// backstop that SupportsOffsetPaging is part of the contract, not an optional type-assert.
func TestFreeleechAdapter_OffsetPaging(t *testing.T) {
	t.Parallel()

	paging := newFreeleechAdapter(&pagingDriver{}, false)
	if !paging.SupportsOffsetPaging() {
		t.Error("paging driver: SupportsOffsetPaging() = false, want true (promoted off the driver)")
	}
	var _ core.Indexer = paging

	nonPaging := newFreeleechAdapter(&fakeDriver{}, false)
	if nonPaging.SupportsOffsetPaging() {
		t.Error("non-paging driver: SupportsOffsetPaging() = true, want false")
	}
}
