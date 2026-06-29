package appsync

import (
	"testing"
)

func TestRadarrBuildIndexerGolden(t *testing.T) {
	t.Parallel()
	drv := asServarr(t, NewRadarr("http://radarr:7878", "app-key", nil))
	d := DesiredIndexer{
		Slug: "movie-tracker", Name: "Movie Tracker", Priority: 25, Enabled: true,
		FeedURL:    "http://harbrr:8787/api/v2.0/indexers/movie-tracker/results/torznab",
		APIKey:     "harbrr-feed-key",
		Categories: []Category{{2000, "Movies"}, {2040, "Movies/HD"}, {2060, "Movies/3D"}},
	}
	assertGolden(t, "radarr_create.golden.json", drv.buildIndexer(d))
}

// TestRadarrHasNoAnimeField guards the one behavioral difference from Sonarr.
func TestRadarrHasNoAnimeField(t *testing.T) {
	t.Parallel()
	drv := asServarr(t, NewRadarr("http://radarr:7878", "k", nil))
	for _, f := range drv.buildIndexer(desired("a", true)).Fields {
		if f.Name == "animeCategories" {
			t.Fatalf("radarr must not push animeCategories")
		}
	}
}
