package native

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
)

// The live ?t=caps machinery of the Newznab wire layer, shared by the two drivers that
// speak it. Prowlarr's Torznab indexer is a subclass of its Newznab one and both read
// their category tree and search modes through the SAME NewznabCapabilitiesProvider
// (fetch ?t=caps on first need, cache it); harbrr's newznab driver already mirrored that
// provider while torznab kept a permanent placeholder, so a child category (5040) or a
// custom id (100xxx) resolved to nothing in either direction (autobrr/harbrr#643). The
// fetch, the parse, the TTL cache and the cross-restart persistence live here once,
// parameterised by the two facts that differ per family: the error prefix / synthetic
// definition id (Base.Family) and the family's own XML GET (its status dialect).

// CapsTTL is the cache lifetime of a fetched caps document (Prowlarr caches ~7 days). Past
// this age the next Capabilities()/Search() refetches.
const CapsTTL = 7 * 24 * time.Hour

// Persisted setting keys for the cross-restart caps cache. The raw caps XML carries no
// secret (apikey is never in the caps body), but it flows through the encrypted settings
// store like any other setting. The fetched-at timestamp is stored as a Unix-seconds string.
const (
	SettingCapsCache     = "__caps_cache"
	SettingCapsFetchedAt = "__caps_fetched_at"
)

// NzbCapsParams wires one driver's caps machinery. Base supplies the clock, the transport
// presence, the base URL and the placeholder fallback; Get is the family's own XML GET, so
// each family keeps its own status classification (newznab treats a 403 as a rate limit,
// torznab as an auth failure); APIKey rides the caps URL and is scrubbed out of error text;
// QuotaCode is the family's documented request-quota error code (0 when it documents none);
// Persist is the settings-write hook (nil when not wired).
type NzbCapsParams struct {
	Base      *Base
	Get       func(ctx context.Context, rawurl string) (*Response, error)
	APIKey    string
	APIPath   string // normalised, no trailing slash (e.g. "/api")
	QuotaCode int
	Persist   func(ctx context.Context, name, value string) error
}

// NzbCaps holds the parsed capabilities behind a mutex with the fetched-at timestamp for
// the TTL check. A driver is shared across concurrent searches, so the cache is guarded.
type NzbCaps struct {
	p         NzbCapsParams
	mu        sync.Mutex
	built     *mapper.Capabilities
	fetchedAt time.Time
}

// NewNzbCaps builds the caps cache for one driver instance. It performs no I/O: the first
// Capabilities/CategoryMap/Fetch call does the fetching.
func NewNzbCaps(p NzbCapsParams) *NzbCaps { return &NzbCaps{p: p} }

// Rehydrate seeds the cache from persisted settings (the cross-restart path): the raw caps
// XML under SettingCapsCache and the Unix-seconds fetched-at under SettingCapsFetchedAt. A
// malformed or unparseable persisted value is ignored (the next need refetches) rather than
// failing construction — the cache is an optimisation, not a correctness dependency.
func (c *NzbCaps) Rehydrate(cfg map[string]string) {
	raw := cfg[SettingCapsCache]
	if raw == "" {
		return
	}
	built, err := c.parseAndBuild([]byte(raw))
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.built = built
	c.fetchedAt = parseFetchedAt(cfg[SettingCapsFetchedAt])
}

// Capabilities returns the live capabilities, fetching and caching from the remote ?t=caps
// when the cache is cold or stale. It never returns nil: a fetch failure falls back to any
// previously built cache (even if stale) so a transient remote outage does not strand
// search, and a cold cache with no fallback serves the driver's placeholder caps — the
// indexer stays addable and searchable, and the next call retries the fetch.
func (c *NzbCaps) Capabilities(ctx context.Context) *mapper.Capabilities {
	if built, ok := c.fresh(c.p.Base.Clock()); ok {
		return built
	}
	// No transport configured (e.g. the addable-indexer list builds the driver only to read
	// the placeholder caps): there is no way to fetch, so serve any cached caps or the
	// placeholder fallback without a network attempt.
	if c.p.Base.Doer != nil {
		if built, err := c.Fetch(ctx); err == nil {
			return built
		}
	}
	if fallback := c.Cached(); fallback != nil {
		return fallback
	}
	return c.p.Base.Caps
}

// CategoryMap returns the category map of the live caps (lazily fetched), falling back to
// the placeholder caps map when the remote caps are unavailable. It never returns nil so
// the parser can always resolve result categories, and the request side always has a tree
// to resolve a requested child category through.
func (c *NzbCaps) CategoryMap(ctx context.Context) *mapper.CategoryMap {
	return c.Capabilities(ctx).CategoryMap
}

// Fetch GETs the remote ?t=caps through the family's own GET (so a 401 is bad credentials
// and a 403/429/503 is classified in that family's dialect), parses + builds the
// capabilities, caches them in memory, and (when Persist is wired) persists the raw XML +
// fetched-at for the cross-restart cache. It is also the "test this indexer" probe: unlike
// Capabilities it surfaces the error instead of falling back. The caps URL embeds the
// apikey, so every error routes the URL through apphttp.RedactURL and the apikey can never
// leak.
func (c *NzbCaps) Fetch(ctx context.Context) (*mapper.Capabilities, error) {
	resp, err := c.p.Get(ctx, c.url())
	if err != nil {
		return nil, err
	}
	built, err := c.parseAndBuild(resp.Body)
	if err != nil {
		return nil, err
	}
	now := c.p.Base.Clock()
	c.store(built, now)
	c.persist(ctx, resp.Body, now)
	return built, nil
}

// parseAndBuild decodes a caps body and builds the capabilities from it, tagging both
// halves with the driver's family (the error prefix and the synthetic definition id).
func (c *NzbCaps) parseAndBuild(body []byte) (*mapper.Capabilities, error) {
	root, err := parseNzbCaps(body, c.p.Base.Family, c.p.APIKey, c.p.QuotaCode)
	if err != nil {
		return nil, err
	}
	return buildFromNzbCaps(root, c.p.Base.Family)
}

// url is the caps endpoint for this instance. It is secret-bearing — redact before logging.
func (c *NzbCaps) url() string {
	return NzbAPIURL(c.p.Base.BaseURL, c.p.APIPath, "caps", c.p.APIKey)
}

// fresh returns the cached capabilities when present and younger than CapsTTL by the
// driver clock, else (nil, false) so the caller fetches.
func (c *NzbCaps) fresh(now time.Time) (*mapper.Capabilities, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.built == nil || c.fetchedAt.IsZero() || now.Sub(c.fetchedAt) >= CapsTTL {
		return nil, false
	}
	return c.built, true
}

// store records a freshly fetched, built capabilities document with its fetched-at timestamp.
func (c *NzbCaps) store(built *mapper.Capabilities, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.built = built
	c.fetchedAt = now
}

// Cached returns the last built capabilities regardless of age, or nil if none was ever
// fetched or rehydrated — the stale fallback, and the "are the live caps primed?" read.
func (c *NzbCaps) Cached() *mapper.Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.built
}

// persist writes the raw caps XML + fetched-at back to the encrypted store when the hook is
// wired, so the cache survives a restart. A persist failure is non-fatal (the in-memory
// cache is authoritative); it is swallowed like MyAnonamouse's rotation write. The
// timestamp is written only after the XML landed: a stale document paired with a fresh
// timestamp would rehydrate as "fresh" and suppress the refresh for a whole TTL, whereas a
// missing timestamp merely forces an early refetch.
func (c *NzbCaps) persist(ctx context.Context, rawXML []byte, now time.Time) {
	if c.p.Persist == nil {
		return
	}
	if err := c.p.Persist(ctx, SettingCapsCache, string(rawXML)); err != nil {
		return
	}
	_ = c.p.Persist(ctx, SettingCapsFetchedAt, strconv.FormatInt(now.Unix(), 10))
}

// NzbAPIURL builds {baseUrl}{apiPath}?[apikey=...&]t={fn} for a parameterless Newznab API
// function (t=caps, t=user). apikey is included only when set (some servers serve caps
// without a key, and a torznab preset may be keyless). It is secret-bearing — redact
// before logging.
func NzbAPIURL(baseURL, apiPath, fn, apikey string) string {
	params := url.Values{}
	params.Set("t", fn)
	if apikey != "" {
		params.Set("apikey", apikey)
	}
	return strings.TrimRight(baseURL, "/") + apiPath + "?" + params.Encode()
}

// buildFromNzbCaps translates a parsed caps document into a *mapper.Capabilities via
// mapper.Build, using a synthetic caps-only definition so the category map, custom-category
// synthesis, and family-root advertising all come from the shared builder. loader.Caps has
// no Limits field (it is a Cardigann definition concept; <limits> is Newznab-only), so the
// advertised request-count limit is set directly from the parsed root afterward.
func buildFromNzbCaps(root *capsRoot, family string) (*mapper.Capabilities, error) {
	def := &loader.Definition{ID: family, Caps: capsToLoaderCaps(root)}
	caps, err := mapper.Build(def)
	if err != nil {
		return nil, fmt.Errorf("%s: build capabilities from caps: %w", family, err)
	}
	caps.Limits = root.Limits.limitsOrDefault()
	return caps, nil
}

// parseFetchedAt parses a Unix-seconds string into a time.Time; a blank/invalid value yields
// the zero time (treated as a cold cache so the next need refetches).
func parseFetchedAt(raw string) time.Time {
	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || secs <= 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}
