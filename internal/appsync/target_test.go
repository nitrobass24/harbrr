package appsync

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
)

// TestHashProtocolBackwardCompat guards that the torrent default does not change a
// DesiredIndexer's fingerprint (so pre-usenet PayloadHashes stay valid and torrent
// indexers don't spuriously re-sync), while usenet fingerprints distinctly.
func TestHashProtocolBackwardCompat(t *testing.T) {
	t.Parallel()
	base := DesiredIndexer{Name: "tt", FeedURL: "http://h/api/indexers/tt/results/torznab", Enabled: true}

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

// TestHashProfileFieldsBackwardCompat is the upgrade-safety proof: a profile-less
// indexer (toggles resolved equal to Enabled, MinSeeders 0 — what buildDesired produces
// for a nil profile) must fingerprint EXACTLY as it did before sync profiles existed, so
// upgrading harbrr does not re-hash and re-push every managed indexer. We reconstruct the
// pre-sync-profile fingerprint by hand and assert the current hash() reproduces it.
func TestHashProfileFieldsBackwardCompat(t *testing.T) {
	t.Parallel()
	d := DesiredIndexer{
		Name: "trk", FeedURL: "http://h/api/indexers/trk/results/torznab",
		Categories: []Category{{5000, "TV"}, {2000, "Movies"}}, Priority: 25, Enabled: true,
		EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true, MinSeeders: 0,
	}
	cats := d.CategoryIDs()
	sort.Ints(cats)
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%v\x00%v\x00%d\x00%t", d.Name, d.FeedURL, cats, []string{}, d.Priority, d.Enabled)
	want := hex.EncodeToString(h.Sum(nil))
	if got := d.hash(); got != want {
		t.Errorf("profile-less hash diverged from the pre-sync-profile fingerprint:\n got %s\nwant %s", got, want)
	}
}

// TestHashProfileDivergence proves the fingerprint changes when a profile actually alters
// the pushed intent — a search-mode toggle diverging from Enabled, or a min-seeders floor.
func TestHashProfileDivergence(t *testing.T) {
	t.Parallel()
	base := DesiredIndexer{
		Name: "trk", FeedURL: "http://h/api/indexers/trk/results/torznab", Priority: 25, Enabled: true,
		EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
	}
	cases := map[string]func(*DesiredIndexer){
		"rss diverges":         func(d *DesiredIndexer) { d.EnableRss = false },
		"auto diverges":        func(d *DesiredIndexer) { d.EnableAutomaticSearch = false },
		"interactive diverges": func(d *DesiredIndexer) { d.EnableInteractiveSearch = false },
		"min seeders set":      func(d *DesiredIndexer) { d.MinSeeders = 3 },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			changed := base
			mut(&changed)
			if changed.hash() == base.hash() {
				t.Errorf("%s must change the hash", name)
			}
		})
	}
}

func TestSlugFromFeedURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"normal feed", "http://harbrr:8787/api/indexers/show-tracker/results/torznab", "show-tracker"},
		// the freeleech-bypass variant's trailing /full must not break slug recovery, so
		// orphan-detection still matches harbrr-managed rows pushed in bypass mode.
		{"bypass /full variant", "http://harbrr:8787/api/indexers/show-tracker/results/torznab/full", "show-tracker"},
		{"with base path", "http://h/harbrr/api/indexers/abc/results/torznab", "abc"},
		{"not a harbrr feed", "http://other/api/v3/indexer", ""},
		{"empty", "", ""},
		// The marker only in the query string must NOT be read as ownership — otherwise
		// a human-added indexer could be falsely tagged harbrr-managed and orphan-deleted.
		{"marker only in query", "http://app/torznab?ref=/api/indexers/evil/results", ""},
		{"trailing slash, no slug", "http://harbrr/api/indexers/", ""},
		// A management URL shares the /api/indexers/{slug} prefix but is NOT a feed — the
		// required /results/torznab suffix keeps it from being read as harbrr-managed.
		{"management search URL", "http://harbrr/api/indexers/show-tracker/search", ""},
		{"management get URL", "http://harbrr/api/indexers/show-tracker", ""},
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
