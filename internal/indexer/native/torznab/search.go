package torznab

import (
	"bytes"
	"context"
	"fmt"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// Search issues the Torznab API GET for the query and returns the parsed releases. A
// 401/403 is bad credentials (login.ErrLoginFailed -> auth_failure health); a 429/503
// is a rate limit; any other non-2xx is an error (native.ClassifyAuth403 — the
// majority dialect MoreThanTV follows, unlike the newznab sibling's
// ClassifyRateLimit403). A 2xx body that does not start with "<" (whitespace-trimmed)
// is Jackett's MoreThanTVAPI non-XML-body guard: an HTML login/error page returned
// with HTTP 200 — classified as an auth/config failure. The Torznab <error> envelope
// (also returned with HTTP 200) is handled by parseReleases. The request URL embeds
// the apikey, so every error routes through apphttp.RedactURL/SchemeHost and is never
// logged bare.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	resp, err := d.get(ctx, d.buildSearchURL(q), false)
	if err != nil {
		return nil, err
	}
	if err := checkXMLBody(resp.Body); err != nil {
		return nil, err
	}
	return d.parseReleases(resp.Body, d.Caps.CategoryMap)
}

// checkXMLBody guards Jackett's MoreThanTVAPI check
// (`!result.ContentString.StartsWith("<")`): a 2xx response whose (whitespace-trimmed)
// body does not start with "<" is treated as an auth/config failure rather than fed to
// the XML decoder. Unlike Jackett — which echoes the raw body into the surfaced
// exception, risking an apikey the server may have reflected back — the error text
// here NEVER includes body content.
func checkXMLBody(body []byte) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '<' {
		return nil
	}
	return fmt.Errorf("torznab: non-XML response (unexpected content): %w", login.ErrLoginFailed)
}

// get issues an authenticated GET (the apikey rides the URL's query, built by
// buildSearchURL). download selects DoDownload's larger body cap and
// truncation-is-an-error semantics for the grab path; the caller owns the returned
// body and interprets the status.
func (d *driver) get(ctx context.Context, rawurl string, download bool) (*native.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("torznab: build request to %s: %w", apphttp.SchemeHost(rawurl), apphttp.RedactURLError(err))
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")
	if download {
		return d.DoDownload(ctx, req, native.ClassifyAuth403)
	}
	return d.Do(ctx, req, native.ClassifyAuth403)
}
