package appsync

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestLidarrBuildIndexerGolden(t *testing.T) {
	t.Parallel()
	drv := newServarr("lidarr", "http://lidarr:8686", "app-key", nil, false, servarrIndexerPathV1)
	d := DesiredIndexer{
		Slug: "music-tracker", Name: "Music Tracker", Priority: 25, Enabled: true,
		FeedURL:    "http://harbrr:8787/api/v2.0/indexers/music-tracker/results/torznab",
		APIKey:     "harbrr-feed-key",
		Categories: []Category{{3000, "Audio"}, {3010, "Audio/MP3"}, {3040, "Audio/Lossless"}},
	}
	assertGolden(t, "lidarr_create.golden.json", drv.buildIndexer(d))
}

func TestLidarrBuildIndexerUsenetGolden(t *testing.T) {
	t.Parallel()
	drv := newServarr("lidarr", "http://lidarr:8686", "app-key", nil, false, servarrIndexerPathV1)
	d := DesiredIndexer{
		Slug: "music-tracker", Name: "Music Tracker", Priority: 25, Enabled: true,
		FeedURL:    "http://harbrr:8787/api/v2.0/indexers/music-tracker/results/torznab",
		APIKey:     "harbrr-feed-key",
		Categories: []Category{{3000, "Audio"}, {3010, "Audio/MP3"}, {3040, "Audio/Lossless"}},
		Protocol:   "usenet",
	}
	got := drv.buildIndexer(d)
	if got.Implementation != "Newznab" || got.ImplementationName != "Newznab" ||
		got.ConfigContract != "NewznabSettings" || got.Protocol != "usenet" {
		t.Errorf("usenet header wrong: impl=%q implName=%q cfg=%q proto=%q",
			got.Implementation, got.ImplementationName, got.ConfigContract, got.Protocol)
	}
	assertGolden(t, "lidarr_create_usenet.golden.json", got)
}

// TestLidarrHasNoAnimeField locks anime=false: Lidarr must never emit animeCategories.
func TestLidarrHasNoAnimeField(t *testing.T) {
	t.Parallel()
	drv := newServarr("lidarr", "http://lidarr:8686", "k", nil, false, servarrIndexerPathV1)
	for _, f := range drv.buildIndexer(desired("a", true)).Fields {
		if f.Name == "animeCategories" {
			t.Fatalf("lidarr must not push animeCategories")
		}
	}
}

// TestLidarrLifecycleV1 is the only test that proves the /api/v1 path wiring: a Lidarr
// driver must drive Create/List/Update/Test/Delete against the v1 stub routes.
func TestLidarrLifecycleV1(t *testing.T) {
	t.Parallel()
	stub := newServarrStub(t)
	srv := httptest.NewServer(stub.handler("/api/v1/indexer"))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	drv := NewLidarr(srv.URL, "app-key-123", srv.Client())

	id, err := drv.Create(ctx, desired("music-tracker", true))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "1" {
		t.Fatalf("Create id = %q, want 1", id)
	}
	if stub.lastAuth != "app-key-123" {
		t.Errorf("X-Api-Key = %q, want app-key-123", stub.lastAuth)
	}

	remote, err := drv.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(remote) != 1 || remote[0].ManagedBySlug != "music-tracker" || remote[0].RemoteID != "1" {
		t.Fatalf("List = %+v, want one managed row slug=music-tracker id=1", remote)
	}

	if err := drv.Update(ctx, "1", desired("music-tracker", false)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var sent servarrIndexer
	if err := json.Unmarshal(stub.lastBody, &sent); err != nil {
		t.Fatalf("decode update body: %v", err)
	}
	if sent.ID != 1 || sent.EnableRss {
		t.Errorf("Update body id=%d enableRss=%v, want id=1 disabled", sent.ID, sent.EnableRss)
	}

	if err := drv.Test(ctx, desired("music-tracker", true)); err != nil {
		t.Fatalf("Test: %v", err)
	}

	if err := drv.Delete(ctx, "1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if remote, _ := drv.List(ctx); len(remote) != 0 {
		t.Errorf("indexer survived Delete: %+v", remote)
	}
}
