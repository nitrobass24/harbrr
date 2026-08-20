package speedapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	stdhttp "net/http"
	"strings"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

var authClassify = native.Classify{}.AlsoAuth(stdhttp.StatusUnauthorized)

type tokenVersion struct {
	value      string
	generation uint64
}

type tokenRefresh struct {
	done  chan struct{}
	token tokenVersion
	err   error
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token   string `json:"token"`
	Message string `json:"message"`
}

// tokenFor returns the cached token, joins an existing login, or performs the one
// required login outside authMu. rejected identifies a token a 401 proved stale.
func (d *driver) tokenFor(ctx context.Context, rejected *tokenVersion) (tokenVersion, error) {
	d.authMu.Lock()
	if d.token.value != "" && (rejected == nil || d.token != *rejected) {
		token := d.token
		d.authMu.Unlock()
		return token, nil
	}
	if d.refresh != nil {
		refresh := d.refresh
		d.authMu.Unlock()
		select {
		case <-ctx.Done():
			return tokenVersion{}, fmt.Errorf("speedapp: wait for token refresh: %w", ctx.Err())
		case <-refresh.done:
			return refresh.token, refresh.err
		}
	}

	refresh := &tokenRefresh{done: make(chan struct{})}
	d.refresh = refresh
	d.authMu.Unlock()

	var rejectedToken string
	if rejected != nil {
		rejectedToken = rejected.value
	}
	value, err := d.authenticate(ctx, rejectedToken)

	d.authMu.Lock()
	if err == nil {
		d.token = tokenVersion{value: value, generation: d.token.generation + 1}
		refresh.token = d.token
	}
	refresh.err = err
	d.refresh = nil
	close(refresh.done)
	d.authMu.Unlock()

	return refresh.token, refresh.err
}

func (d *driver) authenticate(ctx context.Context, runtimeSecrets ...string) (string, error) {
	body, err := json.Marshal(loginRequest{Username: d.Cfg["email"], Password: d.Cfg["password"]}) //nolint:gosec // G117: SpeedApp's required login JSON key is "password"; the body is never logged.
	if err != nil {
		return "", fmt.Errorf("speedapp: encode login request: %w", err)
	}
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, d.BaseURL+"api/login", bytes.NewReader(body))
	if err != nil {
		return "", d.ScrubErr(fmt.Errorf("speedapp: build login request: %w", apphttp.RedactURLError(err)), runtimeSecrets...)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.Do(ctx, req, authClassify)
	if err != nil {
		return "", d.ScrubErr(err, runtimeSecrets...)
	}

	var payload loginResponse
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		parseErr := fmt.Errorf("speedapp: decode login response: %s: %w", apphttp.DecodeErrorDetail(err, resp.Body), search.ErrParseError)
		return "", d.ScrubErr(parseErr, runtimeSecrets...)
	}
	token := strings.TrimSpace(payload.Token)
	if token != "" {
		return token, nil
	}

	reason := "login response did not contain a token"
	if message := strings.TrimSpace(payload.Message); message != "" {
		reason += ": " + message
	}
	return "", d.ScrubErr(fmt.Errorf("speedapp: %s: %w", reason, login.ErrLoginFailed), runtimeSecrets...)
}

// bearerGET performs one authenticated GET and retries it once after a generation-aware
// 401 refresh. It returns the token used for the final response so body errors can scrub it.
func (d *driver) bearerGET(ctx context.Context, rawURL, accept string, download bool) (*native.Response, tokenVersion, error) {
	token, err := d.tokenFor(ctx, nil)
	if err != nil {
		return nil, tokenVersion{}, err
	}
	resp, err := d.doBearerGET(ctx, rawURL, accept, download, token)
	if resp == nil || resp.StatusCode != stdhttp.StatusUnauthorized {
		return resp, token, err
	}

	refreshed, refreshErr := d.tokenFor(ctx, &token)
	if refreshErr != nil {
		return nil, token, refreshErr
	}
	resp, err = d.doBearerGET(ctx, rawURL, accept, download, refreshed)
	return resp, refreshed, err
}

func (d *driver) doBearerGET(ctx context.Context, rawURL, accept string, download bool, token tokenVersion) (*native.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, d.ScrubErr(fmt.Errorf("speedapp: build request: %w", apphttp.RedactURLError(err)), token.value)
	}
	req.Header.Set("Authorization", "Bearer "+token.value)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if download {
		resp, err := d.DoDownload(ctx, req, authClassify, token.value)
		return resp, d.ScrubErr(err, token.value)
	}
	resp, err := d.Do(ctx, req, authClassify)
	return resp, d.ScrubErr(err, token.value)
}
