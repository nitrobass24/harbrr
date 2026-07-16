package http

import (
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
			name:       "bare password param redacted",
			raw:        "https://t.example/login?user=alice&password=" + secret,
			wantNoLeak: []string{secret},
			wantHas:    []string{"user=alice", "REDACTED"},
		},
		{
			name:       "bare pass param redacted",
			raw:        "https://t.example/login?pass=" + secret + "&ok=1",
			wantNoLeak: []string{secret},
			wantHas:    []string{"ok=1", "REDACTED"},
		},
		{
			name:       "api-key and x-api-key params redacted",
			raw:        "https://t.example/api?api-key=" + secret + "&x-api-key=" + secret,
			wantNoLeak: []string{secret},
			wantHas:    []string{"REDACTED"},
		},
		{
			// A passkey carried in a PATH segment (animebytes/beyondhd style) must be
			// scrubbed too — RedactURL was previously query-only.
			name:       "path-segment passkey redacted",
			raw:        "https://t.example/rss/deadbeefdeadbeefdeadbeefdeadbeef0badc0de/announce",
			wantNoLeak: []string{"deadbeefdeadbeefdeadbeefdeadbeef0badc0de"},
			wantHas:    []string{"t.example", "REDACTED", "announce"},
		},
		{
			// An unparseable URL with a path passkey (no query) must not leak the path token.
			name:       "unparseable url path secret redacted",
			raw:        "://bad\x7f/rss/deadbeefdeadbeefdeadbeefdeadbeef0badc0de/x",
			wantNoLeak: []string{"deadbeefdeadbeefdeadbeefdeadbeef0badc0de"},
		},
		{
			// A userinfo password in a URL url.Parse REJECTS must be scrubbed by the
			// fallback (redactUserinfo only runs on a successfully parsed URL).
			name:       "unparseable url userinfo password redacted",
			raw:        "https://alice:" + secret + "@bad\x7fhost/rss",
			wantNoLeak: []string{secret},
			wantHas:    []string{"alice", "REDACTED"},
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

func TestRedactURLIdentity(t *testing.T) {
	t.Parallel()

	const secret = "deadbeefcafe"
	const hexID = "abcdef0123456789abcdef0123456789" // a synthetic 32-hex release id
	tests := []struct {
		name       string
		raw        string
		wantNoLeak []string
		wantHas    []string
	}{
		{
			// The whole point: a details permalink's hex release id is an IDENTITY, not a
			// secret — it must survive so dedup-by-guid keeps distinct releases distinct.
			name:    "hex release id in path preserved",
			raw:     "https://dognzb.cr/details/" + hexID,
			wantHas: []string{"dognzb.cr/details/" + hexID},
		},
		{
			// A genuine secret in a query param is still scrubbed.
			name:       "secret query param still redacted, path id kept",
			raw:        "https://host/details/" + hexID + "?apikey=" + secret,
			wantNoLeak: []string{secret},
			wantHas:    []string{"details/" + hexID, "REDACTED"},
		},
		{
			name:       "userinfo password redacted, path id kept",
			raw:        "https://user:" + secret + "@host/details/" + hexID,
			wantNoLeak: []string{secret},
			wantHas:    []string{"user", "REDACTED", "details/" + hexID},
		},
		{
			name:    "non-secret query preserved",
			raw:     "https://host/d?id=42&cat=tv",
			wantHas: []string{"id=42", "cat=tv"},
		},
		{
			// Parse-failure fallback keeps the path (identity) and strips the query.
			name:       "unparseable url keeps path, strips query secret",
			raw:        "https://bad\x7fhost/details/" + hexID + "?passkey=" + secret,
			wantNoLeak: []string{secret},
			wantHas:    []string{hexID},
		},
		{name: "empty string", raw: "", wantHas: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RedactURLIdentity(tt.raw)
			for _, leak := range tt.wantNoLeak {
				if strings.Contains(got, leak) {
					t.Errorf("RedactURLIdentity(%q) = %q, leaked %q", tt.raw, got, leak)
				}
			}
			for _, has := range tt.wantHas {
				if !strings.Contains(got, has) {
					t.Errorf("RedactURLIdentity(%q) = %q, want substring %q", tt.raw, got, has)
				}
			}
		})
	}
}

// TestRedactURLIdentity_KeepsDistinct is the regression for the Newznab dedup collapse:
// two details permalinks that differ only in their hex release id must stay DISTINCT after
// redaction (RedactURL would collapse both to ".../REDACTED", making dedup drop one).
func TestRedactURLIdentity_KeepsDistinct(t *testing.T) {
	t.Parallel()
	a := RedactURLIdentity("https://dognzb.cr/details/0123456789abcdef0123456789abcdef")
	b := RedactURLIdentity("https://dognzb.cr/details/fedcba9876543210fedcba9876543210")
	if a == b {
		t.Fatalf("distinct release ids collapsed to the same guid: %q", a)
	}
}
