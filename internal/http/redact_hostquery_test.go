package http

import (
	"strings"
	"testing"
)

// TestHostAndRedactedQuery pins the trace-level request diagnostic: it keeps the
// benign search params (the whole point — keywords, categories, sort, paging),
// masks secret-named params, and NEVER emits the path (where a passkey/rsskey can
// hide in a segment that path redaction may miss).
func TestHostAndRedactedQuery(t *testing.T) {
	t.Parallel()

	const passkey = "deadbeefcafedeadbeefcafedeadbeef"

	tests := []struct {
		name       string
		raw        string
		wantHas    []string // substrings that MUST appear (benign, diagnostic)
		wantAbsent []string // substrings that must NOT appear (secrets, path)
	}{
		{
			name:    "unit3d search keeps benign params",
			raw:     "https://darkpeers.org/api/torrents/filter?name=Kitsune&perPage=100&sortField=created_at&sortDirection=desc",
			wantHas: []string{"https://darkpeers.org", "name=Kitsune", "perPage=100", "sortField=created_at", "sortDirection=desc"},
			// the path carries the endpoint, which we deliberately drop
			wantAbsent: []string{"/api/torrents/filter"},
		},
		{
			name:       "secret query param is masked, benign kept",
			raw:        "https://t.example/api?apikey=" + passkey + "&q=deadliest+catch&cat=5000",
			wantHas:    []string{"q=deadliest", "cat=5000", "REDACTED"},
			wantAbsent: []string{passkey},
		},
		{
			// "keywords" contains "key" and "author" contains "auth": the shared
			// secret matcher substring-hits both, so the allowlist must keep them.
			name:       "benign keywords/author survive despite substring collision",
			raw:        "https://t.example/api?keywords=deadliest+catch&author=melville&passkey=" + passkey,
			wantHas:    []string{"keywords=deadliest", "author=melville", "REDACTED"},
			wantAbsent: []string{passkey},
		},
		{
			name:       "userinfo credentials are never emitted",
			raw:        "https://user:" + passkey + "@t.example/api?q=foo",
			wantHas:    []string{"https://t.example", "q=foo"},
			wantAbsent: []string{passkey, "user:", "@t.example"},
		},
		{
			name:       "passkey in a PATH segment never appears (path dropped)",
			raw:        "https://t.example/" + passkey + "/announce?q=foo",
			wantHas:    []string{"https://t.example", "q=foo"},
			wantAbsent: []string{passkey, "/announce"},
		},
		{
			name:       "no query returns bare origin",
			raw:        "https://t.example/rss/" + passkey,
			wantHas:    []string{"https://t.example"},
			wantAbsent: []string{passkey, "?"},
		},
		{
			name:    "unparseable / hostless is fully redacted",
			raw:     "::not a url::",
			wantHas: []string{"REDACTED"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := HostAndRedactedQuery(tt.raw)
			for _, s := range tt.wantHas {
				if !strings.Contains(got, s) {
					t.Errorf("HostAndRedactedQuery(%q) = %q, want it to contain %q", tt.raw, got, s)
				}
			}
			for _, s := range tt.wantAbsent {
				if strings.Contains(got, s) {
					t.Errorf("HostAndRedactedQuery(%q) = %q, must NOT contain %q", tt.raw, got, s)
				}
			}
		})
	}
}
