package core

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

// Indexer is one searchable tracker as the rest of harbrr consumes it: its identity,
// its capabilities (for the caps document and request validation/category mapping),
// and a search entry point that returns normalized releases. It is satisfied by
// an adapter over the Cardigann engine in production and by a fake in
// tests, so a consumer never depends on the concrete engine.
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
	// ConsumesSearchMode reports whether this Indexer routes search.Query.Mode to a
	// different upstream search namespace (newznab/torznab/animebytes, via the
	// flattened registry adapter). The registry's empty-query RSS canonicalization
	// (#341) clears Mode before caching for every non-consuming Indexer — collapsing
	// every RSS consumer's poll onto one cache key regardless of its t= — and leaves
	// it untouched for a consuming Indexer, which needs a per-mode key. Every
	// Cardigann def and every native driver but the three that read Mode answers
	// false.
	ConsumesSearchMode() bool
}

// AggregateSlug is the reserved feed slug that resolves to every enabled indexer
// (autobrr/harbrr#400). It occupies the same {slug} path position as a real indexer,
// so the aggregate feed inherits every Torznab route for free; the registry rejects
// it as an instance slug so the two can never collide.
const AggregateSlug = "all"

// Provider resolves a feed slug to the indexer(s) that serve it.
type Provider interface {
	// Indexer resolves a slug to the single indexer it names. A reserved aggregate
	// slug is NOT an indexer and returns ok=false — which is what binds the /dl grab
	// route to a real member: an aggregate slug can never resolve a download.
	Indexer(ctx context.Context, id string) (Indexer, bool)
	// Resolve returns the member set a feed slug covers: one element for a real
	// indexer (same found-semantics as Indexer), every enabled indexer for
	// AggregateSlug. An aggregate with no enabled members resolves to an empty set
	// with ok=true — a valid, empty feed, not an error.
	Resolve(ctx context.Context, slug string) ([]Indexer, bool)
}
