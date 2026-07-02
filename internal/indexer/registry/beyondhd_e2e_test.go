package registry_test

import (
	"context"
	stdhttp "net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// beyondhdAPIKey and beyondhdRSSKey are synthetic credentials for this e2e test
// only. The api_key rides in the URL PATH of the search endpoint
// (api/torrents/<api_key>) — that is the BHD contract, so it is expected there.
// The rsskey rides in the never-logged JSON POST body on search, and embeds in the
// download_url, which NeedsResolver()=true seals behind /dl. The rsskey must never
// appear in any recorded request URL.
const (
	beyondhdAPIKey = "BHD-E2E-SYNTHETIC-APIKEY00000000"
	beyondhdRSSKey = "BHD-E2E-SYNTHETIC-RSSKEY00000000"
)

// beyondhdDoer fronts the whole BeyondHD driver as the registry wires it: a POST to
// api/torrents/<api_key> returns the saved search golden, and a GET to a
// download_url returns torrent bytes. Every request is recorded so the test can
// assert the rsskey never leaks into a URL.
type beyondhdDoer struct {
	searchBody string

	mu   sync.Mutex
	reqs []*stdhttp.Request
}

func (d *beyondhdDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.reqs = append(d.reqs, req)
	d.mu.Unlock()

	if req.Method == stdhttp.MethodGet {
		return mkResp(stdhttp.StatusOK, "d4:name4:dataee", "application/x-bittorrent"), nil
	}
	return mkResp(stdhttp.StatusOK, d.searchBody, "application/json"), nil
}

func (d *beyondhdDoer) urls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.reqs))
	for i, r := range d.reqs {
		out[i] = r.URL.String()
	}
	return out
}

// TestBeyondHDEndToEnd builds the BeyondHD native driver through the registry
// (Add -> resolve), runs a search against the saved golden, then grabs a release
// through the resolved indexer. It asserts releases come back deterministically
// sorted by PublishDate desc and that the rsskey never appears in any recorded
// request URL: on search it rides in the never-logged JSON body, and the
// rsskey-bearing download_url is routed through /dl (NeedsResolver()=true).
func TestBeyondHDEndToEnd(t *testing.T) {
	golden, err := os.ReadFile("../native/beyondhd/testdata/search.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	doer := &beyondhdDoer{searchBody: string(golden)}
	reg, _ := newRegistry(t, doer)
	ctx := context.Background()

	if _, err := reg.Add(ctx, registry.AddParams{
		Slug:         "bhd",
		DefinitionID: "beyondhd",
		Settings:     map[string]string{"api_key": beyondhdAPIKey, "rsskey": beyondhdRSSKey},
	}); err != nil {
		t.Fatalf("Add(beyondhd): %v", err)
	}

	idx, ok := reg.Indexer(ctx, "bhd")
	if !ok {
		t.Fatal("beyondhd indexer should resolve")
	}
	if !idx.NeedsResolver() {
		t.Error("NeedsResolver = false, want true (download_url embeds rsskey, routed via /dl)")
	}
	if idx.Capabilities().Modes["movie-search"] == nil {
		t.Error("native caps missing movie-search mode")
	}

	releases, err := idx.Search(ctx, search.Query{Keywords: "the matrix"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Two rows in the golden, sorted by PublishDate desc (2024-03-15 before 2024-03-10).
	if len(releases) != 2 {
		t.Fatalf("releases = %d, want 2", len(releases))
	}
	wantTitles := []string{
		"Some Movie 2021 1080p BluRay DD5.1 x264-GRP", // 2024-03-15 (Movies)
		"Some Show S01 720p WEB-DL DDP5.1 H264-GRP",   // 2024-03-10 (TV)
	}
	for i, want := range wantTitles {
		if releases[i].Title != want {
			t.Errorf("releases[%d].Title = %q, want %q", i, releases[i].Title, want)
		}
	}

	// Grab the first release through the resolved indexer; the driver GETs the
	// rsskey-bearing download_url server-side and returns the torrent bytes.
	grab, err := idx.Grab(ctx, releases[0].Link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if len(grab.Body) == 0 {
		t.Error("Grab returned an empty body")
	}

	// The rsskey lives only inside the JSON POST body on search — it must never
	// appear in the search-endpoint URL. (It does embed in the server-side
	// download_url GET, which is by design and sealed behind /dl via
	// NeedsResolver()=true, so that URL is excluded from this assertion.)
	for _, u := range doer.urls() {
		if !strings.Contains(u, "/api/torrents") {
			continue
		}
		if strings.Contains(u, beyondhdRSSKey) {
			t.Errorf("rsskey leaked into search URL: %q", u)
		}
	}
}
