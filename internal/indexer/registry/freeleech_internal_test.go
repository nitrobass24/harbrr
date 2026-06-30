package registry

import (
	"context"
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
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

func TestFreeleechIndexer_Search(t *testing.T) {
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
			inner := &fakeInner{releases: relsFixture()}
			idx := &freeleechIndexer{Indexer: inner, freeleechOnly: tt.freeleechOnly}

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

// TestFreeleechIndexer_DoesNotMutateInner proves the filter allocates a fresh slice so
// the cached full set (shared with the bypass feed + announce tap) is never mutated.
func TestFreeleechIndexer_DoesNotMutateInner(t *testing.T) {
	t.Parallel()
	inner := &fakeInner{releases: relsFixture()}
	idx := &freeleechIndexer{Indexer: inner, freeleechOnly: true}

	if _, err := idx.Search(context.Background(), search.Query{}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := titles(inner.releases); !slices.Equal(got, []string{"free-a", "paid", "free-b", "partial"}) {
		t.Errorf("inner.releases mutated to %v", got)
	}
}

// TestFreeleechIndexer_OffsetPagingDelegated proves the decorator forwards the optional
// OffsetPager capability (embedding the interface would otherwise hide it from the
// handler, which would then double-offset a deep-paging driver).
func TestFreeleechIndexer_OffsetPagingDelegated(t *testing.T) {
	t.Parallel()
	idx := &freeleechIndexer{Indexer: &pagingInner{}, freeleechOnly: false}
	pager, ok := torznabhttp.Indexer(idx).(torznabhttp.OffsetPager)
	if !ok {
		t.Fatal("freeleechIndexer does not satisfy OffsetPager")
	}
	if !pager.SupportsOffsetPaging() {
		t.Error("SupportsOffsetPaging() = false, want true (delegated from inner)")
	}
}

// pagingInner is a fakeInner that also forwards offset/limit upstream.
type pagingInner struct{ fakeInner }

func (p *pagingInner) SupportsOffsetPaging() bool { return true }
