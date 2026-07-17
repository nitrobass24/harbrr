package registry_test

import (
	"context"
	"errors"
	stdhttp "net/http"
	"os"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

func TestNebulanceRequiresAPIKeyBeforePersist(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t, &nebulanceDoer{})
	for _, tt := range []struct {
		slug     string
		settings map[string]string
	}{
		{slug: "missing"},
		{slug: "empty", settings: map[string]string{"apikey": ""}},
		{slug: "whitespace", settings: map[string]string{"apikey": " \t "}},
	} {
		if _, err := reg.Add(ctx, registry.AddParams{Slug: tt.slug, DefinitionID: "nebulance", Settings: tt.settings}); !errors.Is(err, registry.ErrInvalid) {
			t.Errorf("Add(%s) err = %v, want registry.ErrInvalid", tt.slug, err)
		}
		if _, _, err := reg.Get(ctx, tt.slug); !errors.Is(err, database.ErrNotFound) {
			t.Errorf("Get(%s) err = %v, want database.ErrNotFound", tt.slug, err)
		}
	}

	doer := &nebulanceDoer{searchBody: `{"total_results":0,"items":[]}`}
	reg, _ = newRegistry(t, doer)
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "valid", DefinitionID: "nebulance", Settings: map[string]string{"apikey": "NBL-E2E-SYNTHETIC-API-KEY"},
	}); err != nil {
		t.Fatalf("Add(valid): %v", err)
	}
	if err := reg.Update(ctx, "valid", registry.UpdateParams{Settings: map[string]string{"apikey": " "}}); !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("Update(empty API key) err = %v, want registry.ErrInvalid", err)
	}
	if err := reg.Test(ctx, "valid"); err != nil {
		t.Fatalf("Test after rejected update: %v", err)
	}
}

type nebulanceDoer struct {
	searchBody string
	requests   []*stdhttp.Request
}

func (d *nebulanceDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.requests = append(d.requests, req)
	if req.URL.Query().Get("action") == "search" {
		return mkResp(stdhttp.StatusOK, d.searchBody, "application/json"), nil
	}
	return mkResp(stdhttp.StatusOK, "d4:name7:examplee", "application/x-bittorrent"), nil
}

func TestNebulanceEndToEnd(t *testing.T) {
	golden, err := os.ReadFile("../native/nebulance/testdata/search_response.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	doer := &nebulanceDoer{searchBody: string(golden)}
	reg, _ := newRegistry(t, doer)
	ctx := context.Background()

	if _, err := reg.Add(ctx, registry.AddParams{
		Slug:         "nbl",
		DefinitionID: "nebulance",
		Settings:     map[string]string{"apikey": "NBL-E2E-SYNTHETIC-API-KEY"},
	}); err != nil {
		t.Fatalf("Add(nebulance): %v", err)
	}
	indexer, ok := reg.Indexer(ctx, "nbl")
	if !ok {
		t.Fatal("nebulance indexer should resolve")
	}
	if !indexer.NeedsResolver() {
		t.Error("NeedsResolver = false, want true")
	}
	if indexer.Capabilities().Modes["tv-search"] == nil {
		t.Error("native capabilities missing tv-search")
	}

	releases, err := indexer.Search(ctx, search.Query{Keywords: "Example Show", Limit: 100})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 3 {
		t.Fatalf("releases = %d, want 3", len(releases))
	}
	if strings.Contains(releases[0].GUID, "token=") {
		t.Error("stable GUID contains a download token")
	}

	grab, err := indexer.Grab(ctx, releases[0].Link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if len(grab.Body) == 0 || grab.ContentType != "application/x-bittorrent" {
		t.Error("grab did not return torrent bytes")
	}
	if len(doer.requests) != 2 {
		t.Fatalf("requests = %d, want search + grab", len(doer.requests))
	}
	searchRequest := doer.requests[0]
	if searchRequest.Method != stdhttp.MethodGet || searchRequest.URL.Query().Get("api_key") == "" {
		t.Error("search request missing GET/API-key contract")
	}
	grabRequest := doer.requests[1]
	if grabRequest.Method != stdhttp.MethodGet || grabRequest.URL.Query().Get("action") != "download" {
		t.Error("grab request must be a download GET")
	}
}
