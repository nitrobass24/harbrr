package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/web/api"
)

// serve starts an httptest server for the env and returns a base URL + a
// cookie-jar client (so a login cookie persists across requests).
// isSafeTestMethod mirrors the server's read-only method set (see csrf.go).
func isSafeTestMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func serve(t *testing.T, e *env) (string, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(e.handler)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return srv.URL, &http.Client{Jar: jar}
}

// do issues a request with an optional JSON body + headers and returns the
// response (body drained into a buffer for assertions).
func do(t *testing.T, c *http.Client, method, url string, body any, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// Mirror a browser client: echo the CSRF token from the (non-HttpOnly) companion
	// cookie on mutating requests, unless the caller set the header explicitly (e.g. to
	// test a bad token). Names mirror internal/web/api/csrf.go.
	if c.Jar != nil && !isSafeTestMethod(method) && req.Header.Get("X-CSRF-Token") == "" {
		for _, ck := range c.Jar.Cookies(req.URL) {
			if ck.Name == "harbrr_csrf" {
				req.Header.Set("X-CSRF-Token", ck.Value)
			}
		}
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func TestSetupLoginLogoutFlow(t *testing.T) {
	t.Parallel()

	base, c := serve(t, newEnv(t, api.Config{}))

	// Not set up yet.
	resp, body := do(t, c, http.MethodGet, base+"/api/auth/setup", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `"setupComplete":false`) {
		t.Errorf("setup status = %s, want setupComplete:false", body)
	}

	// Create the admin.
	resp, body = do(t, c, http.MethodPost, base+"/api/auth/setup",
		map[string]string{"username": "admin", "password": "correct-horse-staple"}, nil)
	mustStatus(t, resp, body, http.StatusCreated)

	// Setup is now complete; a second setup is rejected.
	resp, body = do(t, c, http.MethodPost, base+"/api/auth/setup",
		map[string]string{"username": "x", "password": "another-password"}, nil)
	mustStatus(t, resp, body, http.StatusConflict)

	// /me requires auth.
	resp, body = do(t, c, http.MethodGet, base+"/api/auth/me", nil, nil)
	mustStatus(t, resp, body, http.StatusUnauthorized)

	// Wrong password is rejected.
	resp, body = do(t, c, http.MethodPost, base+"/api/auth/login",
		map[string]string{"username": "admin", "password": "wrong"}, nil)
	mustStatus(t, resp, body, http.StatusUnauthorized)

	// Correct login sets a session cookie with the CSRF-posture flags (qui model).
	resp, body = do(t, c, http.MethodPost, base+"/api/auth/login",
		map[string]string{"username": "admin", "password": "correct-horse-staple"}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	assertSessionCookie(t, resp)

	// /me now works (session).
	resp, body = do(t, c, http.MethodGet, base+"/api/auth/me", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `"authMethod":"session"`) {
		t.Errorf("/me = %s, want authMethod:session", body)
	}

	// Logout, then /me is unauthorized again.
	resp, body = do(t, c, http.MethodPost, base+"/api/auth/logout", nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodGet, base+"/api/auth/me", nil, nil)
	mustStatus(t, resp, body, http.StatusUnauthorized)
}

func TestAPIKeyAuth(t *testing.T) {
	t.Parallel()

	e := newEnv(t, api.Config{})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	// Mint a key (plaintext returned once).
	resp, body := do(t, c, http.MethodPost, base+"/api/apikeys", map[string]string{"name": "sonarr"}, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	var minted struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &minted); err != nil || minted.Key == "" {
		t.Fatalf("mint response missing key: %s", body)
	}

	// A fresh client (no cookie) authenticates via X-API-Key.
	fresh := &http.Client{}
	resp, body = do(t, fresh, http.MethodGet, base+"/api/auth/me", nil, map[string]string{"X-API-Key": minted.Key})
	mustStatus(t, resp, body, http.StatusOK)
	if !strings.Contains(string(body), `"authMethod":"apikey"`) {
		t.Errorf("/me = %s, want authMethod:apikey", body)
	}

	// A wrong key is rejected; no key is rejected.
	resp, body = do(t, fresh, http.MethodGet, base+"/api/auth/me", nil, map[string]string{"X-API-Key": "nope"})
	mustStatus(t, resp, body, http.StatusUnauthorized)
	resp, body = do(t, fresh, http.MethodGet, base+"/api/auth/me", nil, nil)
	mustStatus(t, resp, body, http.StatusUnauthorized)
}

func TestAuthDisabledIPAllowlist(t *testing.T) {
	t.Parallel()

	// Loopback allowed → synthetic admin, no creds needed.
	base, c := serve(t, newEnv(t, api.Config{AuthDisabled: true, IPAllowlist: []string{"127.0.0.0/8", "::1/128"}}))
	resp, body := do(t, c, http.MethodGet, base+"/api/indexers", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)

	// Allowlist that excludes loopback → rejected even in disabled mode.
	base2, c2 := serve(t, newEnv(t, api.Config{AuthDisabled: true, IPAllowlist: []string{"10.0.0.0/8"}}))
	resp, body = do(t, c2, http.MethodGet, base2+"/api/indexers", nil, nil)
	mustStatus(t, resp, body, http.StatusUnauthorized)
}

func TestAuthDisabledIgnoresUntrustedXFF(t *testing.T) {
	t.Parallel()

	// Allowlist permits 10.0.0.1 but the httptest peer is loopback, which is NOT a
	// trusted proxy. A spoofed X-Forwarded-For: 10.0.0.1 must be ignored, so the
	// request is rejected (the allowlist is checked against the real peer).
	base, c := serve(t, newEnv(t, api.Config{AuthDisabled: true, IPAllowlist: []string{"10.0.0.1/32"}}))
	resp, body := do(t, c, http.MethodGet, base+"/api/indexers", nil,
		map[string]string{"X-Forwarded-For": "10.0.0.1"})
	mustStatus(t, resp, body, http.StatusUnauthorized)
}

func TestAuthDisabledRequiresAllowlist(t *testing.T) {
	t.Parallel()

	_, err := api.NewRouter(api.Deps{Logger: zerolog.Nop()}, api.Config{AuthDisabled: true})
	if err == nil {
		t.Fatal("auth_disabled with an empty allowlist must fail closed")
	}
}

// TestOIDCDisabledAnswersConfig pins the disabled-by-default behavior every
// OIDC test plan case builds on: /config always answers 200 with
// enabled:false (never an error a logged-out visitor could trip on), and the
// callback is unreachable (404, "OIDC is not configured") rather than
// pretending to process a code/state it has no provider to validate.
func TestOIDCDisabledAnswersConfig(t *testing.T) {
	t.Parallel()

	base, c := serve(t, newEnv(t, api.Config{}))

	resp, body := do(t, c, http.MethodGet, base+"/api/auth/oidc/config", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var cfg struct {
		Enabled             bool   `json:"enabled"`
		AuthorizationURL    string `json:"authorizationUrl"`
		DisableBuiltInLogin bool   `json:"disableBuiltInLogin"`
		IssuerURL           string `json:"issuerUrl"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("decode oidc config: %v", err)
	}
	if cfg.Enabled || cfg.AuthorizationURL != "" || cfg.DisableBuiltInLogin || cfg.IssuerURL != "" {
		t.Errorf("oidc config = %+v, want the disabled default", cfg)
	}

	resp, body = do(t, c, http.MethodGet, base+"/api/auth/oidc/callback", nil, nil)
	mustStatus(t, resp, body, http.StatusNotFound)
}

func TestIndexerCRUDViaAPIRedactsSecrets(t *testing.T) {
	t.Parallel()

	e := newEnv(t, api.Config{})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	// Add an indexer with a secret setting.
	add := map[string]any{
		"slug": "tt", "definitionId": "testtracker",
		"settings": map[string]string{"apikey": "SUPER-SECRET-VALUE"},
	}
	resp, body := do(t, c, http.MethodPost, base+"/api/indexers", add, nil)
	mustStatus(t, resp, body, http.StatusCreated)

	// Get it back: the secret value is redacted, never the plaintext.
	resp, body = do(t, c, http.MethodGet, base+"/api/indexers/tt", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	if strings.Contains(string(body), "SUPER-SECRET-VALUE") {
		t.Error("GET indexer leaked the plaintext secret")
	}
	var detail struct {
		Settings []struct {
			Name   string `json:"name"`
			Value  string `json:"value"`
			Secret bool   `json:"secret"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode indexer detail: %v", err)
	}
	var sawSecret bool
	for _, s := range detail.Settings {
		if s.Name == "apikey" {
			sawSecret = true
			if !s.Secret || s.Value != secrets.Redacted {
				t.Errorf("apikey setting = %+v, want redacted secret", s)
			}
		}
	}
	if !sawSecret {
		t.Errorf("apikey setting missing from response: %s", body)
	}

	// Disable then delete.
	resp, body = do(t, c, http.MethodPost, base+"/api/indexers/tt/disable", nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodDelete, base+"/api/indexers/tt", nil, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
	resp, body = do(t, c, http.MethodGet, base+"/api/indexers/tt", nil, nil)
	mustStatus(t, resp, body, http.StatusNotFound)
}

// TestListIndexersReportsFreeleechState pins the list-time freeleech view
// (autobrr/harbrr#188): GET /api/indexers surfaces each instance's freeleech-only
// checkbox using the same canonical rule the engine applies at build time.
func TestListIndexersReportsFreeleechState(t *testing.T) {
	t.Parallel()

	e := newEnv(t, api.Config{})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)

	type addPayload struct {
		Slug         string            `json:"slug"`
		DefinitionID string            `json:"definitionId"`
		Settings     map[string]string `json:"settings,omitempty"`
	}
	tests := []struct {
		name string
		add  addPayload
		want bool
	}{
		{
			name: "freeleech unset -> off",
			add:  addPayload{Slug: "off", DefinitionID: "testtracker"},
			want: false,
		},
		{
			name: "freeleech checked -> on",
			add: addPayload{
				Slug: "on", DefinitionID: "testtracker",
				Settings: map[string]string{"freeleech": "true"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		resp, body := do(t, c, http.MethodPost, base+"/api/indexers", tt.add, nil)
		mustStatus(t, resp, body, http.StatusCreated)
	}

	resp, body := do(t, c, http.MethodGet, base+"/api/indexers", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)

	var list []struct {
		Slug      string `json:"slug"`
		Freeleech bool   `json:"freeleech"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode indexer list: %v", err)
	}
	got := make(map[string]bool, len(list))
	for _, item := range list {
		got[item.Slug] = item.Freeleech
	}
	for _, tt := range tests {
		if got[tt.add.Slug] != tt.want {
			t.Errorf("%s: indexer %q freeleech = %v, want %v", tt.name, tt.add.Slug, got[tt.add.Slug], tt.want)
		}
	}
}

// setupAndLogin creates the admin and logs the client in.
func setupAndLogin(t *testing.T, base string, c *http.Client) {
	t.Helper()
	resp, body := do(t, c, http.MethodPost, base+"/api/auth/setup",
		map[string]string{"username": "admin", "password": "correct-horse-staple"}, nil)
	mustStatus(t, resp, body, http.StatusCreated)
	resp, body = do(t, c, http.MethodPost, base+"/api/auth/login",
		map[string]string{"username": "admin", "password": "correct-horse-staple"}, nil)
	mustStatus(t, resp, body, http.StatusNoContent)
}

// assertSessionCookie checks the qui CSRF posture: HttpOnly + SameSite=Lax.
func assertSessionCookie(t *testing.T, resp *http.Response) {
	t.Helper()
	for _, ck := range resp.Cookies() {
		if ck.Name != "harbrr_session" {
			continue
		}
		if !ck.HttpOnly {
			t.Error("session cookie is not HttpOnly")
		}
		if ck.SameSite != http.SameSiteLaxMode {
			t.Errorf("session cookie SameSite = %v, want Lax", ck.SameSite)
		}
		return
	}
	t.Error("no harbrr_session cookie set on login")
}

func mustStatus(t *testing.T, resp *http.Response, body []byte, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s: status = %d, want %d (body: %s)", resp.Request.URL.Path, resp.StatusCode, want, body)
	}
}
