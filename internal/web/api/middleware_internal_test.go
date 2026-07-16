package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
)

// newAllowlistRouter builds a router with the given trusted-proxy and allowlist
// CIDRs/IPs for clientIP/ipAllowed tests.
func newAllowlistRouter(t *testing.T, allow, proxies []string) *router {
	t.Helper()
	a, err := apphttp.ParseCIDRs(allow)
	if err != nil {
		t.Fatalf("parse allowlist: %v", err)
	}
	p, err := apphttp.ParseTrustedProxies(proxies)
	if err != nil {
		t.Fatalf("parse proxies: %v", err)
	}
	return &router{allowlist: a, trustedProxies: p}
}

func reqWith(remote, xff string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/indexers", nil)
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

// TestClientIPXFFSpoof pins the fix: with a trusted proxy in front, the client IP
// is the rightmost non-proxy hop, so a client cannot forge an allowlisted leftmost
// X-Forwarded-For entry to bypass the auth.mode=disabled allowlist.
func TestClientIPXFFSpoof(t *testing.T) {
	t.Parallel()
	rt := newAllowlistRouter(
		t,
		[]string{"1.2.3.4"},    // allowlisted admin IP
		[]string{"10.0.0.0/8"}, // trusted reverse proxy network
	)

	tests := []struct {
		name        string
		remote      string
		xff         string
		wantIP      string
		wantAllowed bool
	}{
		{
			// Attacker forges the allowlisted IP at the left; the proxy appends the
			// attacker's real IP at the right. Rightmost non-proxy = attacker → denied.
			name:        "spoofed leftmost allowlisted entry is ignored",
			remote:      "10.0.0.1:5000",
			xff:         "1.2.3.4, 9.9.9.9",
			wantIP:      "9.9.9.9",
			wantAllowed: false,
		},
		{
			name:        "genuine allowlisted client behind one proxy",
			remote:      "10.0.0.1:5000",
			xff:         "1.2.3.4",
			wantIP:      "1.2.3.4",
			wantAllowed: true,
		},
		{
			name:        "allowlisted client behind a chain of trusted proxies",
			remote:      "10.0.0.1:5000",
			xff:         "1.2.3.4, 10.0.0.2, 10.0.0.3",
			wantIP:      "1.2.3.4",
			wantAllowed: true,
		},
		{
			name:        "non-proxy peer: XFF ignored entirely",
			remote:      "9.9.9.9:5000",
			xff:         "1.2.3.4",
			wantIP:      "9.9.9.9",
			wantAllowed: false,
		},
		{
			name:        "no XFF: direct peer used",
			remote:      "1.2.3.4:5000",
			xff:         "",
			wantIP:      "1.2.3.4",
			wantAllowed: true,
		},
		{
			name:        "all forwarded hops are trusted proxies: falls back to peer",
			remote:      "10.0.0.1:5000",
			xff:         "10.0.0.2, 10.0.0.3",
			wantIP:      "10.0.0.1",
			wantAllowed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := reqWith(tt.remote, tt.xff)
			if got := rt.clientIP(r); got == nil || got.String() != tt.wantIP {
				t.Fatalf("clientIP = %v, want %s", got, tt.wantIP)
			}
			if got := rt.ipAllowed(r); got != tt.wantAllowed {
				t.Errorf("ipAllowed = %v, want %v", got, tt.wantAllowed)
			}
		})
	}
}
