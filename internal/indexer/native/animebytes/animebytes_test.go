package animebytes

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

// TestFamilies proves the catalog has the single AnimeBytes site, it builds without
// error (so mapper.Build accepts the Go-built caps), it carries the 4 s RequestDelay, it
// needs the /dl resolver (the download embeds the passkey), and it does not require
// out-of-band download auth.
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
	if f.Definition.ID != "animebytes" {
		t.Errorf("id = %q, want animebytes", f.Definition.ID)
	}
	if f.Definition.RequestDelay == nil || *f.Definition.RequestDelay != requestDelaySeconds {
		t.Errorf("RequestDelay = %v, want %v", f.Definition.RequestDelay, requestDelaySeconds)
	}
	if _, err := mapper.Build(f.Definition); err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}

	d := buildDriver(t)
	if !d.NeedsResolver() {
		t.Error("NeedsResolver = false, want true (download embeds the passkey)")
	}
	if d.DownloadNeedsAuth() {
		t.Error("DownloadNeedsAuth = true, want false (already routed through /dl)")
	}
	if d.Capabilities() == nil {
		t.Fatal("Capabilities = nil")
	}
	if d.Capabilities().Modes["search"] == nil {
		t.Error("missing the always-available search mode")
	}
}

// TestSettingsSecrets proves passkey is classified as a secret (encrypted/redacted)
// because its name carries the "passkey" token, while username is not.
func TestSettingsSecrets(t *testing.T) {
	t.Parallel()
	def := Families()[0].Definition
	got := map[string]bool{}
	for _, s := range def.Settings {
		got[s.Name] = s.IsSecret()
	}
	if !got["passkey"] {
		t.Error("passkey should be a secret")
	}
	if got["username"] {
		t.Error("username should NOT be a secret")
	}
}

// TestSiteCaps pins the search modes and the parse-side category mappings the parser
// relies on: the basic q mode is always present, and the AB GroupName descriptions map
// to the expected newznab categories (TV Series -> TV/Anime 5070, Movie -> Movies 2000,
// Album -> Audio 3000, Manga -> Books 7000).
func TestSiteCaps(t *testing.T) {
	t.Parallel()
	caps := buildDriver(t).Capabilities()

	if caps.Modes["search"] == nil {
		t.Fatal("missing search mode")
	}

	cases := []struct {
		desc string
		want int
	}{
		{"TV Series", 5070}, // TV/Anime
		{"OVA", 5070},
		{"Movie", 2000}, // Movies
		{"Album", 3000}, // Audio
		{"Manga", 7000}, // Books
		{"Game", 1000},  // Console
	}
	for _, tc := range cases {
		if got := caps.CategoryMap.MapTrackerCatDescToNewznab(tc.desc); !slices.Contains(got, tc.want) {
			t.Errorf("%q -> %v, want it to include %d", tc.desc, got, tc.want)
		}
	}
}
