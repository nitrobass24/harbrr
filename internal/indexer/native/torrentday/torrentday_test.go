package torrentday

import (
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// buildDriver constructs the driver from the family definition (no doer/creds needed to
// exercise the capabilities — Search/Grab are not called here).
func buildDriver(t *testing.T) native.Driver {
	t.Helper()
	fams := Families()
	d, err := fams[0].Factory(native.Params{Def: fams[0].Definition})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	return d
}

// TestFamilies proves the catalog has the single TorrentDay site, it builds without
// error (so mapper.Build accepts the Go-built caps), it carries the configured
// RequestDelay, it needs the /dl resolver, its download does not require out-of-band
// auth (already routed through /dl), and the credential settings classify correctly:
// `cookie` is a secret, `freeleech_only` is not.
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
	if f.Definition.ID != "torrentday" {
		t.Errorf("id = %q, want torrentday", f.Definition.ID)
	}
	if f.Definition.RequestDelay == nil || *f.Definition.RequestDelay != requestDelaySeconds {
		t.Errorf("RequestDelay = %v, want %v", f.Definition.RequestDelay, requestDelaySeconds)
	}
	if _, err := mapper.Build(f.Definition); err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}

	d := buildDriver(t)
	if d.Capabilities() == nil {
		t.Fatal("Capabilities() = nil")
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

	secret := map[string]bool{"cookie": true, "freeleech_only": false}
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
// map (movie, TV, music, XXX), proving the Prowlarr map was ported and resolves to
// newznab ids.
func TestCaps(t *testing.T) {
	t.Parallel()
	caps := buildDriver(t).Capabilities()
	for _, mode := range []string{"search", "movie-search", "tv-search", "music-search", "book-search"} {
		if caps.Modes[mode] == nil {
			t.Errorf("missing mode %q", mode)
		}
	}
	cases := []struct {
		trackerCat string
		wantNewz   int
	}{
		{"96", 2045},  // Movie/4K -> Movies/UHD
		{"11", 2050},  // Movies/Bluray -> Movies/BluRay
		{"7", 5040},   // TV/x264 -> TV/HD
		{"104", 5045}, // TV/4K -> TV/UHD
		{"27", 3000},  // Music/Flac -> Audio
		{"29", 5070},  // Anime -> TV/Anime
		{"15", 6050},  // XXX/Packs -> XXX/Pack
		{"6", 6000},   // XXX/Movies -> XXX
	}
	for _, tc := range cases {
		got := caps.CategoryMap.MapTrackerCatToNewznab(tc.trackerCat)
		if !containsInt(got, tc.wantNewz) {
			t.Errorf("cat %q -> %v, want it to include %d", tc.trackerCat, got, tc.wantNewz)
		}
	}
}

// TestCategoryCount proves the full Prowlarr category map was ported. Prowlarr's
// TorrentDay.cs ships 48 AddCategoryMapping entries (the Phase-1 contract said 47; the
// authoritative source has 48 — the off-by-one is the contract's, not the port's).
func TestCategoryCount(t *testing.T) {
	t.Parallel()
	if n := len(tdCategoryMappings()); n != 48 {
		t.Errorf("category mappings = %d, want 48", n)
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
