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

// ggnAPIKey is a synthetic API key for this e2e test only. It rides EXCLUSIVELY in the
// X-API-Key request header and must never appear in any request URL — header auth is the
// whole point of the driver, so the apikey leaking into a URL would be a redaction bug.
const ggnAPIKey = "GGN-E2E-SYNTHETIC-APIKEY"

// ggnPasskey is the synthetic download passkey the mocked request=quick_user returns. It
// must end up in every served download URL's torrent_pass; an empty torrent_pass would mean
// the passkey fetch never ran and grabs would silently fail at the tracker.
const ggnPasskey = "GGN-E2E-SYNTHETIC-PASSKEY"

// ggnDoer fronts the GazelleGames native driver as the registry wires it: a GET to
// api.php?request=search returns the saved search golden, and a GET to the rebuilt
// torrents.php?action=download URL returns torrent bytes. Every request is recorded (URL
// + X-API-Key header) so the test can assert the per-site auth header is attached and the
// apikey never leaks into a URL.
type ggnDoer struct {
	searchBody string

	mu   sync.Mutex
	reqs []ggnRecordedReq
}

type ggnRecordedReq struct {
	url    string
	apiKey string
}

func (d *ggnDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.reqs = append(d.reqs, ggnRecordedReq{
		url:    req.URL.String(),
		apiKey: req.Header.Get("X-API-Key"),
	})
	d.mu.Unlock()

	// A quick_user GET fetches the download passkey; a download GET carries action=download;
	// everything else is the JSON search.
	switch {
	case strings.Contains(req.URL.RawQuery, "request=quick_user"):
		return ggnResp(stdhttp.StatusOK, `{"status":"success","response":{"passkey":"`+ggnPasskey+`"}}`, "application/json"), nil
	case strings.Contains(req.URL.RawQuery, "action=download"):
		return ggnResp(stdhttp.StatusOK, "d4:name4:dataee", "application/x-bittorrent"), nil
	default:
		return ggnResp(stdhttp.StatusOK, d.searchBody, "application/json"), nil
	}
}

func (d *ggnDoer) records() []ggnRecordedReq {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]ggnRecordedReq, len(d.reqs))
	copy(out, d.reqs)
	return out
}

func ggnResp(status int, body, contentType string) *stdhttp.Response {
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

// TestGazelleGamesEndToEnd builds the GazelleGames native driver through the registry
// (Add -> resolve), runs a search against the saved golden, then grabs a release through
// the resolved indexer. It asserts: releases come back flattened and sorted by
// PublishDate descending, the X-API-Key header is attached to every request, and the
// apikey never appears in any recorded request URL (it rides only in the header, which is
// never logged). The download URL embeds the passkey, so NeedsResolver is true and the
// driver fetches the torrent server-side.
func TestGazelleGamesEndToEnd(t *testing.T) {
	golden, err := os.ReadFile("../native/gazellegames/testdata/search.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	doer := &ggnDoer{searchBody: string(golden)}
	reg, _ := newRegistry(t, doer)
	ctx := context.Background()

	if _, err := reg.Add(ctx, registry.AddParams{
		Slug:         "ggn",
		DefinitionID: "gazellegames",
		Settings:     map[string]string{"apikey": ggnAPIKey},
	}); err != nil {
		t.Fatalf("Add(gazellegames): %v", err)
	}

	idx, ok := reg.Indexer(ctx, "ggn")
	if !ok {
		t.Fatal("gazellegames indexer should resolve")
	}
	// The download URL embeds the passkey in torrent_pass, so the served feed routes
	// the download through /dl and the driver fetches it server-side.
	if !idx.NeedsResolver() {
		t.Error("NeedsResolver = false, want true (download URL embeds the passkey, routed via /dl)")
	}
	if idx.Capabilities().Modes["search"] == nil {
		t.Error("native caps missing search mode")
	}

	releases, err := idx.Search(ctx, search.Query{Keywords: "cool game"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// The multi-group golden flattens to three TORRENT rows, ordered by PublishDate
	// descending (mirroring Prowlarr): the 2020 Director's Cut sorts ahead of the 2018
	// release, then the 2016 manual.
	if len(releases) != 3 {
		t.Fatalf("releases = %d, want 3", len(releases))
	}
	wantTitles := []string{
		"Cool Game (2018) [Director's Cut 2020] [Rip FitGirl / Some Studio / DLC / Trumpable] [Update]",
		"Cool Game (2018) [ISO Clone / Some Studio / English / Region Free]",
		"A Manual 2016 [EPUB Retail / Book House]",
	}
	for i, want := range wantTitles {
		if releases[i].Title != want {
			t.Errorf("releases[%d].Title = %q, want %q", i, releases[i].Title, want)
		}
	}

	// The served download URL must carry the passkey fetched via request=quick_user; an
	// empty torrent_pass would mean the fetch never ran and the grab would 401 at GGn.
	if !strings.Contains(releases[0].Link, "torrent_pass="+ggnPasskey) {
		t.Errorf("download Link missing the fetched passkey: %q", releases[0].Link)
	}

	// Grab the first release through the resolved indexer; the driver GETs the rebuilt
	// torrents.php download URL server-side (X-API-Key header attached) and returns the
	// torrent bytes.
	grab, err := idx.Grab(ctx, releases[0].Link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if len(grab.Body) == 0 {
		t.Error("Grab returned an empty body")
	}

	recs := doer.records()
	if len(recs) == 0 {
		t.Fatal("no requests recorded")
	}
	for _, r := range recs {
		if r.apiKey != ggnAPIKey {
			t.Errorf("X-API-Key header = %q, want %q", r.apiKey, ggnAPIKey)
		}
		if strings.Contains(r.url, ggnAPIKey) {
			t.Errorf("apikey leaked into request URL: %q", r.url)
		}
	}
}
