package torznab

import (
	"context"
	"net/url"
)

// cacheBypassKey is the unexported context key under which a request signals that
// the search-results cache must be bypassed (nocache=1). It lives here, not in
// search.Query, so the bypass never pollutes the cache key or the engine query.
type cacheBypassKey struct{}

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

// wantsNoCache reports whether the request asked to bypass the cache. The trigger
// is exactly nocache=1 — by design, other truthy spellings (nocache=true, nocache=on)
// and a bare/empty nocache are NOT honored and are served from cache as normal.
// Bypassing forces a live tracker fetch, so the strict single form keeps a stray
// query param from accidentally hammering the tracker.
func wantsNoCache(q url.Values) bool {
	return q.Get("nocache") == "1"
}
