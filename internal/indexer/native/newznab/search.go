package newznab

import (
	"context"
	"fmt"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// maxBodyBytes caps a Newznab RSS response. A page is small XML (<= limit items of
// metadata), so this is generous while bounding a hostile or runaway body.
const maxBodyBytes = 16 << 20 // 16 MiB

// Search issues the Newznab API GET for the query and returns the parsed releases. A 401 is
// bad credentials (login.ErrLoginFailed -> auth_failure health); a 403 or 429/503 is a rate
// limit (the registry backs off rather than misreporting working creds); any other non-2xx
// is an error. The Newznab error envelope (returned with HTTP 200) and its auth/rate-limit
// classification are handled by parseReleases. The request URL embeds the apikey, so every
// error routes the URL through apphttp.RedactURL (which redacts the apikey query param) and
// the URL is never logged bare.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	// Warm the caps cache so result-side category mapping uses the remote category tree.
	// A caps-fetch failure is non-fatal: capabilities() falls back to any prior cache, and
	// categories() ultimately falls back to the placeholder standard table — search must
	// still run when caps are momentarily unavailable.
	catMap := d.activeCategoryMap(ctx)

	rawurl := d.buildSearchURL(q)
	resp, err := d.get(ctx, rawurl)
	if err != nil {
		return nil, err
	}
	return d.parseReleases(resp.Body, catMap)
}

// activeCategoryMap returns the category map of the live caps (lazily fetched), falling back
// to the placeholder caps map when the remote caps are unavailable. It never returns nil so
// the parser can always resolve result categories.
func (d *driver) activeCategoryMap(ctx context.Context) *mapper.CategoryMap {
	if caps, err := d.capabilities(ctx); err == nil && caps != nil {
		return caps.CategoryMap
	}
	return d.Caps.CategoryMap
}

// get issues the Newznab API GET. The URL embeds the apikey, so a transport error surfaces
// only its scheme://host (apphttp.SchemeHost drops the apikey-bearing query) with the cause
// routed through apphttp.RedactURLError; the apikey can never leak through the wrapped
// *url.Error. The caller owns the returned body and interprets the status.
func (d *driver) get(ctx context.Context, rawurl string) (*native.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("newznab: build request to %s: %w", apphttp.SchemeHost(rawurl), apphttp.RedactURLError(err))
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")
	resp, err := d.Do(ctx, req, native.ClassifyRateLimit403)
	if err != nil {
		return resp, err
	}
	return resp, nil
}
