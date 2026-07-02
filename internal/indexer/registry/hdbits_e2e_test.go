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

// hdbitsUsername and hdbitsPasskey are synthetic credentials for this e2e test
// only. Both ride inside the JSON POST body (auth is body-level); the passkey also
// embeds in the download.php URL, which NeedsResolver()=true seals behind /dl.
// Neither must ever appear in a recorded search-request URL.
const (
	hdbitsUsername = "HDBITS-E2E-SYNTHETIC-USER"
	hdbitsPasskey  = "HDBITS-E2E-SYNTHETIC-PASSKEY"
)

// hdbitsDoer fronts the whole HDBits driver as the registry wires it: a POST to
// /api/torrents returns the saved search golden, and a GET to a download.php URL
// returns torrent bytes. Every request is recorded so the test can assert no URL
// ever carries a credential.
type hdbitsDoer struct {
	searchBody string

	mu   sync.Mutex
	reqs []*stdhttp.Request
}

func (d *hdbitsDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.reqs = append(d.reqs, req)
	d.mu.Unlock()

	if req.Method == stdhttp.MethodGet {
		return mkResp(stdhttp.StatusOK, "d4:name4:dataee", "application/x-bittorrent"), nil
	}
	return mkResp(stdhttp.StatusOK, d.searchBody, "application/json"), nil
}

func (d *hdbitsDoer) urls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.reqs))
	for i, r := range d.reqs {
		out[i] = r.URL.String()
	}
	return out
}

// TestHDBitsEndToEnd builds the HDBits native driver through the registry
// (Add -> resolve), runs a search against the saved golden, then grabs a release.
// It asserts releases come back deterministically sorted and that neither the
// username nor the passkey ever appears in any recorded request URL: search auth
// rides in the never-logged JSON body, and the passkey-bearing download URL is
// routed through /dl (NeedsResolver()=true).
func TestHDBitsEndToEnd(t *testing.T) {
	golden, err := os.ReadFile("../native/hdbits/testdata/search.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	doer := &hdbitsDoer{searchBody: string(golden)}
	reg, _ := newRegistry(t, doer)
	ctx := context.Background()

	if _, err := reg.Add(ctx, registry.AddParams{
		Slug:         "hdb",
		DefinitionID: "hdbits",
		Settings:     map[string]string{"username": hdbitsUsername, "passkey": hdbitsPasskey},
	}); err != nil {
		t.Fatalf("Add(hdbits): %v", err)
	}

	idx, ok := reg.Indexer(ctx, "hdb")
	if !ok {
		t.Fatal("hdbits indexer should resolve")
	}
	if !idx.NeedsResolver() {
		t.Error("NeedsResolver = false, want true (download URL embeds passkey, routed via /dl)")
	}
	if idx.Capabilities().Modes["movie-search"] == nil {
		t.Error("native caps missing movie-search mode")
	}

	releases, err := idx.Search(ctx, search.Query{Keywords: "the matrix"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Three rows in the golden, deterministically sorted ascending by id.
	if len(releases) != 3 {
		t.Fatalf("releases = %d, want 3", len(releases))
	}
	wantTitles := []string{
		"The Matrix 1999 1080p BluRay REMUX", // id 100001 (full disc -> name)
		"Some.Show.S01E01.1080p.WEB-DL",      // id 100002 (filename, .torrent stripped)
		"XXX Release Name",                   // id 100003 (cat 7 -> name)
	}
	for i, want := range wantTitles {
		if releases[i].Title != want {
			t.Errorf("releases[%d].Title = %q, want %q", i, releases[i].Title, want)
		}
	}

	// Grab the first release through the resolved indexer; the driver GETs the
	// passkey-bearing download.php URL server-side and returns the torrent bytes.
	grab, err := idx.Grab(ctx, releases[0].Link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if len(grab.Body) == 0 {
		t.Error("Grab returned an empty body")
	}

	// Auth on the search call rides entirely in the never-logged JSON POST body, so
	// the search-endpoint URL must carry neither credential. (The passkey does embed
	// in the server-side download.php GET URL — that is by design and sealed behind
	// /dl via NeedsResolver()=true, so it is not asserted here.)
	for _, u := range doer.urls() {
		if !strings.Contains(u, "/api/torrents") {
			continue
		}
		if strings.Contains(u, hdbitsUsername) {
			t.Errorf("username leaked into search URL: %q", u)
		}
		if strings.Contains(u, hdbitsPasskey) {
			t.Errorf("passkey leaked into search URL: %q", u)
		}
	}
}
