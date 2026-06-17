package myanonamouse

import (
	"context"
	"fmt"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const (
	// maxBodyBytes caps a search response. A torrent download uses the larger
	// maxTorrentBytes cap (grab.go).
	maxBodyBytes = 8 << 20 // 8 MiB
	// mamIDCookie is the session cookie name MAM authenticates with and rotates.
	mamIDCookie = "mam_id"
)

// mamID returns the current (possibly rotated) mam_id under the mutex.
func (d *driver) mamID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.currentMamID
}

// captureRotatedMamID scans a response's Set-Cookie headers for a refreshed mam_id
// and, if present, updates the in-memory current value for subsequent in-process
// requests. MAM rotates mam_id on every response; this is process-local only and is
// never written back to the store (on restart the stored value is used). The new
// value is a secret and is never logged.
func (d *driver) captureRotatedMamID(resp *stdhttp.Response) {
	for _, c := range resp.Cookies() {
		if c.Name == mamIDCookie && c.Value != "" {
			d.mu.Lock()
			d.currentMamID = c.Value
			d.mu.Unlock()
			return
		}
	}
}

// get issues an authenticated GET with the Cookie: mam_id=… header, captures any
// rotated mam_id from the response, and returns the response for the caller to
// interpret (404/429/2xx). The cookie rides as a header, never the URL, so the URL
// carries no secret; errors still redact it.
func (d *driver) get(ctx context.Context, rawurl, accept string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("myanonamouse: build request: %w", err)
	}
	req.AddCookie(&stdhttp.Cookie{Name: mamIDCookie, Value: d.mamID()})
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("myanonamouse: request to %s: %w", apphttp.RedactURL(rawurl), err)
	}
	d.captureRotatedMamID(resp)
	return resp, nil
}

// Test verifies the configured mam_id authenticates (the management "test indexer"
// action) via a cheap authenticated search. A 403 means the session cookie expired or
// is invalid, wrapped with login.ErrLoginFailed so the registry records an
// auth_failure health event.
func (d *driver) Test(ctx context.Context) error {
	resp, err := d.get(ctx, d.buildSearchURL(search.Query{}), "application/json")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusForbidden:
		return fmt.Errorf("myanonamouse: mam_id expired or invalid: %w", login.ErrLoginFailed)
	case search.IsRateLimitStatus(resp.StatusCode):
		return &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("myanonamouse: test returned HTTP %d", resp.StatusCode)
	}
	return nil
}
