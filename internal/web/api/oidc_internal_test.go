package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// stubOIDCProvider swaps oidcNewProvider/oidcSleep for the duration of the
// test, restoring the originals on cleanup.
func stubOIDCProvider(t *testing.T, newProvider func(ctx context.Context, issuer string) (*oidc.Provider, error)) {
	t.Helper()
	origProvider, origSleep := oidcNewProvider, oidcSleep
	oidcNewProvider = newProvider
	oidcSleep = func(time.Duration) {} // no real backoff in tests
	t.Cleanup(func() { oidcNewProvider, oidcSleep = origProvider, origSleep })
}

// TestDiscoverOIDCProviderRetriesUntilSuccess pins the retry loop (autobrr/harbrr#9):
// a slow IdP that fails the first attempts still initializes once it comes up,
// within oidcInitMaxAttempts.
//
// Not t.Parallel(): it stubs the package-level oidcNewProvider/oidcSleep vars
// (as does every other test in this file that does the same), so these must
// run serially relative to each other.
func TestDiscoverOIDCProviderRetriesUntilSuccess(t *testing.T) {
	attempts := 0
	stubOIDCProvider(t, func(context.Context, string) (*oidc.Provider, error) {
		attempts++
		if attempts < 3 {
			return nil, fmt.Errorf("attempt %d", attempts)
		}
		return &oidc.Provider{}, nil
	})

	issuer := "https://issuer.example.com"
	provider, usedIssuer, err := discoverOIDCProvider(context.Background(), issuer)
	if err != nil {
		t.Fatalf("discoverOIDCProvider: %v", err)
	}
	if provider == nil {
		t.Fatal("provider is nil")
	}
	if usedIssuer != issuer {
		t.Errorf("usedIssuer = %q, want %q", usedIssuer, issuer)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

// TestDiscoverOIDCProviderFailsAfterMaxAttempts pins the give-up behavior: an
// IdP that never comes up fails after exactly oidcInitMaxAttempts rounds
// (trying both issuer slash variants each round).
// Not t.Parallel(): see TestDiscoverOIDCProviderRetriesUntilSuccess.
func TestDiscoverOIDCProviderFailsAfterMaxAttempts(t *testing.T) {
	calls := 0
	stubOIDCProvider(t, func(context.Context, string) (*oidc.Provider, error) {
		calls++
		return nil, fmt.Errorf("attempt %d", calls)
	})

	_, usedIssuer, err := discoverOIDCProvider(context.Background(), "https://issuer.example.com")
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if usedIssuer != "" {
		t.Errorf("usedIssuer = %q, want empty", usedIssuer)
	}
	if want := oidcInitMaxAttempts * 2; calls != want {
		t.Errorf("calls = %d, want %d (both slash variants x %d attempts)", calls, want, oidcInitMaxAttempts)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("attempted %d times", oidcInitMaxAttempts)) {
		t.Errorf("error = %v, want it to mention the attempt count", err)
	}
}

// TestDiscoverOIDCProviderTriesBothSlashVariants pins the slash-variant retry:
// an issuer some IdPs only serve without (or with) the trailing slash still
// resolves.
// Not t.Parallel(): see TestDiscoverOIDCProviderRetriesUntilSuccess.
func TestDiscoverOIDCProviderTriesBothSlashVariants(t *testing.T) {
	issuer := "https://issuer.example.com/"
	trimmed := strings.TrimRight(issuer, "/")
	var tried []string
	stubOIDCProvider(t, func(_ context.Context, candidate string) (*oidc.Provider, error) {
		tried = append(tried, candidate)
		if candidate == trimmed {
			return &oidc.Provider{}, nil
		}
		return nil, errors.New("not this one")
	})

	provider, usedIssuer, err := discoverOIDCProvider(context.Background(), issuer)
	if err != nil {
		t.Fatalf("discoverOIDCProvider: %v (tried %v)", err, tried)
	}
	if provider == nil {
		t.Fatal("provider is nil")
	}
	if usedIssuer != trimmed {
		t.Errorf("usedIssuer = %q, want %q", usedIssuer, trimmed)
	}
	if len(tried) < 2 || tried[0] != issuer || tried[1] != trimmed {
		t.Errorf("tried = %v, want [%q, %q, ...]", tried, issuer, trimmed)
	}
}

// TestOIDCClaimsUsername pins the fallback chain: preferred_username →
// nickname → name → email → sub → "oidc_user".
func TestOIDCClaimsUsername(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		claims oidcClaims
		want   string
	}{
		{"preferred_username wins", oidcClaims{PreferredUsername: "pu", Nickname: "nn", Name: "n", Email: "e", Sub: "s"}, "pu"},
		{"falls back to nickname", oidcClaims{Nickname: "nn", Name: "n", Email: "e", Sub: "s"}, "nn"},
		{"falls back to name", oidcClaims{Name: "n", Email: "e", Sub: "s"}, "n"},
		{"falls back to email", oidcClaims{Email: "e", Sub: "s"}, "e"},
		{"falls back to sub", oidcClaims{Sub: "s"}, "s"},
		{"falls back to oidc_user", oidcClaims{}, "oidc_user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.claims.username(); got != tt.want {
				t.Errorf("username() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestMergeOIDCClaimsUserInfoWins pins the merge order: a non-empty userinfo
// claim overrides the ID token's, but a userinfo endpoint that omits a field
// (or fails entirely — the caller's job) never blanks out what the ID token
// already had.
func TestMergeOIDCClaimsUserInfoWins(t *testing.T) {
	t.Parallel()

	base := oidcClaims{PreferredUsername: "id-token-user", Email: "idtoken@example.com"}
	over := oidcClaims{PreferredUsername: "userinfo-user"} // Email omitted from userinfo response

	got := mergeOIDCClaims(base, over)
	if got.PreferredUsername != "userinfo-user" {
		t.Errorf("PreferredUsername = %q, want userinfo to win", got.PreferredUsername)
	}
	if got.Email != "idtoken@example.com" {
		t.Errorf("Email = %q, want the ID token's value preserved", got.Email)
	}
}

// TestOIDCPostLoginRedirect pins the anti-open-redirect design: the redirect
// origin comes only from the operator-configured redirect_url (never from a
// request header), with the app base path appended.
func TestOIDCPostLoginRedirect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		redirectURL string
		baseURL     string
		want        string
	}{
		{"root base path", "https://harbrr.example.com/api/auth/oidc/callback", "", "https://harbrr.example.com/"},
		{"subpath base path", "https://harbrr.example.com/api/auth/oidc/callback", "/harbrr", "https://harbrr.example.com/harbrr"},
		{"malformed redirect url falls back to base path", "not-a-url", "/harbrr", "/harbrr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := oidcPostLoginRedirect(tt.redirectURL, tt.baseURL); got != tt.want {
				t.Errorf("oidcPostLoginRedirect(%q, %q) = %q, want %q", tt.redirectURL, tt.baseURL, got, tt.want)
			}
		})
	}
}

// TestNewOIDCHandlerRequiresAllFields pins the fail-fast validation newOIDCHandler
// applies before ever attempting discovery.
func TestNewOIDCHandlerRequiresAllFields(t *testing.T) {
	t.Parallel()

	complete := OIDCConfig{Issuer: "https://issuer.example.com", ClientID: "id", ClientSecret: "secret", RedirectURL: "https://harbrr.example.com/api/auth/oidc/callback"}
	tests := []struct {
		name   string
		mutate func(*OIDCConfig)
	}{
		{"missing issuer", func(c *OIDCConfig) { c.Issuer = "" }},
		{"missing client id", func(c *OIDCConfig) { c.ClientID = "" }},
		{"missing client secret", func(c *OIDCConfig) { c.ClientSecret = "" }},
		{"missing redirect url", func(c *OIDCConfig) { c.RedirectURL = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := complete
			tt.mutate(&cfg)
			if _, err := newOIDCHandler(context.Background(), cfg); err == nil {
				t.Error("want an error, got nil")
			}
		})
	}
}
