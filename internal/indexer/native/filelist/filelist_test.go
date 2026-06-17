package filelist

import (
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// buildDriver constructs the driver from the family definition (no doer/creds needed
// to exercise the capabilities — Search/Grab are not called here).
func buildDriver(t *testing.T) native.Driver {
	t.Helper()
	fams := Families()
	d, err := fams[0].Factory(native.Params{Def: fams[0].Definition})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	return d
}

// TestFamilies proves the catalog has the single FileList site, it builds without
// error (so mapper.Build accepts the Go-built caps), it carries the 24 s RequestDelay,
// it needs the /dl resolver, and it does not require out-of-band download auth.
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
	if f.Definition.ID != "filelist" {
		t.Errorf("id = %q, want filelist", f.Definition.ID)
	}
	if f.Definition.RequestDelay == nil || *f.Definition.RequestDelay != requestDelaySeconds {
		t.Errorf("RequestDelay = %v, want %v", f.Definition.RequestDelay, requestDelaySeconds)
	}
	if _, err := mapper.Build(f.Definition); err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}

	d := buildDriver(t)
	if !d.NeedsResolver() {
		t.Error("NeedsResolver = false, want true (download carries the passkey)")
	}
	if d.DownloadNeedsAuth() {
		t.Error("DownloadNeedsAuth = true, want false (already routed through /dl)")
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

// TestSiteCaps pins the search modes and a couple of category mappings: the basic q
// mode, movie q+imdbid, tv q+season+ep+imdbid, and the Movies/HD ("Filme HD") + TV/HD
// ("Seriale HD") mappings (the description-keyed reverse map the parser uses).
func TestSiteCaps(t *testing.T) {
	t.Parallel()
	caps := buildDriver(t).Capabilities()

	if !slices.Contains(caps.Modes["movie-search"], "imdbid") {
		t.Errorf("movie-search should advertise imdbid: %v", caps.Modes["movie-search"])
	}
	for _, p := range []string{"season", "ep", "imdbid"} {
		if !slices.Contains(caps.Modes["tv-search"], p) {
			t.Errorf("tv-search should advertise %q: %v", p, caps.Modes["tv-search"])
		}
	}

	// "Filme HD" -> Movies/HD (2040) + the synthesised custom 1:1 (100004).
	if got := caps.CategoryMap.MapTrackerCatDescToNewznab("Filme HD"); !slices.Contains(got, 2040) {
		t.Errorf("Filme HD -> %v, want it to include 2040 (Movies/HD)", got)
	}
	// "Seriale HD" -> TV/HD (5040).
	if got := caps.CategoryMap.MapTrackerCatDescToNewznab("Seriale HD"); !slices.Contains(got, 5040) {
		t.Errorf("Seriale HD -> %v, want it to include 5040 (TV/HD)", got)
	}
}

// TestCategoryParamMapping proves a Movies/HD query resolves to tracker id "4" and a
// TV/HD query to "21" — the forward map the request builder uses for category=.
func TestCategoryParamMapping(t *testing.T) {
	t.Parallel()
	caps := buildDriver(t).Capabilities()
	if got := caps.MapTorznabCapsToTrackers([]int{2040}); !slices.Contains(got, "4") { // Movies/HD
		t.Errorf("Movies/HD -> %v, want tracker 4", got)
	}
	if got := caps.MapTorznabCapsToTrackers([]int{5040}); !slices.Contains(got, "21") { // TV/HD
		t.Errorf("TV/HD -> %v, want tracker 21", got)
	}
}
