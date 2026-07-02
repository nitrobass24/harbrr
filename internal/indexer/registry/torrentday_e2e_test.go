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

// torrentdayCookie is a synthetic session cookie for this e2e test only. TorrentDay
// authenticates with a static session cookie carried in the Cookie REQUEST HEADER —
// never in the URL — so it must appear in the request headers but never in any
// recorded request URL (search /t.json or the /download.php grab).
const torrentdayCookie = "uid=999999; pass=TD-E2E-SYNTHETIC-COOKIE-0000000000"

// torrentdayDoer fronts the whole TorrentDay driver as the registry wires it: a GET to
// /t.json returns the saved search golden (a flat JSON array), and a GET to a
// /download.php URL returns torrent bytes. Every request is recorded so the test can
// assert the cookie rides in the header and never leaks into a URL.
type torrentdayDoer struct {
	searchBody string

	mu   sync.Mutex
	reqs []*stdhttp.Request
}

func (d *torrentdayDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.reqs = append(d.reqs, req)
	d.mu.Unlock()

	if strings.Contains(req.URL.Path, "/t.json") {
		return mkResp(stdhttp.StatusOK, d.searchBody, "application/json"), nil
	}
	return mkResp(stdhttp.StatusOK, "d4:name4:dataee", "application/x-bittorrent"), nil
}

func (d *torrentdayDoer) requests() []*stdhttp.Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]*stdhttp.Request, len(d.reqs))
	copy(out, d.reqs)
	return out
}

// TestTorrentDayEndToEnd builds the TorrentDay native driver through the registry
// (Add -> resolve), runs a search against the saved golden, then grabs a release
// through the resolved indexer. It asserts the session cookie is sent in the Cookie
// header (so auth is applied server-side) and never appears in any recorded request
// URL — the download is cookie-authenticated and routed through /dl (NeedsResolver()=true).
func TestTorrentDayEndToEnd(t *testing.T) {
	golden, err := os.ReadFile("../native/torrentday/testdata/search_results.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	doer := &torrentdayDoer{searchBody: string(golden)}
	reg, _ := newRegistry(t, doer)
	ctx := context.Background()

	if _, err := reg.Add(ctx, registry.AddParams{
		Slug:         "td",
		DefinitionID: "torrentday",
		Settings:     map[string]string{"cookie": torrentdayCookie},
	}); err != nil {
		t.Fatalf("Add(torrentday): %v", err)
	}

	idx, ok := reg.Indexer(ctx, "td")
	if !ok {
		t.Fatal("torrentday indexer should resolve")
	}
	if !idx.NeedsResolver() {
		t.Error("NeedsResolver = false, want true (download needs the session cookie, routed via /dl)")
	}
	if len(idx.Capabilities().Modes) == 0 {
		t.Error("native caps advertise no search modes")
	}

	releases, err := idx.Search(ctx, search.Query{Keywords: "some"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases = %d, want 2", len(releases))
	}
	wantTitles := map[string]bool{
		"Some Movie 2024 2160p UHD BluRay x265-GROUP":    true,
		"Some Show S03E04 1080p WEB-DL DDP5.1 H264-CREW": true,
	}
	for _, r := range releases {
		if !wantTitles[r.Title] {
			t.Errorf("unexpected release title %q", r.Title)
		}
	}

	// Grab the first release; the driver GETs the /download.php URL with the cookie
	// header attached server-side and returns the torrent bytes.
	grab, err := idx.Grab(ctx, releases[0].Link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if len(grab.Body) == 0 {
		t.Error("Grab returned an empty body")
	}

	// The cookie must ride in the Cookie header on every request and never appear in
	// any request URL (search or download).
	reqs := doer.requests()
	if len(reqs) == 0 {
		t.Fatal("no requests recorded")
	}
	for _, r := range reqs {
		if r.Header.Get("Cookie") != torrentdayCookie {
			t.Errorf("request %s carried Cookie %q, want the configured cookie", r.URL.Path, r.Header.Get("Cookie"))
		}
		if strings.Contains(r.URL.String(), "TD-E2E-SYNTHETIC-COOKIE") {
			t.Errorf("cookie leaked into request URL: %q", r.URL.String())
		}
	}
}
