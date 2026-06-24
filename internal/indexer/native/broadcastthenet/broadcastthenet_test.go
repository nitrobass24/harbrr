package broadcastthenet

import (
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
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

// TestFamilies proves the catalog has the single BroadcastTheNet site, it builds without
// error (so mapper.Build accepts the Go-built caps), it carries the 5 s RequestDelay, it
// needs the /dl resolver, and it does not require out-of-band download auth.
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
	if f.Definition.ID != "broadcastthenet" {
		t.Errorf("id = %q, want broadcastthenet", f.Definition.ID)
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
		t.Error("NeedsResolver = false, want true (download embeds authkey/torrent_pass)")
	}
	if d.DownloadNeedsAuth() {
		t.Error("DownloadNeedsAuth = true, want false (already routed through /dl)")
	}
}

// TestSettingsSecrets proves apikey is classified as a secret (encrypted/redacted)
// because its name carries the "apikey" token.
func TestSettingsSecrets(t *testing.T) {
	t.Parallel()
	def := Families()[0].Definition
	if len(def.Settings) != 1 {
		t.Fatalf("settings = %d, want 1", len(def.Settings))
	}
	if !def.Settings[0].IsSecret() {
		t.Error("apikey should be a secret")
	}
}

// TestSiteCaps pins the search modes and the Resolution-keyed category map: the always-
// available basic q mode; the TV mode advertising q/season/ep/tvdbid/rid (no imdb); and
// the Resolution -> newznab mappings SD->TV/SD(5030), 1080p->TV/HD(5040),
// 2160p->TV/UHD(5045), Portable Device->TV/SD(5030). BTN is TV-only, so no
// movie/music modes are advertised.
func TestSiteCaps(t *testing.T) {
	t.Parallel()
	caps := buildDriver(t).Capabilities()

	if caps.Modes["search"] == nil {
		t.Error("missing the always-available search mode")
	}
	for _, p := range []string{"q", "season", "ep", "tvdbid", "rid"} {
		if !slices.Contains(caps.Modes["tv-search"], p) {
			t.Errorf("tv-search should advertise %q: %v", p, caps.Modes["tv-search"])
		}
	}
	if slices.Contains(caps.Modes["tv-search"], "imdbid") {
		t.Errorf("tv-search must NOT advertise imdbid (BTN has no imdb search): %v", caps.Modes["tv-search"])
	}
	if caps.Modes["movie-search"] != nil {
		t.Errorf("movie-search should not be advertised (BTN is TV-only): %v", caps.Modes["movie-search"])
	}

	cases := []struct {
		res  string
		want int
	}{
		{"SD", 5030},
		{"Portable Device", 5030},
		{"720p", 5040},
		{"1080p", 5040},
		{"1080i", 5040},
		{"2160p", 5045},
	}
	for _, c := range cases {
		if got := caps.CategoryMap.MapTrackerCatDescToNewznab(c.res); !slices.Contains(got, c.want) {
			t.Errorf("%q -> %v, want it to include %d", c.res, got, c.want)
		}
	}
}

// TestStubsCompile proves the Search/Grab/Test stubs satisfy native.Driver and report
// not-implemented (filled in later leaves) rather than panicking.
func TestStubsCompile(t *testing.T) {
	t.Parallel()
	d := buildDriver(t)
	if _, err := d.Search(t.Context(), search.Query{}); err == nil {
		t.Error("Search stub should return an error")
	}
	if _, err := d.Grab(t.Context(), "x"); err == nil {
		t.Error("Grab stub should return an error")
	}
	if err := d.Test(t.Context()); err == nil {
		t.Error("Test stub should return an error")
	}
}
