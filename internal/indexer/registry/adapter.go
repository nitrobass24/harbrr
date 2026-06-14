package registry

import (
	"fmt"

	"github.com/autobrr/harbrr/internal/indexer/cardigann"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/web/torznab"
)

// indexerAdapter presents a built Cardigann engine as a torznab.Indexer, so the
// Torznab handler depends only on the interface, never the engine. It is the unit
// the registry caches per slug.
type indexerAdapter struct {
	info   torznab.IndexerInfo
	engine *cardigann.Engine
}

// Compile-time proof the adapter satisfies the handler's contract.
var _ torznab.Indexer = (*indexerAdapter)(nil)

// Info returns the indexer identity (carries no secrets).
func (a *indexerAdapter) Info() torznab.IndexerInfo { return a.info }

// Capabilities returns the engine's capabilities document.
func (a *indexerAdapter) Capabilities() *mapper.Capabilities { return a.engine.Capabilities() }

// Search runs the engine's online search. The error is wrapped with the indexer
// id (not a secret); the caller redacts it before logging.
func (a *indexerAdapter) Search(query search.Query) ([]*normalizer.Release, error) {
	releases, err := a.engine.Search(query)
	if err != nil {
		return nil, fmt.Errorf("registry: search %q: %w", a.info.ID, err)
	}
	return releases, nil
}

// NeedsResolver reports whether the definition declares a download block.
func (a *indexerAdapter) NeedsResolver() bool { return a.engine.NeedsResolver() }

// ResolveDownload resolves a release link to the real torrent URL. The error is
// wrapped with the indexer id (not a secret); the caller redacts it.
func (a *indexerAdapter) ResolveDownload(link string) (string, error) {
	resolved, err := a.engine.ResolveDownload(link)
	if err != nil {
		return "", fmt.Errorf("registry: resolve download %q: %w", a.info.ID, err)
	}
	return resolved, nil
}
