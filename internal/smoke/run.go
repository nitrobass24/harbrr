package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/autobrr/harbrr/internal/appsync"
)

// harbrrIndexer is the subset of harbrr's GET /api/indexers view the suite needs.
type harbrrIndexer struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// RunSuite runs the full operator smoke suite against a live harbrr stack: for every
// enabled harbrr indexer it runs the Prowlarr parity differential and the app-sync
// assertions (Sonarr/Radarr/qui, when configured), then a single cache-hit check on
// the first enabled indexer. It reaches real trackers via harbrr and the *arr/qui/
// Prowlarr APIs — operator-run only, never CI.
func RunSuite(ctx context.Context, cfg Config) (Report, error) {
	c := &http.Client{Timeout: httpTimeout}
	indexers, err := listHarbrrIndexers(ctx, c, cfg)
	if err != nil {
		return Report{}, err
	}
	apps := configuredApps(cfg)
	rep := Report{Query: cfg.Query}
	firstEnabled := ""
	for _, ix := range indexers {
		if !ix.Enabled {
			continue
		}
		if firstEnabled == "" {
			firstEnabled = ix.Slug
		}
		rep.Findings = append(rep.Findings, parityCheck(ctx, c, cfg, ix))
		rep.Findings = append(rep.Findings, appSyncChecks(ctx, c, cfg, apps, ix)...)
		time.Sleep(betweenTrackerDelay)
	}
	if firstEnabled != "" {
		rep.Findings = append(rep.Findings, cacheCheck(ctx, c, cfg, firstEnabled))
	}
	return rep, nil
}

// listHarbrrIndexers fetches harbrr's configured indexers (management API, X-API-Key).
func listHarbrrIndexers(ctx context.Context, c *http.Client, cfg Config) ([]harbrrIndexer, error) {
	body, status, err := harbrrGet(ctx, c, cfg, "/api/indexers")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("harbrr GET /api/indexers: HTTP %d", status)
	}
	var out []harbrrIndexer
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse harbrr indexers: %w", err)
	}
	return out, nil
}

// harbrrGet issues a GET against the harbrr management API with the X-API-Key header.
func harbrrGet(ctx context.Context, c *http.Client, cfg Config, path string) ([]byte, int, error) {
	return httpGet(ctx, c, cfg.HarbrrURL+path, map[string]string{"X-API-Key": cfg.HarbrrKey})
}

// harbrrCategories fetches an indexer's advertised Newznab categories (the app-sync
// content-filter input), mapped to the appsync.Category type IndexerServesApp expects.
func harbrrCategories(ctx context.Context, c *http.Client, cfg Config, slug string) ([]appsync.Category, error) {
	body, status, err := harbrrGet(ctx, c, cfg, "/api/indexers/"+url.PathEscape(slug)+"/capabilities")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("harbrr capabilities for %q: HTTP %d", slug, status)
	}
	var caps struct {
		Categories []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(body, &caps); err != nil {
		return nil, fmt.Errorf("parse harbrr capabilities for %q: %w", slug, err)
	}
	out := make([]appsync.Category, 0, len(caps.Categories))
	for _, cat := range caps.Categories {
		out = append(out, appsync.Category{ID: cat.ID, Name: cat.Name})
	}
	return out, nil
}
