package appsync

import "testing"

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
