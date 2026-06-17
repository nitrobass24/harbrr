package iptorrents

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// TestFamilies proves the catalog has the single IPTorrents site, it builds without
// error (so mapper.Build accepts the Go-built caps), it needs the /dl resolver, its
// download authenticates out-of-band, and the credential settings classify correctly:
// `cookie` is a secret, `user_agent` is not.
func TestFamilies(t *testing.T) {
	t.Parallel()
	fams := Families()
	if len(fams) != 1 {
		t.Fatalf("families = %d, want 1", len(fams))
	}
	f := fams[0]
	if f.Definition == nil || f.Factory == nil {
		t.Fatal("family has nil definition/factory")
	}
	if f.Definition.ID != "iptorrents" {
		t.Errorf("id = %q, want iptorrents", f.Definition.ID)
	}
	if f.Definition.RequestDelay == nil || *f.Definition.RequestDelay != requestDelaySeconds {
		t.Errorf("RequestDelay = %v, want %v", f.Definition.RequestDelay, requestDelaySeconds)
	}

	d, err := f.Factory(native.Params{Def: f.Definition})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if !d.NeedsResolver() {
		t.Error("NeedsResolver = false, want true (downloads need the session cookie)")
	}
	if d.DownloadNeedsAuth() {
		t.Error("DownloadNeedsAuth = true, want false (already routed through /dl)")
	}
	if d.Capabilities().Modes["search"] == nil {
		t.Error("missing the always-available search mode")
	}

	secret := map[string]bool{"cookie": true, "user_agent": false, "freeleech_only": false}
	for _, s := range f.Definition.Settings {
		want, ok := secret[s.Name]
		if !ok {
			t.Errorf("unexpected setting %q", s.Name)
			continue
		}
		if s.IsSecret() != want {
			t.Errorf("setting %q IsSecret = %v, want %v", s.Name, s.IsSecret(), want)
		}
	}
}

// TestCaps pins the advertised search modes and a representative slice of the category
// map (movie, TV, XXX), proving the Prowlarr map was ported and resolves to newznab ids.
func TestCaps(t *testing.T) {
	t.Parallel()
	caps := iptCapabilities()
	for _, mode := range []string{"search", "movie-search", "tv-search", "music-search", "book-search"} {
		if caps.Modes[mode] == nil {
			t.Errorf("missing mode %q", mode)
		}
	}
	cases := []struct {
		trackerCat string
		wantNewz   int
	}{
		{"72", 2000}, // Movies
		{"73", 5000}, // TV
		{"5", 5040},  // TV/x264 -> TV/HD
		{"80", 3040}, // Music/Flac -> Audio/Lossless
		{"88", 6000}, // XXX
		{"84", 6060}, // XXX/Pics -> XXX/ImageSet
	}
	for _, tc := range cases {
		got := caps.CategoryMap.MapTrackerCatToNewznab(tc.trackerCat)
		if !containsInt(got, tc.wantNewz) {
			t.Errorf("cat %q -> %v, want it to include %d", tc.trackerCat, got, tc.wantNewz)
		}
	}
}

func containsInt(xs []int, want int) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
