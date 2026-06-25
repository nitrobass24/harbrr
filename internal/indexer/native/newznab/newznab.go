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
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured Newznab instance. It is built once per instance and cached by
// the registry. There is no login round-trip: every request carries the apikey as a query
// param, so the driver holds no session state.
type driver struct {
	def     *loader.Definition
	caps    *mapper.Capabilities
	apikey  string
	apiPath string // normalised, no trailing slash (e.g. "/api")
	doer    search.Doer
	baseURL string // normalised with NO trailing slash
	clock   func() time.Time
}

var _ native.Driver = (*driver)(nil)

// New is the native.Factory for the generic Newznab driver. It builds the placeholder
// capabilities from the definition (Leaf 5 will refresh these from a live ?t=caps fetch),
// resolves the apikey/apiPath settings, and normalises the base URL.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("newznab: nil definition")
	}
	caps, err := mapper.Build(p.Def)
	if err != nil {
		return nil, fmt.Errorf("newznab: build capabilities for %q: %w", p.Def.ID, err)
	}
	base := p.BaseURL
	if base == "" && len(p.Def.Links) > 0 {
		base = p.Def.Links[0]
	}
	clock := p.Clock
	if clock == nil {
		clock = time.Now
	}
	return &driver{
		def:     p.Def,
		caps:    caps,
		apikey:  strings.TrimSpace(p.Cfg["apikey"]),
		apiPath: normalizeAPIPath(p.Cfg["apiPath"]),
		doer:    p.Doer,
		baseURL: strings.TrimRight(base, "/"),
		clock:   clock,
	}, nil
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

// Capabilities returns the placeholder Newznab capabilities (standard category table + all
// modes). Leaf 5 replaces this with a live ?t=caps fetch mapped onto the standard table;
// this is the clear seam — only this method and New (which calls mapper.Build) need to
// change to swap the Go-literal caps for a fetched-and-cached document.
func (d *driver) Capabilities() *mapper.Capabilities { return d.caps }

// NeedsResolver is false: a Newznab .nzb URL is a direct, apikey-bearing HTTP link (no
// magnet, no extra resolve step). The download is proxied server-side by Grab (driven by
// DownloadNeedsAuth), not resolved.
func (d *driver) NeedsResolver() bool { return false }

// DownloadNeedsAuth is true: the .nzb download URL embeds the apikey, so it must be routed
// through the /dl proxy (which calls Grab) and never served bare in the feed — redirecting
// it would leak the apikey to the *arr/SABnzbd. This is harbrr's deliberate
// proxy-not-redirect divergence from Prowlarr.
func (d *driver) DownloadNeedsAuth() bool { return true }

// Test verifies the instance is usable (the management "test indexer" action). It issues a
// lightweight empty search: a 401/403 or a Newznab auth error envelope (code 100-199, or
// a missing-apikey error) surfaces as login.ErrLoginFailed; a clean response (even zero
// items) proves the apikey/baseUrl authenticate. Leaf 5 will additionally prime the caps
// cache here.
func (d *driver) Test(ctx context.Context) error {
	_, err := d.Search(ctx, search.Query{})
	return err
}

// itoa is a tiny strconv.Itoa alias used by the caps builder.
func itoa(n int) string { return strconv.Itoa(n) }
