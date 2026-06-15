package registry

import (
	stdhttp "net/http"
	"strings"
	"testing"
	"time"
)

func TestBuildTransport(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     map[string]string
		wantErr bool
		check   func(t *testing.T, tr *stdhttp.Transport)
	}{
		{
			name: "no proxy returns nil (stdlib default)",
			cfg:  map[string]string{},
			check: func(t *testing.T, tr *stdhttp.Transport) {
				if tr != nil {
					t.Errorf("want nil transport, got %v", tr)
				}
			},
		},
		{
			name: "http proxy sets Transport.Proxy",
			cfg:  map[string]string{"proxy_type": "http", "proxy_url": "http://proxy.test:8080"},
			check: func(t *testing.T, tr *stdhttp.Transport) {
				if tr == nil || tr.Proxy == nil {
					t.Fatal("want Transport.Proxy set")
				}
				req, _ := stdhttp.NewRequestWithContext(t.Context(), stdhttp.MethodGet, "https://tracker.test/", nil)
				u, err := tr.Proxy(req)
				if err != nil || u == nil || u.String() != "http://proxy.test:8080" {
					t.Errorf("Proxy(req) = %v, %v; want http://proxy.test:8080", u, err)
				}
			},
		},
		{
			name: "socks5 sets DialContext",
			cfg:  map[string]string{"proxy_type": "socks5", "proxy_url": "socks5://proxy.test:1080"},
			check: func(t *testing.T, tr *stdhttp.Transport) {
				if tr == nil || tr.DialContext == nil {
					t.Fatal("want Transport.DialContext set for socks5")
				}
				if tr.Proxy != nil {
					t.Error("socks5 must clear Transport.Proxy so an env HTTP proxy can't layer over it")
				}
			},
		},
		{
			name: "socks5 with userinfo builds without error",
			cfg:  map[string]string{"proxy_type": "socks5", "proxy_url": "socks5://user:pass@proxy.test:1080"},
			check: func(t *testing.T, tr *stdhttp.Transport) {
				if tr == nil || tr.DialContext == nil {
					t.Fatal("want Transport.DialContext set for authed socks5")
				}
			},
		},
		{
			name: "proxy_type is case-insensitive",
			cfg:  map[string]string{"proxy_type": "SOCKS5", "proxy_url": "socks5://proxy.test:1080"},
			check: func(t *testing.T, tr *stdhttp.Transport) {
				if tr == nil || tr.DialContext == nil {
					t.Fatal("uppercase SOCKS5 must build a socks5 transport")
				}
			},
		},
		{name: "proxy_type without url fails loud", cfg: map[string]string{"proxy_type": "http"}, wantErr: true},
		{name: "invalid proxy_url fails loud", cfg: map[string]string{"proxy_type": "http", "proxy_url": "://bad"}, wantErr: true},
		{name: "socks4 not supported fails loud", cfg: map[string]string{"proxy_type": "socks4", "proxy_url": "socks4://proxy.test:1080"}, wantErr: true},
		{name: "unknown proxy_type fails loud", cfg: map[string]string{"proxy_type": "ftp", "proxy_url": "ftp://proxy.test"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr, err := buildTransport(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got transport %v", tr)
				}
				// A fail-loud proxy error must never echo a proxy_url (it may carry creds).
				if pu := tt.cfg["proxy_url"]; pu != "" && strings.Contains(err.Error(), pu) {
					t.Errorf("error %q leaked proxy_url %q", err.Error(), pu)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildTransport: %v", err)
			}
			tt.check(t, tr)
		})
	}
}

// TestNewDoerWrapsProxyTransport pins the full production chain: newDoer ->
// buildTransport (proxy) -> base *http.Client -> newPacedDoer.
func TestNewDoerWrapsProxyTransport(t *testing.T) {
	t.Parallel()
	d, err := newDoer(ClientParams{
		Cfg:          map[string]string{"proxy_type": "http", "proxy_url": "http://proxy.test:8080"},
		Timeout:      30 * time.Second,
		RateInterval: time.Second,
	})
	if err != nil {
		t.Fatalf("newDoer: %v", err)
	}
	pd, ok := d.(*pacedDoer)
	if !ok {
		t.Fatalf("newDoer returned %T, want *pacedDoer", d)
	}
	base, ok := pd.base.(*stdhttp.Client)
	if !ok {
		t.Fatalf("paced base = %T, want *http.Client", pd.base)
	}
	if base.Transport == nil {
		t.Error("proxy transport not applied to base client")
	}
	if base.Timeout != 30*time.Second {
		t.Errorf("base timeout = %v, want 30s", base.Timeout)
	}
}

func TestClassifySecret_ReservedProxyURL(t *testing.T) {
	t.Parallel()
	if !classifySecret("proxy_url", nil) {
		t.Error("proxy_url must be classified secret (encrypted at rest)")
	}
	if classifySecret("proxy_type", nil) {
		t.Error("proxy_type is not a secret")
	}
}
