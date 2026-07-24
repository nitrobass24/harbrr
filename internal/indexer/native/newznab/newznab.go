// Package newznab is the generic native driver for any Newznab/Torznab usenet indexer
// (Newznab, NZBHydra2, and the named presets in Leaf 7). It has no Cardigann definition
// because the usenet path is protocol-level (Protocol=usenet) rather than a YAML corpus
// entry: Prowlarr implements it as one generic C# Newznab class plus presets, not as a
// Cardigann def. The driver builds an outbound Newznab API URL from a search.Query, parses
// the RSS/XML response into normalized releases, and proxies the apikey-bearing .nzb body
// server-side at grab time so the apikey never reaches the served feed.
//
// It reproduces Prowlarr's documented Newznab contract (NewznabRequestGenerator /
// NewznabRssParser) and reuses every harbrr seam: the paced HTTP doer, the secret store
// (apikey is auto-classified secret), the normalized release, the caps mapper, the /dl
// grab proxy, and URL redaction.
package newznab

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured Newznab instance. It is built once per instance and cached by
// the registry. There is no login round-trip: every request carries the apikey as a query
// param, so the driver holds no session state. caps is the placeholder fallback built from
// the definition (the full standard table); capsCache holds the live ?t=caps document, which
// supersedes the placeholder once fetched.
type driver struct {
	native.Base
	capsCache capsCache // live ?t=caps document, lazily fetched + TTL-cached
	apikey    string
	apiPath   string // normalised, no trailing slash (e.g. "/api")
	// persist durably writes the fetched caps XML + fetched-at back to the encrypted
	// store (nil when not wired), so the caps cache survives a restart.
	persist func(ctx context.Context, name, value string) error
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for the generic Newznab driver. It builds the placeholder
// capabilities from the definition (the fallback when no live caps are cached), resolves the
// apikey/apiPath settings, normalises the base URL, and rehydrates the caps cache from any
// persisted ?t=caps document so a restart serves caps without a cold-start network fetch.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("newznab: nil definition")
	}
	caps, err := mapper.Build(p.Def)
	if err != nil {
		return nil, fmt.Errorf("newznab: build capabilities for %q: %w", p.Def.ID, err)
	}
	baseParams := p
	if baseParams.BaseURL == "" && len(p.Def.Links) == 0 {
		// The generic addable family intentionally has no default URL; keep it
		// constructible for metadata/caps reads until an instance supplies BaseURL.
		baseParams.BaseURL = "https://newznab.invalid"
	}
	base, err := native.NewBase("newznab", baseParams)
	if err != nil {
		return nil, err
	}
	base.Caps = caps
	base.MaxBodyBytes = maxBodyBytes
	d := &driver{
		Base:    base,
		apikey:  strings.TrimSpace(p.Cfg["apikey"]),
		apiPath: normalizeAPIPath(p.Cfg["apiPath"]),
		persist: p.PersistSetting,
	}
	d.capsCache.rehydrate(p.Cfg)
	return d, nil
}

// normalizeAPIPath resolves the apiPath setting: a blank value defaults to "/api"
// (Prowlarr NewznabSettings default); a trailing slash is stripped; a missing leading
// slash is added so {base}{apiPath} joins correctly.
func normalizeAPIPath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		p = defaultAPIPath
	}
	p = strings.TrimRight(p, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// Capabilities returns the live Newznab capabilities, lazily fetching and caching the remote
// ?t=caps document on first need (and refreshing past the 7-day TTL). The native.Driver
// contract is context-free and non-nil, so a cold-cache fetch failure (the indexer is down,
// no caps ever cached) falls back to the placeholder standard table rather than returning nil
// — the indexer stays addable and searchable, and the next call retries the fetch.
func (d *driver) Capabilities() *mapper.Capabilities {
	built, err := d.capabilities(context.Background())
	if err != nil {
		return d.Caps
	}
	return built
}

// NeedsResolver is false: a Newznab .nzb URL is a direct, apikey-bearing HTTP link (no
// magnet, no extra resolve step). The download is proxied server-side by Grab (driven by
// DownloadNeedsAuth), not resolved.
func (d *driver) NeedsResolver() bool { return false }

// DownloadNeedsAuth is true: the .nzb download URL embeds the apikey, so it must be routed
// through the /dl proxy (which calls Grab) and never served bare in the feed — redirecting
// it would leak the apikey to the *arr/SABnzbd. This is harbrr's deliberate
// proxy-not-redirect divergence from Prowlarr.
func (d *driver) DownloadNeedsAuth() bool { return true }

// SupportsOffsetPaging is true: the Newznab API takes offset/limit, so the driver forwards
// the requested page window upstream (buildSearchURL) for deep-set paging rather than
// fetching only the first 100 and letting the handler slice. It is part of the
// native.Driver contract, which the registry adapter and search-cache layer read directly.
func (d *driver) SupportsOffsetPaging() bool { return true }

// ConsumesSearchMode is true: fillModeParams routes q.Mode to a different t=
// function (tvsearch/movie/music/book), so an RSS poll under a different mode is a
// distinct outbound request and must keep its own cache key.
func (d *driver) ConsumesSearchMode() bool { return true }

// Test verifies the instance is usable (the management "test indexer" action) and eagerly
// primes the caps cache. The caps fetch both validates the apikey/baseUrl (a 401/403 or a
// Newznab auth error envelope surfaces as login.ErrLoginFailed) and discovers the remote
// category tree + search modes, so a successful add starts with live caps cached (and
// persisted, when wired) rather than the placeholder.
func (d *driver) Test(ctx context.Context) error {
	_, err := d.fetchCaps(ctx)
	return err
}

// itoa is a tiny strconv.Itoa alias used by the caps builder.
func itoa(n int) string { return strconv.Itoa(n) }
