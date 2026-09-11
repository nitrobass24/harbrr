// OIDC/SSO login (autobrr/harbrr#9). The provider-discovery/PKCE/claims-merge
// flow below is ported from autobrr/qui's internal/api/handlers/oidc.go and
// adapted to harbrr's router/session conventions (session-only gate — no
// local user row; explicit absolute redirect_url; coexist with password login
// behind DisableBuiltInLogin).
//
// Copyright (c) 2025-2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later
package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	oidcInitMaxAttempts    = 5
	oidcInitInitialBackoff = 250 * time.Millisecond

	// SCS session keys the config/callback pair hands off through. Cleared as
	// soon as they are consumed (or superseded by a fresh /config call).
	sessionOIDCState = "oidc_state"
	sessionOIDCPKCE  = "oidc_pkce_verifier"
)

// oidcNewProvider/oidcSleep are package vars (not direct calls) so a test can stub
// out network discovery and the retry backoff sleep.
var (
	oidcNewProvider = oidc.NewProvider
	oidcSleep       = time.Sleep

	// oidcInitTimeout bounds the WHOLE startup discovery — every attempt and every
	// backoff together (autobrr/harbrr#651). NewRouter runs before the listener is
	// bound (app.New -> newServer), so an issuer that accepts the TCP connection and
	// then never answers used to hang startup outright: nothing was served, /healthz
	// included, and the retry loop — which bounds attempts, not wall time — was never
	// even reached. go-oidc builds its discovery request from the context handed to
	// oidc.NewProvider, so this one deadline interrupts a stalled connect, stalled
	// response headers and a never-finishing body alike; no separately-timed
	// http.Client is needed. A var so a test can shorten it.
	oidcInitTimeout = 10 * time.Second
)

// errOIDCTokenInvalid marks an ID-token verification failure, mapped to 401 by
// writeServiceError (an exchange failure, by contrast, falls through to the
// default 500 — it isn't the caller's credential that's wrong).
var errOIDCTokenInvalid = errors.New("oidc: id token verification failed")

// OIDCConfig is the OIDC/SSO login posture the router acts on (mapped from
// config.AuthConfig.OIDC by the composition root, mirroring api.Config's other
// primitive-typed fields rather than importing internal/config here).
type OIDCConfig struct {
	Enabled             bool
	Issuer              string
	ClientID            string
	ClientSecret        string
	RedirectURL         string
	DisableBuiltInLogin bool
}

// oidcHandler holds a successfully-discovered provider + the derived verifier/
// oauth2 config. A nil *oidcHandler on router means OIDC is disabled or its
// discovery failed at startup — every handler below treats that as "answer as
// disabled", never as an error.
type oidcHandler struct {
	cfg      OIDCConfig
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

// newOIDCHandler discovers the provider (retrying — see discoverOIDCProvider)
// and builds the verifier + oauth2.Config. Scopes are hardcoded, mirroring qui.
func newOIDCHandler(ctx context.Context, cfg OIDCConfig) (*oidcHandler, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		return nil, errors.New("oidc: issuer, client_id, client_secret, and redirect_url are all required")
	}
	provider, _, err := discoverOIDCProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover provider: %w", err)
	}
	return &oidcHandler{
		cfg:      cfg,
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{"openid", "profile", "email"},
		},
	}, nil
}

// discoverOIDCProvider retries discovery oidcInitMaxAttempts times (doubling
// backoff from oidcInitInitialBackoff), trying both the issuer as configured
// and its slash-toggled variant on each attempt — some IdPs are picky about
// (or slow to serve) the trailing slash, and a slow IdP on the instance's own
// first boot is qui's documented gotcha this retry loop exists for.
//
// ctx bounds the loop as a whole: once it is done, every remaining attempt would fail
// instantly anyway, so giving up immediately keeps the remaining backoff sleeps from
// adding seconds on top of an already-expired deadline.
func discoverOIDCProvider(ctx context.Context, issuer string) (*oidc.Provider, string, error) {
	candidates := []string{issuer}
	if strings.HasSuffix(issuer, "/") {
		candidates = append(candidates, strings.TrimRight(issuer, "/"))
	} else {
		candidates = append(candidates, issuer+"/")
	}

	var lastErr error
	for attempt := 1; attempt <= oidcInitMaxAttempts; attempt++ {
		for _, candidate := range candidates {
			provider, err := oidcNewProvider(ctx, candidate)
			if err == nil {
				return provider, candidate, nil
			}
			lastErr = err
		}
		if ctx.Err() != nil {
			break
		}
		if attempt < oidcInitMaxAttempts {
			oidcSleep(oidcInitInitialBackoff << (attempt - 1))
		}
	}
	return nil, "", fmt.Errorf("attempted %d times without success: %w", oidcInitMaxAttempts, lastErr)
}

// oidcConfigResponse is GET /api/auth/oidc/config's body.
type oidcConfigResponse struct {
	Enabled             bool   `json:"enabled"`
	AuthorizationURL    string `json:"authorizationUrl"`
	DisableBuiltInLogin bool   `json:"disableBuiltInLogin"`
	IssuerURL           string `json:"issuerUrl"`
}

// supportsPKCE reports whether the discovered provider advertises the S256
// code-challenge method.
func (h *oidcHandler) supportsPKCE() bool {
	var claims struct {
		CodeChallengeMethods []string `json:"code_challenge_methods_supported"`
	}
	if err := h.provider.Claims(&claims); err != nil {
		return false
	}
	return slices.Contains(claims.CodeChallengeMethods, "S256")
}

// configResponse builds the /config response plus the state (and, when the
// provider supports PKCE, the verifier) the caller must stash in the session.
func (h *oidcHandler) configResponse() (resp oidcConfigResponse, state, verifier string) {
	// rand.Text is the stdlib's CSPRNG-backed random string (~130 bits, base32) and
	// cannot fail; oauth2.GenerateVerifier is oauth2's own RFC 7636 verifier. Both
	// panic rather than return an error if the system CSPRNG is broken, which is the
	// only way they can fail and not a condition to paper over.
	state = rand.Text()

	var authURL string
	if h.supportsPKCE() {
		verifier = oauth2.GenerateVerifier()
		authURL = h.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
	} else {
		authURL = h.oauth.AuthCodeURL(state)
	}

	return oidcConfigResponse{
		Enabled:             h.cfg.Enabled,
		AuthorizationURL:    authURL,
		DisableBuiltInLogin: h.cfg.DisableBuiltInLogin,
		IssuerURL:           h.cfg.Issuer,
	}, state, verifier
}

// oidcClaims is the subset of ID-token/userinfo claims the username derivation
// (preferred_username → nickname → name → email → sub) reads.
type oidcClaims struct {
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	Nickname          string `json:"nickname"`
	Sub               string `json:"sub"`
}

// username derives the session username per the preferred_username → nickname
// → name → email → sub fallback chain (mirroring qui); "oidc_user" is the last
// resort when a provider supplies none of them.
func (c oidcClaims) username() string {
	for _, v := range []string{c.PreferredUsername, c.Nickname, c.Name, c.Email, c.Sub} {
		if v != "" {
			return v
		}
	}
	return "oidc_user"
}

// exchange trades the authorization code for a token, adding the PKCE verifier
// when the /config call generated one.
func (h *oidcHandler) exchange(ctx context.Context, code, pkceVerifier string) (*oauth2.Token, error) {
	var opts []oauth2.AuthCodeOption
	if pkceVerifier != "" {
		opts = append(opts, oauth2.VerifierOption(pkceVerifier))
	}
	token, err := h.oauth.Exchange(ctx, code, opts...)
	if err != nil {
		return nil, fmt.Errorf("oidc: exchange token: %w", err)
	}
	return token, nil
}

// verifyAndDeriveUsername verifies the token's ID token, merges in userinfo-
// endpoint claims (when the endpoint is reachable — best-effort, matching
// qui), and derives the session username. A verification failure wraps
// errOIDCTokenInvalid so the callback handler answers 401.
func (h *oidcHandler) verifyAndDeriveUsername(ctx context.Context, token *oauth2.Token) (string, error) {
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return "", fmt.Errorf("%w: no id_token in token response", errOIDCTokenInvalid)
	}
	idToken, err := h.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errOIDCTokenInvalid, err)
	}
	var claims oidcClaims
	if err := idToken.Claims(&claims); err != nil {
		return "", fmt.Errorf("%w: parse claims: %w", errOIDCTokenInvalid, err)
	}
	// Best-effort: a userinfo-endpoint failure keeps the ID token's claims
	// rather than failing the whole login (mirrors qui). go-oidc does NOT check
	// that the userinfo subject matches the verified ID token's, so we do — per
	// the OIDC spec, a userinfo response whose sub differs must be discarded, or
	// a rogue/misconfigured endpoint could override the session identity.
	if userInfo, err := h.provider.UserInfo(ctx, oauth2.StaticTokenSource(token)); err == nil && userInfo.Subject == claims.Sub {
		var fromUserInfo oidcClaims
		if err := userInfo.Claims(&fromUserInfo); err == nil {
			claims = mergeOIDCClaims(claims, fromUserInfo)
		}
	}
	return claims.username(), nil
}

// mergeOIDCClaims overlays every non-empty field of over onto base (userinfo
// claims win when present, mirroring qui).
func mergeOIDCClaims(base, over oidcClaims) oidcClaims {
	if over.Email != "" {
		base.Email = over.Email
	}
	if over.PreferredUsername != "" {
		base.PreferredUsername = over.PreferredUsername
	}
	if over.Name != "" {
		base.Name = over.Name
	}
	if over.Nickname != "" {
		base.Nickname = over.Nickname
	}
	if over.Sub != "" {
		base.Sub = over.Sub
	}
	return base
}

// postLoginRedirect is where the callback sends the browser after a
// successful login: the origin is taken from RedirectURL (never from the
// request's Host/X-Forwarded-Host — the operator-configured value is the only
// input, so there is no open-redirect surface here), with BaseURL as the path.
func oidcPostLoginRedirect(redirectURL, baseURL string) string {
	path := baseURL
	if path == "" {
		path = "/"
	}
	u, err := url.Parse(redirectURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return path
	}
	return u.Scheme + "://" + u.Host + path
}
