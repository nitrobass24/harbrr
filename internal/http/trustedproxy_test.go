package http

import (
	"crypto/tls"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
)

func TestParseTrustedProxies(t *testing.T) {
	t.Parallel()

	trusted, err := ParseTrustedProxies([]string{"192.0.2.1", "10.0.0.0/8"})
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	tests := []struct {
		ip   string
		want bool
	}{
		{"192.0.2.1", true},
		{"192.0.2.2", false},
		{"10.1.2.3", true},
		{"172.16.0.1", false},
	}
	for _, tt := range tests {
		if got := trusted(net.ParseIP(tt.ip)); got != tt.want {
			t.Errorf("trusted(%s) = %v, want %v", tt.ip, got, tt.want)
		}
	}

	if _, err := ParseTrustedProxies([]string{"not-an-ip"}); err == nil {
		t.Error("expected an error for an unparseable entry")
	}
}

func TestRequestScheme(t *testing.T) {
	t.Parallel()

	trustAll := TrustedProxies(func(net.IP) bool { return true })

	tests := []struct {
		name    string
		tls     bool
		fwd     string
		trusted TrustedProxies
		want    string
	}{
		{"plain http, no header", false, "", nil, "http"},
		{"tls always wins", true, "", nil, "https"},
		{"forwarded https, nil trusted (fails closed)", false, "https", nil, "http"},
		{"forwarded https, untrusted peer ignored", false, "https", TrustedProxies(func(net.IP) bool { return false }), "http"},
		{"forwarded https, trusted peer honored", false, "https", trustAll, "https"},
		{"forwarded http is never upgraded", false, "http", trustAll, "http"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(t.Context(), stdhttp.MethodGet, "http://h.test/", nil)
			r.RemoteAddr = "192.0.2.1:1234" // httptest.NewRequest's default peer
			if tt.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if tt.fwd != "" {
				r.Header.Set("X-Forwarded-Proto", tt.fwd)
			}
			if got := RequestScheme(r, tt.trusted); got != tt.want {
				t.Errorf("RequestScheme() = %q, want %q", got, tt.want)
			}
		})
	}
}
