package http

import (
	"strings"
	"testing"
)

func TestRedactJSONBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		body           string
		mustNotContain []string
		mustContain    []string
	}{
		{
			name:           "flaresolverr request",
			body:           `{"cmd":"request.get","url":"https://t.test/","postData":"user=a&pass=SECRETPW","userAgent":"Mozilla/5.0"}`,
			mustNotContain: []string{"SECRETPW", "Mozilla/5.0"},
			mustContain:    []string{"request.get", "https://t.test/"},
		},
		{
			name:           "flaresolverr solution with nested cookies",
			body:           `{"status":"ok","solution":{"userAgent":"UA-XYZ","cookies":[{"name":"cf_clearance","value":"CLEARANCE-TOKEN"}]}}`,
			mustNotContain: []string{"UA-XYZ", "CLEARANCE-TOKEN", "cf_clearance"},
			mustContain:    []string{"ok"},
		},
		{
			name:           "flaresolverr solution response html + headers redacted wholesale",
			body:           `{"status":"ok","solution":{"url":"https://t/","response":"<html>SECRET-IN-PAGE</html>","headers":{"Set-Cookie":"sid=SECRET-SID"}}}`,
			mustNotContain: []string{"SECRET-IN-PAGE", "SECRET-SID"},
			mustContain:    []string{"ok", "https://t/"},
		},
		{
			name:           "invalid json is replaced wholesale",
			body:           `not json at all passkey=LEAK`,
			mustNotContain: []string{"LEAK", "passkey"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := string(RedactJSONBody([]byte(tt.body)))
			for _, s := range tt.mustNotContain {
				if strings.Contains(got, s) {
					t.Errorf("scrubbed %q must NOT contain %q", got, s)
				}
			}
			for _, s := range tt.mustContain {
				if !strings.Contains(got, s) {
					t.Errorf("scrubbed %q must contain %q", got, s)
				}
			}
		})
	}
}

func TestRedactProxyURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		in             string
		mustNotContain []string
		mustContain    []string
	}{
		{
			"socks5 with user:pass",
			"socks5://proxyuser:proxypass@proxy.test:1080",
			[]string{"proxyuser", "proxypass"},
			[]string{"proxy.test:1080", "socks5"},
		},
		{
			"http proxy with user only",
			"http://accountid@proxy.test:8080",
			[]string{"accountid"},
			[]string{"proxy.test:8080"},
		},
		{
			"no userinfo is unchanged host",
			"socks5://proxy.test:1080",
			nil,
			[]string{"proxy.test:1080"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RedactProxyURL(tt.in)
			for _, s := range tt.mustNotContain {
				if strings.Contains(got, s) {
					t.Errorf("redacted %q must NOT contain %q", got, s)
				}
			}
			for _, s := range tt.mustContain {
				if !strings.Contains(got, s) {
					t.Errorf("redacted %q must contain %q", got, s)
				}
			}
		})
	}
}
