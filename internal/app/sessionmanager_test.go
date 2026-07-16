package app

import (
	"testing"

	"github.com/autobrr/harbrr/internal/config"
)

// TestSessionManagerCookieSecure covers the Secure derivation matrix (issue #10):
// the manual secure_cookie override, external_url's https scheme implying Secure
// automatically, and plain http with neither set staying insecure. Secure is
// computed once here at construction — never mutated per-request.
func TestSessionManagerCookieSecure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		secureCookie bool
		externalURL  string
		want         bool
	}{
		{"neither set", false, "", false},
		{"secure_cookie=true, no external_url", true, "", true},
		{"external_url https implies secure", false, "https://harbrr.example.com", true},
		{"external_url http does not imply secure", false, "http://harbrr.example.com", false},
		{"secure_cookie=true and external_url http", true, "http://harbrr.example.com", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Defaults()
			cfg.Server.SecureCookie = tt.secureCookie
			cfg.Server.ExternalURL = tt.externalURL
			sm := sessionManager(nil, &cfg)
			if got := sm.Cookie.Secure; got != tt.want {
				t.Errorf("sessionManager(...).Cookie.Secure = %v, want %v", got, tt.want)
			}
		})
	}
}
