package registry_test

import (
	"context"
	stdhttp "net/http"
	"os"
	"strings"
	"sync"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// abUsername / abPasskey are synthetic credentials for this e2e test only. For
// AnimeBytes BOTH ride in the scrape URL query (username) and the download URL path
// (passkey) — that is how the API works — so the redaction guarantee is that
// apphttp.RedactURL strips the passkey before any URL is logged, not that the passkey
// is absent from the raw request URL.
const (
	abUsername = "ab-e2e-user"
	abPasskey  = "PASSKEY32CHARSYNTHETICxxxxxxxxxx"
)

// abDoer fronts the whole AnimeBytes driver as the registry wires it: a GET to
// scrape.php returns the saved scrape golden, and a GET to a torrent download URL
// returns torrent bytes. Every request URL is recorded so the test can assert the
// raw scrape URL carries the passkey (required) but RedactURL strips it.
type abDoer struct {
	scrapeBody string

	mu   sync.Mutex
	urls []string
}

func (d *abDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.urls = append(d.urls, req.URL.String())
	d.mu.Unlock()

	if strings.Contains(req.URL.Path, "/download/") {
		return mkResp(stdhttp.StatusOK, "d4:name4:dataee", "application/x-bittorrent"), nil
	}
	return mkResp(stdhttp.StatusOK, d.scrapeBody, "application/json"), nil
}

func (d *abDoer) recordedURLs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.urls))
	copy(out, d.urls)
	return out
}

// TestAnimeBytesEndToEnd builds the AnimeBytes native driver through the registry
// (Add -> resolve), runs a search against the saved scrape golden, and grabs a
// release through the resolved indexer. It asserts releases come back with the
// synthesized titles and that, although the scrape URL legitimately carries the
// passkey in its query (the auth model), apphttp.RedactURL strips it so no logged
// form of any request URL leaks the secret.
func TestAnimeBytesEndToEnd(t *testing.T) {
	golden, err := os.ReadFile("../native/animebytes/testdata/scrape_response.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	doer := &abDoer{scrapeBody: string(golden)}
	reg, _ := newRegistry(t, doer)
	ctx := context.Background()

	if _, err := reg.Add(ctx, registry.AddParams{
		Slug:         "ab",
		DefinitionID: "animebytes",
		Settings:     map[string]string{"username": abUsername, "passkey": abPasskey},
	}); err != nil {
		t.Fatalf("Add(animebytes): %v", err)
	}

	idx, ok := reg.Indexer(ctx, "ab")
	if !ok {
		t.Fatal("animebytes indexer should resolve")
	}
	if !idx.NeedsResolver() {
		t.Error("NeedsResolver = false, want true (download Link embeds the passkey, routed via /dl)")
	}
	if idx.Capabilities().Modes["tv-search"] == nil {
		t.Error("native caps missing tv-search mode")
	}

	releases, err := idx.Search(ctx, search.Query{Keywords: "anime"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Two groups in the golden, one torrent each, flatten to two releases (sorted by
	// PublishDate descending: the 2020 anime ahead of the 2019 OST).
	if len(releases) != 2 {
		t.Fatalf("releases = %d, want 2", len(releases))
	}
	wantTitles := []string{
		"[SubGroup] Great BluRay SoftSubbed Anime S01 [Blu-ray][MKV][h264 10-bit][1080p][FLAC 2.0][Dual Audio][Softsubs (SubGroup)]",
		"Some OST  [MP3][320][CD]",
	}
	for i, want := range wantTitles {
		if releases[i].Title != want {
			t.Errorf("releases[%d].Title = %q, want %q", i, releases[i].Title, want)
		}
	}

	// Grab the first release through the resolved indexer; the driver GETs the
	// passkey-bearing download URL server-side and returns the torrent bytes.
	grab, err := idx.Grab(ctx, releases[0].Link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if len(grab.Body) == 0 {
		t.Error("Grab returned an empty body")
	}

	assertNoPasskeyLeak(t, doer.recordedURLs())
}

// assertNoPasskeyLeak verifies the redaction guarantee for AnimeBytes. The scrape URL
// carries the passkey as a query param (the auth model), and apphttp.RedactURL — which
// scrubs known secret query params — strips it from that URL's logged form. The
// download URL instead embeds the passkey in its PATH, which RedactURL cannot scrub;
// the driver's guarantee there is that grab.go never surfaces that URL in any error or
// log (see sanitizeGrabError), not that RedactURL neutralizes it. So we assert
// query-redaction on the scrape URL and confirm the download URL is path-embedded.
func assertNoPasskeyLeak(t *testing.T, urls []string) {
	t.Helper()
	if len(urls) == 0 {
		t.Fatal("no requests recorded")
	}
	var sawScrapeWithPasskey, sawDownloadPathPasskey bool
	for _, u := range urls {
		switch {
		case strings.Contains(u, "scrape.php"):
			if !strings.Contains(u, abPasskey) {
				t.Errorf("scrape URL missing the passkey query the auth model needs: %q", u)
			}
			sawScrapeWithPasskey = true
			if redacted := apphttp.RedactURL(u); strings.Contains(redacted, abPasskey) {
				t.Errorf("passkey survived RedactURL on the scrape URL: %q", redacted)
			}
		case strings.Contains(u, "/download/"+abPasskey):
			sawDownloadPathPasskey = true
		}
	}
	if !sawScrapeWithPasskey {
		t.Error("expected a scrape.php request carrying the passkey in its query")
	}
	if !sawDownloadPathPasskey {
		t.Error("expected a download request with the passkey embedded in the URL path")
	}
}
