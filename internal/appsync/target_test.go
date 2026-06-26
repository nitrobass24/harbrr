package appsync

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// TestHashProtocolBackwardCompat guards that the torrent default does not change a
// DesiredIndexer's fingerprint (so pre-usenet PayloadHashes stay valid and torrent
// indexers don't spuriously re-sync), while usenet fingerprints distinctly.
func TestHashProtocolBackwardCompat(t *testing.T) {
	t.Parallel()
	base := DesiredIndexer{Name: "tt", FeedURL: "http://h/api/v2.0/indexers/tt/results/torznab", Enabled: true}

	empty := base
	torrent := base
	torrent.Protocol = "torrent"
	usenet := base
	usenet.Protocol = "usenet"

	if empty.hash() != torrent.hash() {
		t.Error(`Protocol "" and "torrent" must hash identically (backward compat)`)
	}
	if usenet.hash() == torrent.hash() {
		t.Error("usenet must hash differently from torrent")
	}
}

func TestSlugFromFeedURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"normal feed", "http://harbrr:8787/api/v2.0/indexers/show-tracker/results/torznab", "show-tracker"},
		{"with base path", "http://h/harbrr/api/v2.0/indexers/abc/results/torznab", "abc"},
		{"not a harbrr feed", "http://other/api/v3/indexer", ""},
		{"empty", "", ""},
		// The marker only in the query string must NOT be read as ownership — otherwise
		// a human-added indexer could be falsely tagged harbrr-managed and orphan-deleted.
		{"marker only in query", "http://app/torznab?ref=/api/v2.0/indexers/evil/results", ""},
		{"trailing slash, no slug", "http://harbrr/api/v2.0/indexers/", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := slugFromFeedURL(tc.url); got != tc.want {
				t.Errorf("slugFromFeedURL(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

// TestScrubURLError proves a *url.Error's URL (which can embed userinfo credentials —
// and which url.Parse errors carry verbatim, with no Go-side password stripping) never
// survives into the scrubbed error, while the operation and underlying cause remain.
func TestScrubURLError(t *testing.T) {
	t.Parallel()
	urlErr := &url.Error{
		Op:  "Get",
		URL: "http://admin:sup3rsecret@sonarr:8989/api/v3/indexer",
		Err: errors.New("dial tcp: connection refused"),
	}
	got := scrubURLError(urlErr).Error()
	if strings.Contains(got, "sup3rsecret") || strings.Contains(got, "admin") || strings.Contains(got, "sonarr:8989") {
		t.Errorf("scrubURLError leaked URL/credentials: %q", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Errorf("scrubURLError dropped the underlying cause: %q", got)
	}

	// A non-URL error passes through unchanged.
	plain := errors.New("boom")
	if got := scrubURLError(plain); !errors.Is(got, plain) {
		t.Errorf("scrubURLError altered a non-URL error: %v", got)
	}
}
