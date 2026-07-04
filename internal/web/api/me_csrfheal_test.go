package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestMeBackfillsCSRFForTokenlessSession proves the #56 fix: a session that predates
// CSRF binding (authenticated, but no csrf_token — e.g. a 30-day session from a build
// before the CSRF feature) gets a token minted on its next /api/auth/me call, so
// mutations stop 403-ing with no forced re-login. Store keys mirror internal/web/api
// (middleware.go / csrf.go): "authenticated", "username".
func TestMeBackfillsCSRFForTokenlessSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t, api.Config{})
	base, c := serve(t, e)

	// Fabricate an authenticated-but-tokenless session directly in the store — login
	// always mints a token, so it can't reproduce the legacy state.
	sctx, err := e.sessions.Load(context.Background(), "")
	if err != nil {
		t.Fatalf("session load: %v", err)
	}
	e.sessions.Put(sctx, "authenticated", true)
	e.sessions.Put(sctx, "username", "admin")
	sessTok, _, err := e.sessions.Commit(sctx)
	if err != nil {
		t.Fatalf("session commit: %v", err)
	}
	u, _ := url.Parse(base)
	//nolint:gosec // G124: a test client cookie injected into the jar to mimic a browser; Secure/HttpOnly/SameSite are the server's to set, not this fixture's.
	c.Jar.SetCookies(u, []*http.Cookie{{Name: "harbrr_session", Value: sessTok}})

	// /me heals the session: it mints and returns a token (and sets the harbrr_csrf
	// companion cookie, captured by the jar).
	resp, body := do(t, c, http.MethodGet, base+"/api/auth/me", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)
	var me struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(body, &me); err != nil || me.CSRFToken == "" {
		t.Fatalf("/me should mint + return a csrfToken for a tokenless session: %s", body)
	}

	// The companion cookie now lets do auto-attach the token, so a mutation that would
	// have 403'd forever succeeds.
	resp, body = do(t, c, http.MethodPost, base+"/api/apikeys", map[string]string{"name": "healed"}, nil)
	mustStatus(t, resp, body, http.StatusCreated)
}

// TestMeDoesNotMintCSRFForAPIKeyCaller pins the gate: an API-key caller has no session,
// so /me must not materialize one (no csrfToken, no session/companion cookies).
func TestMeDoesNotMintCSRFForAPIKeyCaller(t *testing.T) {
	t.Parallel()
	base, c := serve(t, newEnv(t, api.Config{}))
	setupAndLogin(t, base, c)

	_, body := do(t, c, http.MethodPost, base+"/api/apikeys", map[string]string{"name": "k"}, nil)
	var minted struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &minted); err != nil || minted.Key == "" {
		t.Fatalf("mint key: %s", body)
	}

	fresh := &http.Client{}
	resp, body := do(t, fresh, http.MethodGet, base+"/api/auth/me", nil, map[string]string{"X-API-Key": minted.Key})
	mustStatus(t, resp, body, http.StatusOK)
	var me struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decode /me: %s", body)
	}
	if me.CSRFToken != "" {
		t.Errorf("apikey caller should get an empty csrfToken, got %q", me.CSRFToken)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "harbrr_session" || ck.Name == "harbrr_csrf" {
			t.Errorf("apikey /me must not set %s", ck.Name)
		}
	}
}
