package main

import (
	"context"
	"net/http"
	"time"

	"github.com/autobrr/harbrr/internal/appsync"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// appSyncClient is the HTTP client app-sync drivers use to reach the *arr/qui apps.
// A bounded timeout keeps a hung app from stalling a sync.
func appSyncClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// registrySource adapts the indexer registry to appsync.IndexerSource: the configured
// instances and each one's advertised Newznab categories. Keeping it in the
// composition root keeps the appsync package free of an engine dependency.
type registrySource struct {
	reg *registry.Registry
}

func (s registrySource) List(ctx context.Context) ([]domain.IndexerInstance, error) {
	return s.reg.List(ctx) //nolint:wrapcheck // composition-root adapter; the service wraps.
}

// Categories returns the indexer's advertised categories. An indexer that fails to
// resolve yields no categories rather than failing the whole sync — the indexer is
// still pushed (the app falls back to all categories).
func (s registrySource) Categories(ctx context.Context, slug string) ([]appsync.Category, error) {
	idx, ok := s.reg.Indexer(ctx, slug)
	if !ok {
		return nil, nil
	}
	caps := idx.Capabilities()
	out := make([]appsync.Category, 0, len(caps.Categories))
	for _, c := range caps.Categories {
		out = append(out, appsync.Category{ID: c.ID, Name: c.Name})
	}
	return out, nil
}
