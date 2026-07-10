package myanonamouse

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"strings"

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

// scrubSecret removes the mam_id session cookie (both the currently rotated value and the
// originally configured one) from s so a server-echoed error string cannot leak it. MAM's
// error text is server-controlled free text that reaches a persisted health event / webhook,
// and mam_id is the sole secret, so it is value-scrubbed at the echo site (mirroring the
// sibling native drivers' scrubAPIKey). Only non-empty values are replaced.
func (d *driver) scrubSecret(s string) string {
	d.mu.Lock()
	current := d.currentMamID
	d.mu.Unlock()
	for _, secret := range []string{current, d.cfg["mam_id"]} {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	return s
}

// captureRotatedMamID scans a response's Set-Cookie headers for a refreshed mam_id
// and, when it changed, updates the in-memory current value and persists it back to
// the encrypted store so the session survives a restart instead of reverting to the
// stored value. The persist is synchronous: MAM rotates mam_id on every response and
// the per-host paced doer serializes MAM requests, so an in-line write keeps the
// stored value in rotation order — a detached goroutine could race and persist an
// older token last. The write is best-effort (the registry logs a failure; it never
// fails the search), and the new value is a secret never logged here.
func (d *driver) captureRotatedMamID(ctx context.Context, resp *stdhttp.Response) {
	for _, c := range resp.Cookies() {
		if c.Name == mamIDCookie && c.Value != "" {
			d.mu.Lock()
			changed := c.Value != d.currentMamID
			d.currentMamID = c.Value
			persist := d.persist
			d.mu.Unlock()
			if changed && persist != nil {
				_ = persist(ctx, mamIDCookie, c.Value)
			}
			return
		}
	}
}

// get issues an authenticated GET with the Cookie: mam_id=… header, captures any
// rotated mam_id from the response, and returns the response for the caller to
// interpret (404/429/2xx). The cookie rides as a header, never the URL, so the URL
// carries no secret; a transport error still surfaces only its scheme://host.
func (d *driver) get(ctx context.Context, rawurl, accept string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("myanonamouse: build request: %w", err)
	}
	// Set the request Cookie header directly (not http.Cookie): the mam_id is an
	// opaque session token, and Secure/HttpOnly/SameSite are response-only attributes
	// that never ride a request cookie anyway.
	req.Header.Set("Cookie", mamIDCookie+"="+d.mamID())
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("myanonamouse: request to %s: %w", apphttp.SchemeHost(rawurl), apphttp.RedactURLError(err))
	}
	d.captureRotatedMamID(ctx, resp)
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
