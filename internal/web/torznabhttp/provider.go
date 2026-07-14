package torznabhttp

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
	Protocol    string // "torrent" / "usenet"
}

// Indexer is one searchable tracker the handler serves: its identity, its
// capabilities (for the caps document and request validation/category mapping),
// and a search entry point that returns normalized releases. It is satisfied by
// an adapter over the Cardigann engine in production and by a fake in
// tests, so the handler never depends on the concrete engine.
type Indexer interface {
	Info() IndexerInfo
	Capabilities() *mapper.Capabilities
	Search(ctx context.Context, query search.Query) ([]*normalizer.Release, error)
	// NeedsResolver reports whether the definition declares a download block, so a
	// served link must be resolved before a grab. Direct-link trackers report
	// false and their link is served as-is.
	NeedsResolver() bool
	// DownloadNeedsAuth reports whether the download authenticates out-of-band — by
	// session cookie or request header (i.e. the def has a login block) rather than a
	// passkey baked into the URL. Such a link can't be served bare (a bare GET by *arr
	// hits a login page or 401), so it is routed through /dl and fetched with harbrr's
	// authenticated session, just like a resolver-needing link.
	DownloadNeedsAuth() bool
	// Grab performs the grab-time download: resolve the release link (the full
	// Cardigann download algorithm, with testlinktorrent validation) and fetch the
	// torrent through the session, honouring download.method/headers. A magnet is
	// returned as a redirect. The /dl proxy drives this so a passkey-bearing link is
	// resolved and fetched server-side, never exposed in the served feed.
	Grab(ctx context.Context, link string) (*search.GrabResult, error)
	// SupportsOffsetPaging reports whether this Indexer forwards offset/limit upstream
	// for deep-set paging (the newznab and nzbindex usenet drivers, via the flattened
	// registry adapter). It is part of the contract — not a type-asserted optional
	// capability — so the handler and the cache layer read one unconditional signal;
	// every other implementer (every Cardigann def, every other native driver, and the
	// test fakes) answers false.
	SupportsOffsetPaging() bool
}

// Provider resolves the indexer id from the request path to its Indexer.
type Provider interface {
	Indexer(ctx context.Context, id string) (Indexer, bool)
}
