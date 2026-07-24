package core

import (
	"context"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// fakeIndexer is a minimal Indexer test double for the read-pipeline tests in this
// package: it serves canned capabilities + releases and records the search query (and
// context) it received.
type fakeIndexer struct {
	info      IndexerInfo
	caps      *mapper.Capabilities
	releases  []*normalizer.Release
	searchErr error
	gotQuery  search.Query
	gotCtx    context.Context //nolint:containedctx // captured for cache-bypass assertions in tests
}

func (f *fakeIndexer) Info() IndexerInfo                  { return f.info }
func (f *fakeIndexer) Capabilities() *mapper.Capabilities { return f.caps }

func (f *fakeIndexer) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	f.gotCtx = ctx
	f.gotQuery = q
	return f.releases, f.searchErr
}

func (f *fakeIndexer) NeedsResolver() bool        { return false }
func (f *fakeIndexer) DownloadNeedsAuth() bool    { return false }
func (f *fakeIndexer) SupportsOffsetPaging() bool { return false }
func (f *fakeIndexer) ConsumesSearchMode() bool   { return false }

func (f *fakeIndexer) Grab(_ context.Context, _ string) (*search.GrabResult, error) {
	return &search.GrabResult{Body: []byte("d0:e"), ContentType: "application/x-bittorrent"}, nil
}

// testCaps builds capabilities for a demo indexer: categorymappings 1->Movies
// (+custom 100001 "HD Movies"), 2->TV; modes search, tv-search [q,season,tvdbid]
// (no imdbid), movie-search [q,imdbid].
func testCaps(t *testing.T) *mapper.Capabilities {
	t.Helper()
	def := &loader.Definition{
		ID:    "demo",
		Links: []string{"https://demo.test/"},
		Caps: loader.Caps{
			CategoryMappings: []loader.CategoryMapping{
				{ID: loader.Scalar{Value: "1", Set: true}, Cat: "Movies", Desc: "HD Movies"},
				{ID: loader.Scalar{Value: "2", Set: true}, Cat: "TV"},
			},
			// tv-search WITHOUT tvdbid/imdbid (like 1337x/eztv/limetorrents — the
			// majority of real trackers); movie-search WITH imdbid but no tmdbid.
			Modes: loader.Modes{
				Search:      []string{"q"},
				TVSearch:    []string{"q", "season", "ep"},
				MovieSearch: []string{"q", "imdbid"},
			},
		},
	}
	caps, err := mapper.Build(def)
	if err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}
	return caps
}

func demoRelease(title, link string, cats []int) *normalizer.Release {
	return &normalizer.Release{
		Title: title, Link: link, Size: 1024, Categories: cats,
		Seeders: 1, Peers: 1, PublishDate: "2024-01-02T03:04:05Z",
		DownloadVolumeFactor: 1, UploadVolumeFactor: 1,
	}
}
