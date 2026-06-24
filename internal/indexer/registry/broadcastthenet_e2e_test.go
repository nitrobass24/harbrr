package registry_test

import (
	"context"
	"io"
	stdhttp "net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// btnAPIKey is a synthetic API key for this e2e test only. It is the params[0]
// secret inside the JSON-RPC body and must never appear in any request URL.
const btnAPIKey = "BTN-E2E-SYNTHETIC-APIKEY"

// btnDoer is a single Doer that fronts the whole BroadcastTheNet driver as the
// registry wires it: a POST to the JSON-RPC endpoint returns the saved
// getTorrents golden, and a GET to a torrent's DownloadURL returns torrent bytes.
// Every request is recorded so the test can assert no URL ever carries the apikey.
type btnDoer struct {
	searchBody string

	mu   sync.Mutex
	reqs []*stdhttp.Request
}

func (d *btnDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.reqs = append(d.reqs, req)
	d.mu.Unlock()

	if req.Method == stdhttp.MethodGet {
		return mkResp(stdhttp.StatusOK, "d4:name4:dataee", "application/x-bittorrent"), nil
	}
	return mkResp(stdhttp.StatusOK, d.searchBody, "application/json"), nil
}

func (d *btnDoer) urls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.reqs))
	for i, r := range d.reqs {
		out[i] = r.URL.String()
	}
	return out
}

func mkResp(status int, body, contentType string) *stdhttp.Response {
	h := stdhttp.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &stdhttp.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestBroadcastTheNetEndToEnd builds the BTN native driver through the registry
// (Add -> resolve), runs a search against the saved getTorrents golden, then grabs
// a release through the resolved indexer. It asserts releases come back sorted by
// TorrentID and that the apikey never appears in any recorded request URL (it lives
// only in the JSON-RPC body, which is never logged).
func TestBroadcastTheNetEndToEnd(t *testing.T) {
	golden, err := os.ReadFile("../native/broadcastthenet/testdata/getTorrents_response.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	doer := &btnDoer{searchBody: string(golden)}
	reg, _ := newRegistry(t, doer)
	ctx := context.Background()

	if _, err := reg.Add(ctx, registry.AddParams{
		Slug:         "btn",
		DefinitionID: "broadcastthenet",
		Settings:     map[string]string{"apikey": btnAPIKey},
	}); err != nil {
		t.Fatalf("Add(broadcastthenet): %v", err)
	}

	idx, ok := reg.Indexer(ctx, "btn")
	if !ok {
		t.Fatal("broadcastthenet indexer should resolve")
	}
	if !idx.NeedsResolver() {
		t.Error("NeedsResolver = false, want true (DownloadURL embeds creds, routed via /dl)")
	}
	if idx.Capabilities().Modes["tv-search"] == nil {
		t.Error("native caps missing tv-search mode")
	}

	releases, err := idx.Search(ctx, search.Query{Keywords: "show"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Three torrents in the golden, deterministically sorted by numeric TorrentID.
	if len(releases) != 3 {
		t.Fatalf("releases = %d, want 3", len(releases))
	}
	wantTitles := []string{
		"Old.Show.S01E01.SDTV.XviD-GRP",             // 1555000
		"That.Show.S05E04.1080p.WEB-DL.H.264-NOGRP", // 1555073
		"New.Show.S02.2160p.BluRay.x265-GRP",        // 1555200
	}
	for i, want := range wantTitles {
		if releases[i].Title != want {
			t.Errorf("releases[%d].Title = %q, want %q", i, releases[i].Title, want)
		}
	}

	// Grab the first release through the resolved indexer; the driver GETs the
	// DownloadURL server-side and returns the torrent bytes.
	grab, err := idx.Grab(ctx, releases[0].Link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if len(grab.Body) == 0 {
		t.Error("Grab returned an empty body")
	}

	// The apikey lives only inside the JSON-RPC POST body — it must never appear in
	// any request URL (search endpoint or download URL).
	for _, u := range doer.urls() {
		if strings.Contains(u, btnAPIKey) {
			t.Errorf("apikey leaked into request URL: %q", u)
		}
	}
}
