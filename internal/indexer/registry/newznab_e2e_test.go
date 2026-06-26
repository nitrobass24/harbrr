package registry_test

import (
	"context"
	stdhttp "net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// newznabAPIKey is a synthetic apikey for this e2e test only. It is appended to every
// outbound Newznab request URL (?t=caps / ?t=search / the .nzb GET) and so is a
// secret-bearing query param: it MUST never reach the served feed (a Release.Link), which
// the driver guarantees via DownloadNeedsAuth()=true routing the download through /dl.
const newznabAPIKey = "NEWZNAB-E2E-SYNTHETIC-APIKEY"

// newznabDoer fronts the whole Newznab driver as the registry wires it: a ?t=caps request
// returns the caps golden, a ?t=search request returns the search golden, and a GET to a
// getnzb URL returns .nzb bytes. Every request is recorded so the test can assert the
// apikey only ever leaves harbrr on outbound requests, never in a served result.
type newznabDoer struct {
	caps   string
	search string
	nzb    string

	mu   sync.Mutex
	reqs []*stdhttp.Request
}

func (d *newznabDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.reqs = append(d.reqs, req)
	d.mu.Unlock()

	switch {
	case strings.Contains(req.URL.Path, "getnzb"):
		return mkResp(stdhttp.StatusOK, d.nzb, "application/x-nzb"), nil
	case req.URL.Query().Get("t") == "caps":
		return mkResp(stdhttp.StatusOK, d.caps, "application/xml"), nil
	default:
		return mkResp(stdhttp.StatusOK, d.search, "application/rss+xml"), nil
	}
}

func (d *newznabDoer) urls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.reqs))
	for i, r := range d.reqs {
		out[i] = r.URL.String()
	}
	return out
}

// TestNewznabEndToEnd builds the generic Newznab usenet indexer AND a preset through the
// registry (Add -> resolve), runs a search against the saved goldens for each, then grabs a
// release. It asserts:
//   - the registry adapter path yields Protocol=usenet for both (the Leaf 1 plumbing);
//   - the configured apikey never appears in any served Release.Link (the redaction gate:
//     the apikey-bearing .nzb URL is sealed behind /dl by DownloadNeedsAuth()=true, never
//     emitted bare in the served feed);
//   - the apikey IS present on the outbound request URLs (it must reach the remote server).
func TestNewznabEndToEnd(t *testing.T) {
	caps, err := os.ReadFile("../native/newznab/testdata/caps.xml")
	if err != nil {
		t.Fatalf("read caps golden: %v", err)
	}
	searchXML, err := os.ReadFile("../native/newznab/testdata/search.xml")
	if err != nil {
		t.Fatalf("read search golden: %v", err)
	}
	nzb, err := os.ReadFile("../native/newznab/testdata/sample.nzb")
	if err != nil {
		t.Fatalf("read nzb golden: %v", err)
	}

	doer := &newznabDoer{caps: string(caps), search: string(searchXML), nzb: string(nzb)}
	reg, _ := newRegistry(t, doer)

	// Generic Newznab needs an explicit base URL (it ships none); a preset ships its own.
	addNewznab(t, reg, "nzb-generic", "newznab", "https://news.example.test")
	addNewznab(t, reg, "nzb-preset", "nzbgeek", "")

	for _, slug := range []string{"nzb-generic", "nzb-preset"} {
		runNewznabSlug(t, reg, slug)
	}

	// The apikey must reach the remote server: at least one outbound URL carries it.
	if !anyContains(doer.urls(), newznabAPIKey) {
		t.Errorf("apikey never sent on any outbound request URL; the driver must send it to the remote")
	}
}

// addNewznab adds a Newznab instance and asserts the registry persisted Protocol=usenet.
func addNewznab(t *testing.T, reg *registry.Registry, slug, defID, baseURL string) {
	t.Helper()
	inst, err := reg.Add(context.Background(), registry.AddParams{
		Slug:         slug,
		DefinitionID: defID,
		BaseURL:      baseURL,
		Settings:     map[string]string{"apikey": newznabAPIKey},
	})
	if err != nil {
		t.Fatalf("Add(%s): %v", defID, err)
	}
	if inst.Protocol != loader.ProtocolUsenet {
		t.Errorf("instance %q protocol = %q, want usenet (registry must copy def.EffectiveProtocol)", slug, inst.Protocol)
	}
}

// runNewznabSlug resolves a Newznab slug, asserts its usenet characteristics, searches,
// grabs, and proves the configured apikey never leaks into a served Release.Link.
func runNewznabSlug(t *testing.T, reg *registry.Registry, slug string) {
	t.Helper()
	ctx := context.Background()

	idx, ok := reg.Indexer(ctx, slug)
	if !ok {
		t.Fatalf("%s should resolve", slug)
	}
	if idx.NeedsResolver() {
		t.Errorf("%s NeedsResolver = true, want false (no magnet/resolve step)", slug)
	}
	if idx.Capabilities().Modes["tv-search"] == nil {
		t.Errorf("%s caps missing tv-search mode", slug)
	}

	releases, err := idx.Search(ctx, search.Query{Keywords: "example"})
	if err != nil {
		t.Fatalf("%s Search: %v", slug, err)
	}
	// Two usable items in the golden; the third has no .nzb enclosure and is skipped.
	if len(releases) != 2 {
		t.Fatalf("%s releases = %d, want 2", slug, len(releases))
	}

	// The redaction gate: no served result link may carry the configured apikey. The
	// apikey-bearing download is sealed behind /dl (DownloadNeedsAuth()=true).
	for i, rel := range releases {
		if strings.Contains(rel.Link, newznabAPIKey) {
			t.Errorf("%s releases[%d].Link leaked the apikey: %q", slug, i, rel.Link)
		}
	}

	// Grab the first release: the driver GETs the apikey-bearing .nzb URL server-side and
	// returns the bytes with the usenet content type — never a redirect that would leak it.
	grab, err := idx.Grab(ctx, releases[0].Link)
	if err != nil {
		t.Fatalf("%s Grab: %v", slug, err)
	}
	if len(grab.Body) == 0 {
		t.Errorf("%s Grab returned an empty body", slug)
	}
	if grab.ContentType != "application/x-nzb" {
		t.Errorf("%s Grab ContentType = %q, want application/x-nzb", slug, grab.ContentType)
	}
	if grab.Redirect != "" {
		t.Errorf("%s Grab returned a redirect %q; an apikey-bearing URL must be proxied, never redirected", slug, grab.Redirect)
	}
}

// anyContains reports whether any string in xs contains sub.
func anyContains(xs []string, sub string) bool {
	for _, x := range xs {
		if strings.Contains(x, sub) {
			return true
		}
	}
	return false
}
