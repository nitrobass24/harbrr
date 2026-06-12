package http

import (
	"net/http"
	"strings"
	"testing"
)

func TestRedactURL(t *testing.T) {
	t.Parallel()

	const secret = "deadbeefcafe"
	tests := []struct {
		name       string
		raw        string
		wantNoLeak []string // substrings that must NOT appear in the output
		wantHas    []string // substrings that MUST appear in the output
	}{
		{
			name:       "passkey in query redacted",
			raw:        "https://tracker.example/rss?passkey=" + secret + "&cat=5",
			wantNoLeak: []string{secret},
			wantHas:    []string{"tracker.example", "cat=5", "REDACTED"},
		},
		{
			name:       "torrent_pass redacted",
			raw:        "https://t.example/dl.php?id=42&torrent_pass=" + secret,
			wantNoLeak: []string{secret},
			wantHas:    []string{"id=42", "REDACTED"},
		},
		{
			name:       "apikey and api_key redacted",
			raw:        "https://t.example/api?apikey=" + secret + "&api_key=" + secret,
			wantNoLeak: []string{secret},
			wantHas:    []string{"REDACTED"},
		},
		{
			name:       "rsskey redacted",
			raw:        "https://t.example/feed?rsskey=" + secret,
			wantNoLeak: []string{secret},
		},
		{
			name:       "authkey and token redacted",
			raw:        "https://t.example/x?authkey=" + secret + "&token=" + secret + "&q=ok",
			wantNoLeak: []string{secret},
			wantHas:    []string{"q=ok"},
		},
		{
			name:       "non-secret params preserved",
			raw:        "https://t.example/search?q=ubuntu&cat=movies&page=2",
			wantNoLeak: []string{"REDACTED"},
			wantHas:    []string{"q=ubuntu", "cat=movies", "page=2"},
		},
		{
			name:    "no query string untouched",
			raw:     "https://t.example/login.php",
			wantHas: []string{"https://t.example/login.php"},
		},
		{
			name:       "unparseable input does not leak query",
			raw:        "://bad url\x7f?passkey=" + secret,
			wantNoLeak: []string{secret},
		},
		{
			name:       "userinfo password redacted, username preserved",
			raw:        "https://alice:" + secret + "@t.example/login",
			wantNoLeak: []string{secret},
			wantHas:    []string{"alice", "REDACTED", "t.example/login"},
		},
		{
			name:       "userinfo password redacted alongside query secret",
			raw:        "https://bob:" + secret + "@t.example/rss?passkey=" + secret + "&q=ok",
			wantNoLeak: []string{secret},
			wantHas:    []string{"bob", "q=ok", "REDACTED"},
		},
		{
			name:    "empty string",
			raw:     "",
			wantHas: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RedactURL(tt.raw)
			for _, leak := range tt.wantNoLeak {
				if strings.Contains(got, leak) {
					t.Errorf("RedactURL(%q) = %q, leaked %q", tt.raw, got, leak)
				}
			}
			for _, has := range tt.wantHas {
				if !strings.Contains(got, has) {
					t.Errorf("RedactURL(%q) = %q, want substring %q", tt.raw, got, has)
				}
			}
		})
	}
}

func TestRedactHeader(t *testing.T) {
	t.Parallel()

	in := http.Header{
		"Authorization": {"Bearer sk-secret-token"},
		"Cookie":        {"session=abc123; uid=42"},
		"Set-Cookie":    {"session=xyz; HttpOnly"},
		"Content-Type":  {"text/html"},
		"User-Agent":    {"seekbrr/1.0"},
	}
	out := RedactHeader(in)

	// Sensitive headers redacted.
	for _, name := range []string{"Authorization", "Cookie", "Set-Cookie"} {
		if got := out.Get(name); got != "REDACTED" {
			t.Errorf("header %s = %q, want REDACTED", name, got)
		}
	}
	// Non-sensitive headers preserved.
	if got := out.Get("Content-Type"); got != "text/html" {
		t.Errorf("Content-Type = %q, want text/html", got)
	}
	if got := out.Get("User-Agent"); got != "seekbrr/1.0" {
		t.Errorf("User-Agent = %q, want seekbrr/1.0", got)
	}

	// Input must not be mutated.
	if got := in.Get("Authorization"); got != "Bearer sk-secret-token" {
		t.Errorf("input mutated: Authorization = %q", got)
	}
	if got := in.Get("Cookie"); got != "session=abc123; uid=42" {
		t.Errorf("input mutated: Cookie = %q", got)
	}

	// No secret content survives anywhere in the redacted header.
	for _, vals := range out {
		for _, v := range vals {
			for _, leak := range []string{"sk-secret-token", "abc123", "xyz"} {
				if strings.Contains(v, leak) {
					t.Fatalf("redacted header leaked %q in %q", leak, v)
				}
			}
		}
	}

	if RedactHeader(nil) != nil {
		t.Error("RedactHeader(nil) should return nil")
	}
}
