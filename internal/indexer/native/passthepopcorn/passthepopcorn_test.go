package passthepopcorn

import (
	"slices"
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

// TestFamilies proves the catalog has the single PassThePopcorn site, it builds without
// error (so mapper.Build accepts the Go-built caps), it carries the 4 s RequestDelay, and
// it routes downloads through /dl via out-of-band header auth (not the resolver): the
// download URL carries no secret, so NeedsResolver is false and DownloadNeedsAuth is true.
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
	if f.Definition.ID != "passthepopcorn" {
		t.Errorf("id = %q, want passthepopcorn", f.Definition.ID)
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
	if d.NeedsResolver() {
		t.Error("NeedsResolver = true, want false (download URL carries no secret)")
	}
	if !d.DownloadNeedsAuth() {
		t.Error("DownloadNeedsAuth = false, want true (download re-attaches ApiUser/ApiKey headers)")
	}
}

// TestSettingsSecrets proves both credential fields are classified as secrets
// (encrypted/redacted): apikey by its name token, apiuser by its password type (Prowlarr
// marks both PrivacyLevel.UserName / PrivacyLevel.ApiKey — neither may be logged).
func TestSettingsSecrets(t *testing.T) {
	t.Parallel()
	def := Families()[0].Definition
	if len(def.Settings) != 2 {
		t.Fatalf("settings = %d, want 2", len(def.Settings))
	}
	for _, s := range def.Settings {
		if !s.IsSecret() {
			t.Errorf("setting %q should be a secret", s.Name)
		}
	}
}

// TestSiteCaps pins the movie-only search modes and the CategoryId-keyed category map:
// the always-available basic q mode; the movie mode advertising q + imdbid (no separate
// imdb param — both flow into searchstr); and the six PTP CategoryId values (1-6) all
// mapping to Movies (2000), matching Prowlarr's PassThePopcorn.SetCapabilities. PTP is
// movie-only, so no tv/music/book modes are advertised.
func TestSiteCaps(t *testing.T) {
	t.Parallel()
	caps := buildDriver(t).Capabilities()

	if caps.Modes["search"] == nil {
		t.Error("missing the always-available search mode")
	}
	for _, p := range []string{"q", "imdbid"} {
		if !slices.Contains(caps.Modes["movie-search"], p) {
			t.Errorf("movie-search should advertise %q: %v", p, caps.Modes["movie-search"])
		}
	}
	if caps.Modes["tv-search"] != nil {
		t.Errorf("tv-search should not be advertised (PTP is movie-only): %v", caps.Modes["tv-search"])
	}
	if caps.Modes["music-search"] != nil {
		t.Errorf("music-search should not be advertised (PTP is movie-only): %v", caps.Modes["music-search"])
	}

	for _, id := range []string{"1", "2", "3", "4", "5", "6"} {
		got := caps.CategoryMap.MapTrackerCatToNewznab(id)
		if !slices.Contains(got, 2000) {
			t.Errorf("CategoryId %q -> %v, want it to include 2000 (Movies)", id, got)
		}
	}
}
