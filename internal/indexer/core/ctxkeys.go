package core

import (
	"context"
	"net/url"
)

// The unexported context keys the read pipeline threads through, and the one query-param
// predicate that sets one of them. Each marker lives on the context rather than in
// search.Query so it never pollutes the cache key or the engine query.

// cacheBypassKey is the context key under which a request signals that the search-results
// cache must be bypassed (nocache=1).
type cacheBypassKey struct{}

// freeleechBypassKey is the context key under which the freeleech-bypass feed variant
// signals that the serve-time freeleech view must be skipped (the full catalog is served).
// The route — not a query param — selects the variant; SearchReleases copies it onto the
// engine query for the registry adapter's freeleech serve-time view to read.
type freeleechBypassKey struct{}

// grabCategoryKey is the context key carrying the parent category of the release a grab is
// for. It travels on the context rather than in Indexer.Grab's signature because the
// category is pure observability (autobrr/harbrr#403): it exists only so the stats layer
// can tally the grab under the right family, and no driver reads it. The /dl proxy seals
// the category into the download token alongside the link, so it survives the round trip
// to the consumer and back.
type grabCategoryKey struct{}

// WithCacheBypass marks ctx so a downstream search-cache decorator skips the cache
// (no read, no write) for this request.
func WithCacheBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, cacheBypassKey{}, true)
}

// CacheBypass reports whether ctx carries the cache-bypass marker.
func CacheBypass(ctx context.Context) bool {
	v, _ := ctx.Value(cacheBypassKey{}).(bool)
	return v
}

// WithFreeleechBypass marks ctx as a freeleech-bypass request (the `/results/torznab/full`
// variant), so the downstream freeleech view returns the full catalog instead of
// freeleech-only. The Torznab handler's bypass route sets this; SearchReleases and the
// handler's revalidator both read it via FreeleechBypass.
func WithFreeleechBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, freeleechBypassKey{}, true)
}

// FreeleechBypass reports whether ctx carries the freeleech-bypass marker.
func FreeleechBypass(ctx context.Context) bool {
	v, _ := ctx.Value(freeleechBypassKey{}).(bool)
	return v
}

// WithGrabCategory marks ctx with the grabbed release's standard PARENT category id
// (0 = unknown), for the per-category grab tally.
func WithGrabCategory(ctx context.Context, categoryID int) context.Context {
	return context.WithValue(ctx, grabCategoryKey{}, categoryID)
}

// GrabCategory returns the parent category id ctx carries, or 0 when the grab arrived
// without one (an older token, a magnet, or a caller outside the /dl proxy).
func GrabCategory(ctx context.Context) int {
	id, _ := ctx.Value(grabCategoryKey{}).(int)
	return id
}

// WantsNoCache reports whether the request asked to bypass the cache. The trigger
// is exactly nocache=1 — by design, other truthy spellings (nocache=true, nocache=on)
// and a bare/empty nocache are NOT honored and are served from cache as normal.
// Bypassing forces a live tracker fetch, so the strict single form keeps a stray
// query param from accidentally hammering the tracker. Exported because both the read
// pipeline (SearchReleases, internally) and the Torznab handler's header-based
// no-cache check (the `Cache-Control: no-cache` sibling) need it.
func WantsNoCache(q url.Values) bool {
	return q.Get("nocache") == "1"
}
