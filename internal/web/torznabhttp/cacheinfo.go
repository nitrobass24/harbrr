package torznabhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CacheInfo is the per-request cache metadata the search-cache decorator records so
// the feed handler can emit HTTP cache validators (ETag + Cache-Control) and answer a
// conditional GET with 304 Not Modified. It is populated only when a response was
// produced from — or freshly stored into — the cache; a cache-disabled or degraded
// path leaves it zero (no validators are emitted).
type CacheInfo struct {
	// ETag is a strong validator over the cached result payload, already quoted and
	// ready for the ETag header. It changes iff the result set changes (it is a hash
	// of the pre-/dl payload, NOT the rendered body — the /dl token rotates per
	// request, so the body is never byte-stable, but the underlying releases are).
	// Empty when the response did not come through the cache.
	ETag string
	// ExpiresAt is when the cached entry expires; the handler derives max-age from it.
	ExpiresAt time.Time
}

// cacheInfoKey is the unexported context key under which a request carries its
// CacheInfo sink (a pointer the cache layer fills). It lives here, beside the
// cache-bypass key, so cache plumbing never leaks into the engine query.
type cacheInfoKey struct{}

// WithCacheInfoSink attaches a fresh CacheInfo sink to ctx and returns both. The feed
// handler creates the sink before Search; the cache layer fills it via RecordCacheInfo
// on the synchronous read path; the handler reads it after Search to set validators.
func WithCacheInfoSink(ctx context.Context) (context.Context, *CacheInfo) {
	ci := &CacheInfo{}
	return context.WithValue(ctx, cacheInfoKey{}, ci), ci
}

// RecordCacheInfo writes info into ctx's CacheInfo sink when one is present, and is a
// no-op otherwise — so a background refresh (whose detached ctx carries no sink) or
// the JSON search API (which sets none) never touches a stale sink. It is the cache
// layer's one entry point for surfacing validators to the feed handler.
func RecordCacheInfo(ctx context.Context, info CacheInfo) {
	if ci, ok := ctx.Value(cacheInfoKey{}).(*CacheInfo); ok && ci != nil {
		*ci = info
	}
}

// requestNoCache reports whether the request asked harbrr to revalidate against the
// tracker rather than serve from cache: a `Cache-Control: no-cache`/`no-store` or a
// `Pragma: no-cache` request header. It is the header-based sibling of the `nocache=1`
// query param — both force a live fetch and suppress the 304 short-circuit.
func requestNoCache(r *http.Request) bool {
	if hasNoCacheDirective(r.Header.Get("Cache-Control")) {
		return true
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Pragma")), "no-cache")
}

// hasNoCacheDirective reports whether a Cache-Control header value carries a no-cache
// or no-store directive (case-insensitive, comma-separated).
func hasNoCacheDirective(v string) bool {
	for part := range strings.SplitSeq(v, ",") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "no-cache", "no-store":
			return true
		}
	}
	return false
}

// ifNoneMatchMatches reports whether an If-None-Match header matches etag (the quoted
// strong validator harbrr emitted). "*" matches any current representation; otherwise
// each candidate is compared after stripping an optional weak `W/` prefix, the weak
// comparison RFC 9110 mandates for If-None-Match.
func ifNoneMatchMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for cand := range strings.SplitSeq(header, ",") {
		cand = strings.TrimPrefix(strings.TrimSpace(cand), "W/")
		if strings.TrimSpace(cand) == etag {
			return true
		}
	}
	return false
}

// pagedETag folds this page's window into the cache layer's payload ETag so two feed
// requests that share a cached result set but render different pages get distinct
// validators. The payload ETag (registry.payloadETag) hashes the full pre-page
// result set and the cache key excludes limit/offset — one engine fetch serves every
// page — so without this fold a client revalidating page N with page M's ETag would be
// answered 304 and reuse the wrong page. It hashes the page-independent payload ETag,
// NOT the rendered body: the /dl-rewritten body varies by host/apikey, so hashing it
// would leak request identity into the validator and never match across clients.
func pagedETag(payloadETag string, offset, limit int) string {
	sum := sha256.Sum256([]byte(payloadETag + "|" + strconv.Itoa(offset) + "|" + strconv.Itoa(limit)))
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// setCacheValidators writes the ETag and Cache-Control headers for a cached response.
// max-age is the entry's remaining lifetime (clamped at 0). The directive is `private`
// because the feed URL carries the caller's apikey, so a shared/CDN cache must not
// store it — the validator still lets the client itself revalidate cheaply.
func setCacheValidators(w http.ResponseWriter, ci *CacheInfo, now time.Time) {
	w.Header().Set("ETag", ci.ETag)
	maxAge := max(int(ci.ExpiresAt.Sub(now).Seconds()), 0)
	w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(maxAge))
}
