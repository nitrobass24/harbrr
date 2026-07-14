// Package nzbindex is the native driver for NZBIndex (nzbindex.com), a public Usenet
// indexer. NZBIndex is NOT a standard Newznab server — it exposes its own JSON search
// API (`/api/search`) and `.nzb` download endpoint (`/api/download/{id}.nzb`), which is
// why Prowlarr implements it as a bespoke `NzbIndex` class rather than a generic Newznab
// definition. This driver ports that contract onto harbrr's native.Driver so it flows
// through the registry, caps mapper, Torznab serializer, and /dl grab path unchanged.
//
// It reproduces Prowlarr's NzbIndex request generator + parser: a paged
// `GET /api/search?max=&key=&q=&p=` search, a JSON `data.content[]` response whose title
// is extracted from the article `name`, and a keyless public `.nzb` download link.
package nzbindex

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	// defaultBaseURL is NZBIndex's site (Prowlarr's IndexerUrls[0]). Trailing slash is
	// trimmed at build time so {base}{path} joins cleanly.
	defaultBaseURL = "https://nzbindex.com"
	// requestDelaySeconds paces the shared client against a public server with no
	// documented rate limit (Prowlarr applies none; harbrr rides a small RequestDelay so
	// the paced client never hammers it).
	requestDelaySeconds = 2.0
	// categoryOther is the single newznab category NZBIndex tags every release with —
	// the API returns no per-release categorisation (Prowlarr: NewznabStandardCategory.Other).
	categoryOther = 8000
	// maxBodyBytes caps a search response. A page is a small JSON list of metadata, so
	// this is generous while bounding a hostile or runaway body.
	maxBodyBytes = 16 << 20
)

// driver is one configured NZBIndex instance. There is no login round-trip: the optional
// apikey rides the search query, and the .nzb download is public, so the driver holds no
// session state. caps are static (Prowlarr hardcodes them — NZBIndex has no ?t=caps).
type driver struct {
	native.Base
	apikey string
}

var _ native.Driver = (*driver)(nil)

// Families returns the single NZBIndex native family, wired into the registry's
// nativeFamilies() so the indexer is addable like any other native.
func Families() []native.Family {
	return []native.Family{{Definition: Definition(), Factory: New}}
}

// Definition is the Go-built, caps-only NZBIndex definition. Protocol is usenet, Type is
// public (no credentials required — the apikey is an optional rate-limit lift). It is never
// schema-validated (no login/search/download block); it exists so mapper.Build, the
// credential store, indexerInfo, and the addable-indexer list all work.
func Definition() *loader.Definition {
	delay := requestDelaySeconds
	return &loader.Definition{
		ID:           "nzbindex",
		Name:         "NZBIndex",
		Description:  "NZBIndex — public Usenet indexer (native driver)",
		Language:     "en-US",
		Type:         "public",
		Encoding:     "UTF-8",
		Protocol:     loader.ProtocolUsenet,
		Links:        []string{defaultBaseURL},
		RequestDelay: &delay,
		Settings:     settingFields(),
		Caps:         caps(),
	}
}

// settingFields is the single optional apikey field. The name carries the "apikey" token so
// the secret store auto-classifies it (encrypted at rest, redacted by the API). NZBIndex
// works anonymously (rate-limited); a key lifts the limit.
func settingFields() []loader.SettingsField {
	return []loader.SettingsField{
		{Name: "apikey", Label: "API Key (optional — omit for rate-limited public access)", Type: "text"},
	}
}

// caps advertises NZBIndex's real capabilities (Prowlarr's SetCapabilities): a q-only
// search across all modes (tv adds season/ep, folded into the q term), and the single
// Other category every release is tagged with. NZBIndex exposes no category tree, so this
// is the honest, static set.
func caps() loader.Caps {
	return loader.Caps{
		CategoryMappings: []loader.CategoryMapping{
			{ID: loader.Scalar{Value: "8000", Set: true}, Cat: "Other", Desc: "Other"},
		},
		Modes: loader.Modes{
			Search:      []string{"q"},
			TVSearch:    []string{"q", "season", "ep"},
			MovieSearch: []string{"q"},
			MusicSearch: []string{"q"},
			BookSearch:  []string{"q"},
		},
	}
}

// New is the native.Factory: it builds the static capabilities, resolves the optional
// apikey, and normalises the base URL. There is no caps fetch or login to prime.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("nzbindex: nil definition")
	}
	built, err := mapper.Build(p.Def)
	if err != nil {
		return nil, fmt.Errorf("nzbindex: build capabilities for %q: %w", p.Def.ID, err)
	}
	base, err := native.NewBase("nzbindex", p)
	if err != nil {
		return nil, err
	}
	base.Caps = built
	base.MaxBodyBytes = maxBodyBytes
	return &driver{
		Base:   base,
		apikey: strings.TrimSpace(p.Cfg["apikey"]),
	}, nil
}

// Capabilities returns the static capabilities (no live caps fetch — NZBIndex has none).
func (d *driver) Capabilities() *mapper.Capabilities { return d.Caps }

// NeedsResolver is false: the .nzb download URL is known directly from the result id.
func (d *driver) NeedsResolver() bool { return false }

// DownloadNeedsAuth is false: the .nzb download link is public and carries no secret (the
// optional apikey rides only the search, not the download — matching Prowlarr), so it is
// served bare. The Torznab serializer tags it application/x-nzb from the usenet protocol.
func (d *driver) DownloadNeedsAuth() bool { return false }

// SupportsOffsetPaging is true: NZBIndex pages via p={offset/limit}, so the driver forwards
// the requested window upstream for deep-set paging (buildSearchURL) rather than serving
// only the first page.
func (d *driver) SupportsOffsetPaging() bool { return true }

// Test verifies the instance is usable by issuing a minimal live search (max=1). A
// transport/auth/parse failure surfaces; an empty-but-valid response passes.
func (d *driver) Test(ctx context.Context) error {
	_, err := d.Search(ctx, search.Query{Limit: 1})
	return err
}

// Search issues the NZBIndex JSON search and returns the parsed releases. A 401 is bad
// credentials (login.ErrLoginFailed → auth_failure health); a 403/429/503 is a rate limit
// (the registry backs off rather than misreporting working creds); any other non-2xx is an
// error. The API error envelope (returned with HTTP 200) is handled by parseReleases. The
// request URL may embed the apikey, so every error routes the URL through apphttp.RedactURL.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	rawurl := d.buildSearchURL(q)
	resp, err := d.get(ctx, rawurl)
	if err != nil {
		return nil, err
	}
	return d.parseReleases(resp.Body)
}

// get issues the search GET with an Accept: application/json header. The URL may embed the
// apikey, so a transport error surfaces only its scheme://host (apphttp.RedactURLError drops
// the key-bearing query). The caller owns the returned body and interprets the status.
func (d *driver) get(ctx context.Context, rawurl string) (*native.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("nzbindex: build request to %s: %w", apphttp.SchemeHost(rawurl), apphttp.RedactURLError(err))
	}
	req.Header.Set("Accept", "application/json")
	resp, err := d.Do(ctx, req, native.ClassifyRateLimit403)
	if err != nil {
		return resp, normalizeReadError(err)
	}
	return resp, nil
}
