package appsync

import (
	"testing"
)

func TestWhisparrBuildIndexerGolden(t *testing.T) {
	t.Parallel()
	drv := asServarr(t, NewWhisparr("http://whisparr:6969", "app-key", nil))
	d := DesiredIndexer{
		Slug: "xxx-tracker", Name: "XXX Tracker", Priority: 25, Enabled: true,
		EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
		FeedURL:    "http://harbrr:8787/api/indexers/xxx-tracker/results/torznab",
		APIKey:     "harbrr-feed-key",
		Categories: []Category{{6000, "XXX"}, {6010, "XXX/DVD"}, {6040, "XXX/x264"}},
	}
	assertGolden(t, "whisparr_create.golden.json", drv.buildIndexer(d))
}

func TestWhisparrBuildIndexerUsenetGolden(t *testing.T) {
	t.Parallel()
	drv := asServarr(t, NewWhisparr("http://whisparr:6969", "app-key", nil))
	d := DesiredIndexer{
		Slug: "xxx-tracker", Name: "XXX Tracker", Priority: 25, Enabled: true,
		EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
		FeedURL:    "http://harbrr:8787/api/indexers/xxx-tracker/results/torznab",
		APIKey:     "harbrr-feed-key",
		Categories: []Category{{6000, "XXX"}, {6010, "XXX/DVD"}, {6040, "XXX/x264"}},
		Protocol:   "usenet",
	}
	got := drv.buildIndexer(d)
	if got.Implementation != "Newznab" || got.ImplementationName != "Newznab" ||
		got.ConfigContract != "NewznabSettings" || got.Protocol != "usenet" {
		t.Errorf("usenet header wrong: impl=%q implName=%q cfg=%q proto=%q",
			got.Implementation, got.ImplementationName, got.ConfigContract, got.Protocol)
	}
	assertGolden(t, "whisparr_create_usenet.golden.json", got)
}

// TestWhisparrHasNoAnimeField locks anime=false: Whisparr must never emit animeCategories.
func TestWhisparrHasNoAnimeField(t *testing.T) {
	t.Parallel()
	drv := asServarr(t, NewWhisparr("http://whisparr:6969", "k", nil))
	for _, f := range drv.buildIndexer(desired("a", true)).Fields {
		if f.Name == "animeCategories" {
			t.Fatalf("whisparr must not push animeCategories")
		}
	}
}
