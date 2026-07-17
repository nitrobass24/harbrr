package api_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	josejose "github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"

	"github.com/autobrr/harbrr/internal/web/api"
)

// fakeOIDCProvider is a minimal httptest OpenID Connect provider: discovery,
// JWKS, an authorize endpoint (unused by the server-side flow — the client
// only ever GETs authorizationUrl in a browser), a token endpoint that mints a
// real RS256-signed ID token, and a userinfo endpoint. Just enough for
// coreos/go-oidc's discovery + ID-token verification to succeed end to end.
type fakeOIDCProvider struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	keyID    string
	clientID string         // aud claim in minted ID tokens — every test uses "client-id"
	claims   map[string]any // extra ID-token claims (username fields, sub, ...)
	userInfo map[string]any // userinfo-endpoint response (nil = 404)
	wantPKCE bool           // advertise S256 in discovery when true

	// expectChallenge, when set, makes the token endpoint require the form's
	// code_verifier and reject it unless base64url(sha256(verifier)) equals this
	// value — proving PKCE round-trips, not just that the challenge was issued.
	expectChallenge string
}

func newFakeOIDCProvider(t *testing.T, claims map[string]any, wantPKCE bool) *fakeOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	p := &fakeOIDCProvider{key: key, keyID: "test-key", clientID: "client-id", claims: claims, wantPKCE: wantPKCE}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("/jwks", p.jwks)
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/token", p.token)
	mux.HandleFunc("/userinfo", p.userinfoHandler)
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeOIDCProvider) discovery(w http.ResponseWriter, _ *http.Request) {
	doc := map[string]any{
		"issuer":                 p.srv.URL,
		"authorization_endpoint": p.srv.URL + "/authorize",
		"token_endpoint":         p.srv.URL + "/token",
		"userinfo_endpoint":      p.srv.URL + "/userinfo",
		"jwks_uri":               p.srv.URL + "/jwks",
	}
	if p.wantPKCE {
		doc["code_challenge_methods_supported"] = []string{"S256"}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func (p *fakeOIDCProvider) jwks(w http.ResponseWriter, _ *http.Request) {
	jwk := josejose.JSONWebKey{Key: &p.key.PublicKey, KeyID: p.keyID, Algorithm: "RS256", Use: "sig"}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(josejose.JSONWebKeySet{Keys: []josejose.JSONWebKey{jwk}})
}

// idToken mints a fresh RS256-signed ID token with the standard claims plus
// p.claims, valid for the given audience (the OAuth client id).
func (p *fakeOIDCProvider) idToken(audience string) (string, error) {
	signer, err := josejose.NewSigner(
		josejose.SigningKey{Algorithm: josejose.RS256, Key: p.key},
		(&josejose.SignerOptions{}).WithType("JWT").WithHeader("kid", p.keyID),
	)
	if err != nil {
		return "", fmt.Errorf("new signer: %w", err)
	}
	now := time.Now()
	claims := map[string]any{
		"iss": p.srv.URL,
		"aud": audience,
		"sub": "test-subject",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	for k, v := range p.claims {
		claims[k] = v
	}
	raw, err := josejwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		return "", fmt.Errorf("sign id token: %w", err)
	}
	return raw, nil
}

func (p *fakeOIDCProvider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// When a PKCE challenge was issued, verify the round-trip: the form's
	// code_verifier must S256-hash to the challenge harbrr sent to /authorize.
	if p.expectChallenge != "" {
		verifier := r.PostFormValue("code_verifier")
		sum := sha256.Sum256([]byte(verifier))
		if verifier == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != p.expectChallenge {
			http.Error(w, "pkce verification failed", http.StatusBadRequest)
			return
		}
	}
	// The oauth2 client may send client_id via Basic auth rather than the form
	// body (AuthStyleAutoDetect), so the fixture's own clientID is used as the
	// token's audience rather than trying to read it back off the request.
	idToken, err := p.idToken(p.clientID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := map[string]any{
		"access_token": "test-access-token",
		"id_token":     idToken,
		"token_type":   "Bearer",
		"expires_in":   3600,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (p *fakeOIDCProvider) userinfoHandler(w http.ResponseWriter, _ *http.Request) {
	if p.userInfo == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p.userInfo)
}

// oidcConfigBody is the /api/auth/oidc/config response shape (hand-rolled here
// rather than importing the unexported api.oidcConfigResponse type).
type oidcConfigBody struct {
	Enabled             bool   `json:"enabled"`
	AuthorizationURL    string `json:"authorizationUrl"`
	DisableBuiltInLogin bool   `json:"disableBuiltInLogin"`
	IssuerURL           string `json:"issuerUrl"`
}

// noRedirectClientFrom builds a client sharing c's cookie jar (so the
// session cookie /config set is honored) but that does not follow redirects —
// the callback's Location points at redirect_url's origin
// (https://harbrr.example.test), which doesn't resolve in a test environment,
// so the test must inspect the 302 itself rather than let the client chase it.
func noRedirectClientFrom(c *http.Client) *http.Client {
	return &http.Client{Jar: c.Jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func getOIDCConfigBody(t *testing.T, c *http.Client, base string) oidcConfigBody {
	t.Helper()
	resp, body := do(t, c, http.MethodGet, base+"/api/auth/oidc/config", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var cfg oidcConfigBody
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("decode oidc config: %v", err)
	}
	return cfg
}

// TestOIDCCallbackHappyPath is the full flow against an httptest OIDC
// provider: /config mints state (+ PKCE, since this fixture advertises S256)
// stored in the session, and the callback with a matching state establishes
// an authenticated session with the username derived from claims, plus the
// CSRF token login mints (autobrr/harbrr#9 test plan).
func TestOIDCCallbackHappyPath(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t, map[string]any{"preferred_username": "alice"}, true)
	base, c := serve(t, newEnv(t, api.Config{OIDC: api.OIDCConfig{
		Enabled: true, Issuer: provider.srv.URL, ClientID: "client-id", ClientSecret: "client-secret",
		RedirectURL: "https://harbrr.example.test/api/auth/oidc/callback",
	}}))

	cfg := getOIDCConfigBody(t, c, base)
	if !cfg.Enabled || cfg.AuthorizationURL == "" {
		t.Fatalf("oidc config = %+v, want enabled with an authorization URL", cfg)
	}
	authURL, err := url.Parse(cfg.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorization url: %v", err)
	}
	state := authURL.Query().Get("state")
	if state == "" {
		t.Fatal("authorization url carries no state")
	}
	challenge := authURL.Query().Get("code_challenge")
	if challenge == "" {
		t.Error("authorization url carries no code_challenge; want PKCE (provider advertises S256)")
	}
	// Make the token endpoint enforce the round-trip against the issued challenge.
	provider.expectChallenge = challenge

	resp, body := do(t, noRedirectClientFrom(c), http.MethodGet,
		fmt.Sprintf("%s/api/auth/oidc/callback?code=test-code&state=%s", base, state), nil, nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302 (body: %s)", resp.StatusCode, body)
	}
	location := resp.Header.Get("Location")
	if !strings.HasPrefix(location, "https://harbrr.example.test") {
		t.Errorf("redirect Location = %q, want it derived from redirect_url's origin", location)
	}
	assertSessionCookie(t, resp)

	resp, body = do(t, c, http.MethodGet, base+"/api/auth/me", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var me struct {
		Username  string `json:"username"`
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decode /me: %v", err)
	}
	if me.Username != "alice" {
		t.Errorf("username = %q, want %q (from preferred_username)", me.Username, "alice")
	}
	if me.CSRFToken == "" {
		t.Error("no csrf token issued by the oidc callback (login mirrors issueCSRFToken)")
	}
}

// TestOIDCCallbackUserInfoOverridesIDToken pins the claims-merge rule: a
// userinfo-endpoint claim overrides the ID token's for username derivation.
func TestOIDCCallbackUserInfoOverridesIDToken(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t, map[string]any{"preferred_username": "from-id-token"}, false)
	// Matching sub: userinfo is trusted only when its subject equals the verified
	// ID token's (test-subject, set in idToken).
	provider.userInfo = map[string]any{"sub": "test-subject", "preferred_username": "from-userinfo"}
	base, c := serve(t, newEnv(t, api.Config{OIDC: api.OIDCConfig{
		Enabled: true, Issuer: provider.srv.URL, ClientID: "client-id", ClientSecret: "client-secret",
		RedirectURL: "https://harbrr.example.test/api/auth/oidc/callback",
	}}))

	cfg := getOIDCConfigBody(t, c, base)
	authURL, _ := url.Parse(cfg.AuthorizationURL)
	state := authURL.Query().Get("state")
	if authURL.Query().Get("code_challenge") != "" {
		t.Error("code_challenge present but this provider does not advertise S256")
	}

	resp, body := do(t, noRedirectClientFrom(c), http.MethodGet,
		fmt.Sprintf("%s/api/auth/oidc/callback?code=test-code&state=%s", base, state), nil, nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302 (body: %s)", resp.StatusCode, body)
	}

	resp, body = do(t, c, http.MethodGet, base+"/api/auth/me", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `"username":"from-userinfo"`) {
		t.Errorf("/me = %s, want username from-userinfo (userinfo overrides id token)", body)
	}
}

// TestOIDCCallbackUserInfoSubjectMismatchIgnored pins the security rule that a
// userinfo response whose sub differs from the verified ID token's is discarded
// — a rogue/misconfigured userinfo endpoint must not override the session identity.
func TestOIDCCallbackUserInfoSubjectMismatchIgnored(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t, map[string]any{"preferred_username": "from-id-token"}, false)
	// Mismatched sub: this userinfo must be ignored entirely.
	provider.userInfo = map[string]any{"sub": "attacker-subject", "preferred_username": "from-userinfo"}
	base, c := serve(t, newEnv(t, api.Config{OIDC: api.OIDCConfig{
		Enabled: true, Issuer: provider.srv.URL, ClientID: "client-id", ClientSecret: "client-secret",
		RedirectURL: "https://harbrr.example.test/api/auth/oidc/callback",
	}}))

	cfg := getOIDCConfigBody(t, c, base)
	state, _ := url.Parse(cfg.AuthorizationURL)
	resp, body := do(t, noRedirectClientFrom(c), http.MethodGet,
		fmt.Sprintf("%s/api/auth/oidc/callback?code=test-code&state=%s", base, state.Query().Get("state")), nil, nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302 (body: %s)", resp.StatusCode, body)
	}
	resp, body = do(t, c, http.MethodGet, base+"/api/auth/me", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if strings.Contains(string(body), "from-userinfo") {
		t.Errorf("/me = %s, mismatched-subject userinfo must NOT override identity", body)
	}
	if !strings.Contains(string(body), `"username":"from-id-token"`) {
		t.Errorf("/me = %s, want the ID token's username to stand", body)
	}
}

// TestOIDCCallbackStateValidation pins the two 400 cases the test plan calls
// out: no state in the session at all, and a state that doesn't match.
func TestOIDCCallbackStateValidation(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t, nil, false)
	base, c := serve(t, newEnv(t, api.Config{OIDC: api.OIDCConfig{
		Enabled: true, Issuer: provider.srv.URL, ClientID: "client-id", ClientSecret: "client-secret",
		RedirectURL: "https://harbrr.example.test/api/auth/oidc/callback",
	}}))

	// No /config call yet: no state in the session.
	resp, body := do(t, c, http.MethodGet, base+"/api/auth/oidc/callback?code=x&state=whatever", nil, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)

	// A /config call mints a state, but the callback is hit with a different one.
	getOIDCConfigBody(t, c, base)
	resp, body = do(t, c, http.MethodGet, base+"/api/auth/oidc/callback?code=x&state=not-the-right-state", nil, nil)
	mustStatus(t, resp, body, http.StatusBadRequest)
}

// TestOIDCDisableBuiltInLoginReflectedInConfig pins the coexist + UI-hide
// design: disable_built_in_login flows straight through to /config.
func TestOIDCDisableBuiltInLoginReflectedInConfig(t *testing.T) {
	t.Parallel()

	provider := newFakeOIDCProvider(t, nil, false)
	base, c := serve(t, newEnv(t, api.Config{OIDC: api.OIDCConfig{
		Enabled: true, Issuer: provider.srv.URL, ClientID: "client-id", ClientSecret: "client-secret",
		RedirectURL: "https://harbrr.example.test/api/auth/oidc/callback", DisableBuiltInLogin: true,
	}}))

	cfg := getOIDCConfigBody(t, c, base)
	if !cfg.DisableBuiltInLogin {
		t.Error("disableBuiltInLogin = false, want true")
	}

	// POST /api/auth/login stays registered regardless (UI-level hide only).
	resp, body := do(t, c, http.MethodPost, base+"/api/auth/setup",
		map[string]string{"username": "admin", "password": "correct-horse-staple"}, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	resp, body = do(t, c, http.MethodPost, base+"/api/auth/login",
		map[string]string{"username": "admin", "password": "correct-horse-staple"}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
}
