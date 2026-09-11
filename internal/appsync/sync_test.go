package appsync

import (
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
)

func TestAppAcceptsProtocol(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		kind     string
		protocol string
		want     bool
	}{
		{"qui rejects usenet", domain.AppKindQui, "usenet", false},
		{"qui accepts torrent", domain.AppKindQui, "torrent", true},
		{"qui accepts empty (torrent default)", domain.AppKindQui, "", true},
		{"sonarr accepts usenet", domain.AppKindSonarr, "usenet", true},
		{"radarr accepts torrent", domain.AppKindRadarr, "torrent", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := AppAcceptsProtocol(tc.kind, tc.protocol); got != tc.want {
				t.Fatalf("AppAcceptsProtocol(%q, %q) = %v, want %v", tc.kind, tc.protocol, got, tc.want)
			}
		})
	}
}
