package torznab

import (
	"context"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// IndexerInfo is the indexer identity the Torznab feed needs, sourced from the
// loaded definition. It carries no secrets (no passkeys/cookies/config).
type IndexerInfo struct {
	ID          string
	Name        string
	Description string
	SiteLink    string
	Type        string // "public" / "private" / "semi-private"
}

// Indexer is one searchable tracker the handler serves: its identity, its
// capabilities (for the caps document and request validation/category mapping),
// and a search entry point that returns normalized releases. It is satisfied by
// an adapter over the Cardigann engine in production (Phase 4) and by a fake in
// tests, so the handler never depends on the concrete engine.
type Indexer interface {
	Info() IndexerInfo
	Capabilities() *mapper.Capabilities
	Search(ctx context.Context, query search.Query) ([]*normalizer.Release, error)
	// NeedsResolver reports whether the definition declares a download block, so a
	// served link must be resolved before a grab. Direct-link trackers report
	// false and their link is served as-is.
	NeedsResolver() bool
	// ResolveDownload turns a release's download link into the real torrent URL
	// (Phase-2 baseline: before.path + selectors). A def with no download block
	// returns the link unchanged. The full resolver and a grab-time /dl proxy are
	// Phase 7.
	ResolveDownload(ctx context.Context, link string) (string, error)
}

// Provider resolves the indexer id from the request path to its Indexer.
type Provider interface {
	Indexer(ctx context.Context, id string) (Indexer, bool)
}
