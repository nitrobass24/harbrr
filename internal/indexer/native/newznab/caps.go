package newznab

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// capsTTL is the cache lifetime of a fetched caps document (Prowlarr caches ~7 days). Past
// this age the next Capabilities()/Search() refetches.
const capsTTL = 7 * 24 * time.Hour

// Persisted setting keys for the cross-restart caps cache. The raw caps XML carries no
// secret (apikey is never in the caps body), but it flows through the encrypted settings
// store like any other setting. The fetched-at timestamp is stored as a Unix-seconds string.
const (
	settingCapsCache     = "__caps_cache"
	settingCapsFetchedAt = "__caps_fetched_at"
)

// capsCache holds the parsed capabilities and the source XML behind a mutex, with the
// fetched-at timestamp for the TTL check. A driver is shared across concurrent searches, so
// the cache is guarded.
type capsCache struct {
	mu        sync.Mutex
	built     *mapper.Capabilities
	fetchedAt time.Time
}

// rehydrate seeds the cache from persisted settings (the cross-restart path): the raw caps
// XML under settingCapsCache and the Unix-seconds fetched-at under settingCapsFetchedAt. A
// malformed or unparseable persisted value is ignored (the next need refetches) rather than
// failing construction — the cache is an optimisation, not a correctness dependency.
func (c *capsCache) rehydrate(cfg map[string]string) {
	raw := cfg[settingCapsCache]
	if raw == "" {
		return
	}
	root, err := parseCaps([]byte(raw), strings.TrimSpace(cfg["apikey"]))
	if err != nil {
		return
	}
	built, err := buildFromCaps(root)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.built = built
	c.fetchedAt = parseFetchedAt(cfg[settingCapsFetchedAt])
}

// get returns the cached capabilities when present and fresh (younger than capsTTL by the
// driver clock), else (nil, false) so the caller fetches.
func (c *capsCache) get(now time.Time) (*mapper.Capabilities, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.built == nil || c.fetchedAt.IsZero() || now.Sub(c.fetchedAt) >= capsTTL {
		return nil, false
	}
	return c.built, true
}

// store records a freshly fetched, built capabilities document with its fetched-at timestamp.
func (c *capsCache) store(built *mapper.Capabilities, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.built = built
	c.fetchedAt = now
}

// capabilities returns the live capabilities, fetching and caching from the remote ?t=caps
// when the cache is cold or stale. On a fetch failure it falls back to any previously built
// cache (even if stale) so a transient remote outage does not strand search; only a cold
// cache with no fallback surfaces the error.
func (d *driver) capabilities(ctx context.Context) (*mapper.Capabilities, error) {
	now := d.Clock()
	if built, ok := d.capsCache.get(now); ok {
		return built, nil
	}
	// No transport configured (e.g. the addable-indexer list builds the driver only to read
	// the placeholder caps): there is no way to fetch, so serve any cached caps or the
	// placeholder fallback without a network attempt.
	if d.Doer == nil {
		if fallback := d.capsCache.current(); fallback != nil {
			return fallback, nil
		}
		return d.Caps, nil
	}
	built, err := d.fetchCaps(ctx)
	if err != nil {
		if fallback := d.capsCache.current(); fallback != nil {
			return fallback, nil
		}
		return nil, err
	}
	return built, nil
}

// current returns the last built capabilities regardless of age, or nil if never fetched.
func (c *capsCache) current() *mapper.Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.built
}

// fetchCaps GETs the remote ?t=caps, parses + builds the capabilities, caches them in
// memory, and (when PersistSetting is wired) persists the raw XML + fetched-at for the
// cross-restart cache. The caps URL embeds the apikey, so every error routes the URL through
// apphttp.RedactURL and the apikey can never leak.
func (d *driver) fetchCaps(ctx context.Context) (*mapper.Capabilities, error) {
	rawurl := d.buildCapsURL()
	body, err := d.getCaps(ctx, rawurl)
	if err != nil {
		return nil, err
	}
	root, err := parseCaps(body, d.apikey)
	if err != nil {
		return nil, err
	}
	built, err := buildFromCaps(root)
	if err != nil {
		return nil, err
	}
	now := d.Clock()
	d.capsCache.store(built, now)
	d.persistCaps(ctx, body, now)
	return built, nil
}

// getCaps issues the caps GET and returns the body, classifying the status like a search: a
// 401 is bad credentials (login.ErrLoginFailed), a 403/429/503 is a rate limit, any other
// non-2xx is an error. Every error surfaces only the endpoint's scheme://host (the
// apikey-bearing query is dropped).
func (d *driver) getCaps(ctx context.Context, rawurl string) ([]byte, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("newznab: build caps request to %s: %w", apphttp.SchemeHost(rawurl), apphttp.RedactURLError(err))
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")
	resp, err := d.Do(ctx, req, native.ClassifyRateLimit403)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// buildCapsURL builds {baseUrl}{apiPath}?t=caps[&apikey=...]. apikey is appended only when
// set (some servers serve caps without a key). It is secret-bearing — redact before logging.
func (d *driver) buildCapsURL() string {
	params := url.Values{}
	params.Set("t", "caps")
	if d.apikey != "" {
		params.Set("apikey", d.apikey)
	}
	return strings.TrimRight(d.BaseURL, "/") + d.apiPath + "?" + encodeCapsQuery(params)
}

// encodeCapsQuery encodes the caps params with t first and apikey last (stable, redaction-
// safe order), mirroring encodeQuery for the search URL.
func encodeCapsQuery(params url.Values) string {
	var b []byte
	first := true
	for _, key := range []string{"t", "apikey"} {
		for _, v := range params[key] {
			if !first {
				b = append(b, '&')
			}
			first = false
			b = append(b, url.QueryEscape(key)...)
			b = append(b, '=')
			b = append(b, url.QueryEscape(v)...)
		}
	}
	return string(b)
}

// persistCaps writes the raw caps XML + fetched-at back to the encrypted store when
// PersistSetting is wired, so the cache survives a restart. A persist failure is non-fatal
// (the in-memory cache is authoritative); it is swallowed like MyAnonamouse's rotation write.
func (d *driver) persistCaps(ctx context.Context, rawXML []byte, now time.Time) {
	if d.persist == nil {
		return
	}
	_ = d.persist(ctx, settingCapsCache, string(rawXML))
	_ = d.persist(ctx, settingCapsFetchedAt, strconv.FormatInt(now.Unix(), 10))
}

// buildFromCaps translates a parsed caps document into a *mapper.Capabilities via
// mapper.Build, using a synthetic caps-only definition so the category map, custom-category
// synthesis, and family-root advertising all come from the shared builder.
func buildFromCaps(root *capsRoot) (*mapper.Capabilities, error) {
	def := &loader.Definition{ID: "newznab", Caps: capsToLoaderCaps(root)}
	caps, err := mapper.Build(def)
	if err != nil {
		return nil, fmt.Errorf("newznab: build capabilities from caps: %w", err)
	}
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
