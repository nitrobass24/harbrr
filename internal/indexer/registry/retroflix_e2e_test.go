package registry_test

import (
	"fmt"
	stdhttp "net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

const (
	retroEmail    = "retroflix-e2e@example.invalid"
	retroPassword = "RETROFLIX-E2E-SYNTHETIC-PASSWORD"
	retroToken    = "RETROFLIX-E2E-SYNTHETIC-TOKEN"
)

type retroFlixDoer struct {
	searchBody string
	rejectGrab atomic.Bool

	mu      sync.Mutex
	records []retroFlixRequest
}

type retroFlixRequest struct {
	url           string
	path          string
	authorization string
}

func (d *retroFlixDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.records = append(d.records, retroFlixRequest{
		url:           req.URL.String(),
		path:          req.URL.Path,
		authorization: req.Header.Get("Authorization"),
	})
	d.mu.Unlock()

	switch {
	case req.URL.Path == "/api/login":
		return mkResp(stdhttp.StatusOK, `{"token":"`+retroToken+`"}`, "application/json"), nil
	case req.URL.Path == "/api/torrent":
		return mkResp(stdhttp.StatusOK, d.searchBody, "application/json"), nil
	case strings.HasSuffix(req.URL.Path, "/download"):
		if d.rejectGrab.Load() {
			return mkResp(stdhttp.StatusForbidden, `{"message":"rejected `+retroToken+`"}`, "application/json"), nil
		}
		return mkResp(stdhttp.StatusOK, "d4:name5:retroe", "application/x-bittorrent"), nil
	default:
		return nil, fmt.Errorf("unexpected RetroFlix request path %q", req.URL.Path)
	}
}

func (d *retroFlixDoer) snapshot() []retroFlixRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]retroFlixRequest(nil), d.records...)
}

func TestRetroFlixEndToEnd(t *testing.T) {
	body, err := os.ReadFile("../native/speedapp/testdata/search_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doer := &retroFlixDoer{searchBody: string(body)}
	reg, _ := newRegistry(t, doer)
	ctx := t.Context()

	if _, err := reg.Add(ctx, registry.AddParams{
		Slug:         "retro",
		DefinitionID: "retroflix",
		Settings:     map[string]string{"email": retroEmail, "password": retroPassword},
	}); err != nil {
		t.Fatalf("Add(retroflix): %v", err)
	}
	idx, ok := reg.Indexer(ctx, "retro")
	if !ok {
		t.Fatal("retroflix indexer should resolve")
	}
	if idx.NeedsResolver() || !idx.DownloadNeedsAuth() || !idx.SupportsOffsetPaging() {
		t.Errorf("flags resolver=%v downloadAuth=%v paging=%v",
			idx.NeedsResolver(), idx.DownloadNeedsAuth(), idx.SupportsOffsetPaging())
	}
	if idx.Capabilities().Modes["movie-search"] == nil || idx.Capabilities().Modes["tv-search"] == nil {
		t.Errorf("caps modes = %v", idx.Capabilities().Modes)
	}

	releases, err := idx.Search(ctx, search.Query{Keywords: "Retro Movie", Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 || releases[0].Title != "Retro Movie" {
		t.Fatalf("releases = %+v, want the cleaned Retro Movie row", releases)
	}
	if strings.Contains(releases[0].Link, retroEmail) || strings.Contains(releases[0].Link, retroPassword) || strings.Contains(releases[0].Link, retroToken) {
		t.Errorf("release link contains a credential: %q", releases[0].Link)
	}

	grab, err := idx.Grab(ctx, releases[0].Link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if len(grab.Body) == 0 || grab.ContentType != "application/x-bittorrent" {
		t.Errorf("grab = %+v", grab)
	}

	doer.rejectGrab.Store(true)
	_, err = idx.Grab(ctx, releases[0].Link)
	if err == nil {
		t.Fatal("refused grab should fail")
	}
	for _, secret := range []string{retroEmail, retroPassword, retroToken} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("refused grab error leaked %q: %v", secret, err)
		}
	}

	records := doer.snapshot()
	if len(records) != 4 {
		t.Fatalf("requests = %d, want login + search + two grabs", len(records))
	}
	for _, record := range records {
		for _, secret := range []string{retroEmail, retroPassword, retroToken} {
			if strings.Contains(record.url, secret) {
				t.Errorf("request URL leaked %q: %q", secret, record.url)
			}
		}
		if record.path != "/api/login" && record.authorization != "Bearer "+retroToken {
			t.Errorf("%s Authorization = %q", record.path, record.authorization)
		}
	}
}
