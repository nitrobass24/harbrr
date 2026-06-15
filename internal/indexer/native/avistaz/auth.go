package avistaz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"time"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const (
	authPath     = "api/v1/jackett/auth"
	maxBodyBytes = 8 << 20 // 8 MiB cap on an auth/search/torrent response
)

// authResponse + errorResponse are the JSON the auth endpoint returns.
type authResponse struct {
	Token string `json:"token"`
}

type errorResponse struct {
	Message string `json:"message"`
}

// authenticate POSTs the form-encoded credentials to api/v1/jackett/auth and returns
// a fresh bearer token. Credentials live only in the request body, never the URL or
// a log. A 401/422 is an auth failure wrapped with login.ErrLoginFailed (so the
// registry records an auth_failure health event), surfacing the server's {message};
// a 429 is a rate-limit error.
func (d *driver) authenticate(ctx context.Context) (string, error) {
	form := url.Values{}
	form.Set("username", strings.TrimSpace(d.cfg["username"]))
	form.Set("password", strings.TrimSpace(d.cfg["password"]))
	form.Set("pid", strings.TrimSpace(d.cfg["pid"]))

	authURL := d.baseURL + authPath
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, authURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("avistaz: build auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := d.doer.Do(req)
	if err != nil {
		return "", fmt.Errorf("avistaz: auth request to %s: %w", apphttp.RedactURL(authURL), err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("avistaz: read auth response: %w", err)
	}

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusUnprocessableEntity:
		return "", fmt.Errorf("avistaz: %s: %w", d.authErrorMessage(body), login.ErrLoginFailed)
	case search.IsRateLimitStatus(resp.StatusCode):
		return "", &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return "", fmt.Errorf("avistaz: auth returned HTTP %d", resp.StatusCode)
	}

	var ar authResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return "", fmt.Errorf("avistaz: decode auth response: %w", err)
	}
	if ar.Token == "" {
		return "", fmt.Errorf("avistaz: auth response carried no token: %w", login.ErrLoginFailed)
	}
	return ar.Token, nil
}

// authErrorMessage returns the server's error message with any echoed credential
// scrubbed, or a generic fallback. A hostile or buggy server could reflect the
// submitted password/PID back in its {message}; that message is persisted verbatim as
// a health-event detail, and RedactError only catches key=value shapes — not the free
// prose a reflected credential would appear in — so the submitted secret values are
// scrubbed here before surfacing.
func (d *driver) authErrorMessage(body []byte) string {
	var er errorResponse
	if json.Unmarshal(body, &er) != nil || er.Message == "" {
		return "authentication failed"
	}
	return scrubSubmittedCredentials(er.Message, d.cfg)
}

// scrubSubmittedCredentials removes any occurrence of the submitted secret credential
// values (password, pid) from s. They are matched as submitted (trimmed) — the only
// form the server could have received. username is text, not secret-classified, so it
// is left intact.
func scrubSubmittedCredentials(s string, cfg map[string]string) string {
	for _, key := range [...]string{"password", "pid"} {
		if v := strings.TrimSpace(cfg[key]); v != "" {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	return s
}

// ensureToken returns the cached bearer or fetches one.
func (d *driver) ensureToken(ctx context.Context) (string, error) {
	d.mu.Lock()
	tok := d.token
	d.mu.Unlock()
	if tok != "" {
		return tok, nil
	}
	return d.refreshToken(ctx)
}

// refreshToken forces a fresh authentication and caches the token.
func (d *driver) refreshToken(ctx context.Context) (string, error) {
	tok, err := d.authenticate(ctx)
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	d.token = tok
	d.mu.Unlock()
	return tok, nil
}

// get issues an authenticated GET, reactively re-authenticating ONCE on a 401/412
// (Prowlarr's CheckIfLoginNeeded) and retrying. The caller owns the returned body
// and interprets the status (404/429/2xx). The URL may carry no secret (the bearer
// is a header), but errors still redact it.
func (d *driver) get(ctx context.Context, rawurl string) (*stdhttp.Response, error) {
	token, err := d.ensureToken(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := d.sendBearer(ctx, rawurl, token)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusPreconditionFailed {
		_ = resp.Body.Close()
		token, err = d.refreshToken(ctx)
		if err != nil {
			return nil, err
		}
		return d.sendBearer(ctx, rawurl, token)
	}
	return resp, nil
}

// sendBearer sends one GET with the Authorization: Bearer header.
func (d *driver) sendBearer(ctx context.Context, rawurl, token string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("avistaz: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("avistaz: request to %s: %w", apphttp.RedactURL(rawurl), err)
	}
	return resp, nil
}
