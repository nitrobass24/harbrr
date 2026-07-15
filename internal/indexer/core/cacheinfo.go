package core

import (
	"context"
	"time"
)

// CacheInfo is the per-request cache metadata the search-cache decorator records so
// a serving surface knows whether to emit HTTP cache validators (ETag + Cache-Control)
// and answer a conditional GET with 304 Not Modified. It is populated only when a
// response was produced from — or freshly stored into — the cache; a cache-disabled
// or degraded path leaves it zero (Cached stays false, no validators are emitted).
type CacheInfo struct {
	// Cached reports whether this response came from — or was freshly stored into —
	// the search cache. The Torznab handler derives the SERVED validator itself from
	// the post-filter page it is about to write; this is only the boolean signal that
	// a validator should be emitted at all.
	Cached bool
	// ExpiresAt is when the cached entry expires; a consumer derives max-age from it.
	ExpiresAt time.Time
}

// cacheInfoKey is the unexported context key under which a request carries its
// CacheInfo sink (a pointer the cache layer fills). It lives here, beside the
// cache-bypass key, so cache plumbing never leaks into the engine query.
type cacheInfoKey struct{}

// WithCacheInfoSink attaches a fresh CacheInfo sink to ctx and returns both. The
// Torznab feed handler creates the sink before Search; the cache layer fills it via
// RecordCacheInfo on the synchronous read path; the handler reads it after Search to
// set validators.
func WithCacheInfoSink(ctx context.Context) (context.Context, *CacheInfo) {
	ci := &CacheInfo{}
	return context.WithValue(ctx, cacheInfoKey{}, ci), ci
}

// RecordCacheInfo writes info into ctx's CacheInfo sink when one is present, and is a
// no-op otherwise — so a background refresh (whose detached ctx carries no sink) or a
// caller that sets none (the JSON search API) never touches a stale sink. It is the
// cache layer's one entry point for surfacing validators to a serving surface.
func RecordCacheInfo(ctx context.Context, info CacheInfo) {
	if ci, ok := ctx.Value(cacheInfoKey{}).(*CacheInfo); ok && ci != nil {
		*ci = info
	}
}
