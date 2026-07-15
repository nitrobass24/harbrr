package torznabhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
)

// CacheInfo is the per-request cache metadata the search-cache decorator records so
// the feed handler knows whether to emit HTTP cache validators (ETag + Cache-Control)
// and answer a conditional GET with 304 Not Modified. It is populated only when a
// response was produced from — or freshly stored into — the cache; a cache-disabled
// or degraded path leaves it zero (Cached stays false, no validators are emitted).
type CacheInfo struct {
	// Cached reports whether this response came from — or was freshly stored into —
	// the search cache. The handler derives the SERVED validator itself from the
	// post-filter page it is about to write (servedPayloadETag + pagedETag); this is
	// only the boolean signal that a validator should be emitted at all.
	Cached bool
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

// servedPayloadETag hashes the POST-filter releases the handler is about to serialize
// and folds in the freeleech-bypass variant, yielding the content component the served
// validator (pagedETag) hangs the page window off. It exists because the freeleech
// honor feed and the /full bypass feed share ONE cached entry but apply different
// serve-time filters: the cache layer's payload ETag hashes the PRE-filter set and is
// identical for both feeds, so reusing it would let a conditional GET on one feed be
// answered 304 with the other variant's body. Hashing the served (post-filter) set fixes
// the honor feed's validator to track its actual body, and folding the bypass flag keeps
// the two variants distinct even when a freeleech-only page happens to equal the full
// page. It hashes the releases BEFORE the /dl rewrite, so the validator stays identical
// across clients (the rewrite injects per-request host/apikey). A marshal failure returns
// ("", false) and the handler then emits no validator rather than a wrong one.
func servedPayloadETag(releases []*normalizer.Release, bypass bool) (string, bool) {
	payload, err := json.Marshal(releases)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(payload)
	return strconv.FormatBool(bypass) + "|" + hex.EncodeToString(sum[:]), true
}

// setCacheValidators writes the ETag and Cache-Control headers for a cached response.
// etag is the served validator (servedPayloadETag+pagedETag, already quoted); max-age
// is expiresAt's remaining lifetime from now (clamped at 0). The directive is `private`
// because the feed URL carries the caller's apikey, so a shared/CDN cache must not
// store it — the validator still lets the client itself revalidate cheaply.
func setCacheValidators(w http.ResponseWriter, etag string, expiresAt, now time.Time) {
	w.Header().Set("ETag", etag)
	maxAge := max(int(expiresAt.Sub(now).Seconds()), 0)
	w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(maxAge))
}

// servedPage is the page of releases the handler is about to serialize — the content
// the revalidator's served ETag needs to track (servedPayloadETag+pagedETag), not the
// cache layer's pre-page, pre-filter payload.
type servedPage struct {
	releases []*normalizer.Release
	offset   int
	limit    int
}

// revalidate is the conditional-GET 304 protocol for a cache-backed feed response — the
// single place the "never answer a 304 with the wrong feed-variant or page body" hazard
// is decided, so it can be tested directly rather than only through the full handler.
//
// When ci reports no cached entry, or the served page fails to hash, it writes nothing
// and returns false: the caller falls through to serializing the full body. Otherwise it
// computes and emits the served validators — servedPayloadETag(page, bypass) folded with
// page's offset/limit via pagedETag, ExpiresAt for Cache-Control's max-age — so the
// response always carries them, even on a 200. It then answers 304 (handled=true, nothing
// more written) only when fresh does not force a live body AND requestHeaders'
// If-None-Match matches the JUST-EMITTED validator; the fold means a client revalidating
// one variant/page can never match another's, so it always falls through to 200 with the
// live body instead of a 304 for the wrong content.
func (h *handler) revalidate(w http.ResponseWriter, requestHeaders http.Header, ci CacheInfo, page servedPage, bypass, fresh bool) (handled bool) {
	if !ci.Cached {
		return false
	}
	view, ok := servedPayloadETag(page.releases, bypass)
	if !ok {
		return false
	}
	etag := pagedETag(view, page.offset, page.limit)
	setCacheValidators(w, etag, ci.ExpiresAt, h.clock())
	if fresh || !ifNoneMatchMatches(requestHeaders.Get("If-None-Match"), etag) {
		return false
	}
	w.WriteHeader(http.StatusNotModified)
	return true
}
