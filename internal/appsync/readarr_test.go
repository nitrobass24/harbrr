package appsync

import (
	"testing"
)

func TestReadarrBuildIndexerGolden(t *testing.T) {
	t.Parallel()
	drv := newServarr("readarr", "http://readarr:8787", "app-key", nil, false, servarrIndexerPathV1)
	d := DesiredIndexer{
		Slug: "book-tracker", Name: "Book Tracker", Priority: 25, Enabled: true,
		FeedURL:    "http://harbrr:8787/api/v2.0/indexers/book-tracker/results/torznab",
		APIKey:     "harbrr-feed-key",
		Categories: []Category{{7000, "Books"}, {7020, "Books/EBook"}, {8010, "Books/Comics"}},
	}
	assertGolden(t, "readarr_create.golden.json", drv.buildIndexer(d))
}

func TestReadarrBuildIndexerUsenetGolden(t *testing.T) {
	t.Parallel()
	drv := newServarr("readarr", "http://readarr:8787", "app-key", nil, false, servarrIndexerPathV1)
	d := DesiredIndexer{
		Slug: "book-tracker", Name: "Book Tracker", Priority: 25, Enabled: true,
		FeedURL:    "http://harbrr:8787/api/v2.0/indexers/book-tracker/results/torznab",
		APIKey:     "harbrr-feed-key",
		Categories: []Category{{7000, "Books"}, {7020, "Books/EBook"}, {8010, "Books/Comics"}},
		Protocol:   "usenet",
	}
	got := drv.buildIndexer(d)
	if got.Implementation != "Newznab" || got.ImplementationName != "Newznab" ||
		got.ConfigContract != "NewznabSettings" || got.Protocol != "usenet" {
		t.Errorf("usenet header wrong: impl=%q implName=%q cfg=%q proto=%q",
			got.Implementation, got.ImplementationName, got.ConfigContract, got.Protocol)
	}
	assertGolden(t, "readarr_create_usenet.golden.json", got)
}

// TestReadarrHasNoAnimeField locks anime=false: Readarr must never emit animeCategories.
func TestReadarrHasNoAnimeField(t *testing.T) {
	t.Parallel()
	drv := newServarr("readarr", "http://readarr:8787", "k", nil, false, servarrIndexerPathV1)
	for _, f := range drv.buildIndexer(desired("a", true)).Fields {
		if f.Name == "animeCategories" {
			t.Fatalf("readarr must not push animeCategories")
		}
	}
}
