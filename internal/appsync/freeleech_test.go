package appsync

import (
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
)

func TestFeedURL(t *testing.T) {
	t.Parallel()
	const base = "http://harbrr:8787"
	tests := []struct {
		name string
		mode string
		want string
	}{
		{"honor uses the standard feed", domain.FreeleechModeHonor, "http://harbrr:8787/api/indexers/tt/results/torznab"},
		{"bypass appends /full", domain.FreeleechModeBypass, "http://harbrr:8787/api/indexers/tt/results/torznab/full"},
		{"empty mode defaults to the standard feed", "", "http://harbrr:8787/api/indexers/tt/results/torznab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := FeedURL(base, "tt", tt.mode); got != tt.want {
				t.Errorf("feedURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultFreeleechModeByKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind string
		want string
	}{
		{domain.AppKindSonarr, domain.FreeleechModeHonor},
		{domain.AppKindRadarr, domain.FreeleechModeHonor},
		{domain.AppKindLidarr, domain.FreeleechModeHonor},
		{domain.AppKindReadarr, domain.FreeleechModeHonor},
		{domain.AppKindWhisparr, domain.FreeleechModeHonor},
		{domain.AppKindQui, domain.FreeleechModeBypass},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			t.Parallel()
			if got := defaultFreeleechMode(tt.kind); got != tt.want {
				t.Errorf("defaultFreeleechMode(%q) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

// TestWithDefaultsFreeleechMode proves the create path fills freeleech_mode by kind when
// the operator omits it, and leaves an explicit choice untouched.
func TestWithDefaultsFreeleechMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   CreateConnectionParams
		want string
	}{
		{"qui defaults to bypass", CreateConnectionParams{Kind: domain.AppKindQui}, domain.FreeleechModeBypass},
		{"sonarr defaults to honor", CreateConnectionParams{Kind: domain.AppKindSonarr}, domain.FreeleechModeHonor},
		{"explicit override is kept", CreateConnectionParams{Kind: domain.AppKindQui, FreeleechMode: domain.FreeleechModeHonor}, domain.FreeleechModeHonor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.in.withDefaults().FreeleechMode; got != tt.want {
				t.Errorf("withDefaults FreeleechMode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateFreeleechMode(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{domain.FreeleechModeHonor, domain.FreeleechModeBypass} {
		if err := validateFreeleechMode(mode); err != nil {
			t.Errorf("validateFreeleechMode(%q) = %v, want nil", mode, err)
		}
	}
	if err := validateFreeleechMode("nonsense"); err == nil {
		t.Error("validateFreeleechMode(nonsense) = nil, want error")
	}
}
