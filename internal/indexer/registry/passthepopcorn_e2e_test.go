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

// ptpAPIUser and ptpAPIKey are synthetic credentials for this e2e test only. Both
// are secrets that ride EXCLUSIVELY in the ApiUser / ApiKey request headers and must
// never appear in any request URL — the header-auth model (no passkey in URL) is the
// whole point of the driver.
const (
	ptpAPIUser = "PTP-E2E-SYNTHETIC-APIUSER"
	ptpAPIKey  = "PTP-E2E-SYNTHETIC-APIKEY"
)

// ptpDoer fronts the PassThePopcorn native driver as the registry wires it: a GET to
// torrents.php?action=advanced returns the saved search golden, and a GET to a
// torrent's download URL returns torrent bytes. Every request is recorded (URL +
// ApiUser/ApiKey headers) so the test can assert the per-site auth headers are
// attached and neither secret ever leaks into a request URL.
type ptpDoer struct {
	searchBody string

	mu   sync.Mutex
	reqs []ptpRecordedReq
}

type ptpRecordedReq struct {
	url     string
	apiUser string
	apiKey  string
}

func (d *ptpDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.reqs = append(d.reqs, ptpRecordedReq{
		url:     req.URL.String(),
		apiUser: req.Header.Get("ApiUser"),
		apiKey:  req.Header.Get("ApiKey"),
	})
	d.mu.Unlock()

	// A download GET carries action=download; everything else is the JSON search.
	if strings.Contains(req.URL.RawQuery, "action=download") {
		return ptpResp(stdhttp.StatusOK, "d4:name4:dataee", "application/x-bittorrent"), nil
	}
	return ptpResp(stdhttp.StatusOK, d.searchBody, "application/json"), nil
}

func (d *ptpDoer) records() []ptpRecordedReq {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]ptpRecordedReq, len(d.reqs))
	copy(out, d.reqs)
	return out
}

func ptpResp(status int, body, contentType string) *stdhttp.Response {
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

// TestPassThePopcornEndToEnd builds the PTP native driver through the registry
// (Add -> resolve), runs a search against the saved golden, then grabs a release
// through the resolved indexer. It asserts: releases come back sorted by PublishDate
// descending, the per-site ApiUser/ApiKey headers are attached to every request, and
// neither secret ever appears in any recorded request URL (header auth only — the
// download URL carries no passkey, so NeedsResolver is false).
func TestPassThePopcornEndToEnd(t *testing.T) {
	golden, err := os.ReadFile("../native/passthepopcorn/testdata/search_response.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	doer := &ptpDoer{searchBody: string(golden)}
	reg, _ := newRegistry(t, doer)
	ctx := context.Background()

	if _, err := reg.Add(ctx, registry.AddParams{
		Slug:         "ptp",
		DefinitionID: "passthepopcorn",
		Settings:     map[string]string{"apiuser": ptpAPIUser, "apikey": ptpAPIKey},
	}); err != nil {
		t.Fatalf("Add(passthepopcorn): %v", err)
	}

	idx, ok := reg.Indexer(ctx, "ptp")
	if !ok {
		t.Fatal("passthepopcorn indexer should resolve")
	}
	// Gazelle model: the download URL embeds no passkey, so it is safe in the feed
	// (NeedsResolver=false); auth is re-attached via headers at grab time.
	if idx.NeedsResolver() {
		t.Error("NeedsResolver = true, want false (no passkey in URL; header auth at grab time)")
	}
	if idx.Capabilities().Modes["movie-search"] == nil {
		t.Error("native caps missing movie-search mode")
	}

	releases, err := idx.Search(ctx, search.Query{Keywords: "the matrix"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Three torrents flatten to three releases, ordered by PublishDate descending
	// (mirroring Prowlarr): the 2021 collection sorts ahead of the 2018 and 2015
	// Matrix torrents.
	if len(releases) != 3 {
		t.Fatalf("releases = %d, want 3", len(releases))
	}
	wantTitles := []string{
		"Some Collection 2005 2160p UHD BluRay x265-GROUP", // 2021-11-20
		"The Matrix 1999 1080p BluRay x264-GROUP",          // 2018-05-01
		"The Matrix 1999 DVDRip XviD-GROUP",                // 2015-02-10
	}
	for i, want := range wantTitles {
		if releases[i].Title != want {
			t.Errorf("releases[%d].Title = %q, want %q", i, releases[i].Title, want)
		}
	}

	// Grab the first release through the resolved indexer; the driver GETs the
	// download URL server-side with the headers re-attached and returns the bytes.
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
		if r.apiUser != ptpAPIUser {
			t.Errorf("ApiUser header = %q, want %q", r.apiUser, ptpAPIUser)
		}
		if r.apiKey != ptpAPIKey {
			t.Errorf("ApiKey header = %q, want %q", r.apiKey, ptpAPIKey)
		}
		if strings.Contains(r.url, ptpAPIUser) || strings.Contains(r.url, ptpAPIKey) {
			t.Errorf("credential leaked into request URL: %q", r.url)
		}
	}
}
