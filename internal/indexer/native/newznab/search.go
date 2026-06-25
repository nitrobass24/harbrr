package newznab

import (
	"context"
	"fmt"
	"io"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
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
	rawurl := d.buildSearchURL(q)
	resp, err := d.get(ctx, rawurl)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized:
		return nil, fmt.Errorf("newznab: search unauthorized: %w", login.ErrLoginFailed)
	case resp.StatusCode == stdhttp.StatusForbidden || search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("newznab: search returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("newznab: read search response: %w", search.ErrParseError)
	}
	return d.parseReleases(body)
}

// get issues the Newznab API GET. The URL embeds the apikey, so a transport error routes the
// URL through apphttp.RedactURL (redacting the apikey query param) before it reaches the
// error string; the apikey can never leak through the wrapped *url.Error. The caller owns
// the returned body and interprets the status.
func (d *driver) get(ctx context.Context, rawurl string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("newznab: build request to %s: %w", apphttp.RedactURL(rawurl), err)
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")
	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("newznab: request to %s: %s", apphttp.RedactURL(rawurl), apphttp.RedactError(err))
	}
	return resp, nil
}
