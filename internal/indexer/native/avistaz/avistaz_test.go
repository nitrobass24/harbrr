package avistaz

import (
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// buildDriver constructs a driver from a family's definition (no doer/creds needed
// to exercise the capabilities — Search/Grab are not called here).
func buildDriver(t *testing.T, def, _ string) native.Driver {
	t.Helper()
	for _, f := range Families() {
		if f.Definition.ID == def {
			d, err := f.Factory(native.Params{Def: f.Definition})
			if err != nil {
				t.Fatalf("factory(%q): %v", def, err)
			}
			return d
		}
	}
	t.Fatalf("no family %q", def)
	return nil
}

// TestFamilies proves the catalog has the four sites, each builds without error
// (so mapper.Build accepts the Go-built caps), and each needs the /dl resolver.
func TestFamilies(t *testing.T) {
	t.Parallel()
	fams := Families()
	if len(fams) != 4 {
		t.Fatalf("families = %d, want 4", len(fams))
	}
	want := map[string]bool{"avistaz": false, "cinemaz": false, "privatehd": false, "exoticaz": false}
	for _, f := range fams {
		if f.Definition == nil || f.Factory == nil {
			t.Fatalf("family has nil definition/factory")
		}
		id := f.Definition.ID
		if _, ok := want[id]; !ok {
			t.Fatalf("unexpected family id %q", id)
		}
		want[id] = true
		if f.Definition.RequestDelay == nil || *f.Definition.RequestDelay != requestDelaySeconds {
			t.Errorf("%s: RequestDelay = %v, want %v", id, f.Definition.RequestDelay, requestDelaySeconds)
		}
		d, err := f.Factory(native.Params{Def: f.Definition})
		if err != nil {
			t.Fatalf("%s: factory: %v", id, err)
		}
		if !d.NeedsResolver() {
			t.Errorf("%s: NeedsResolver = false, want true (downloads need the Bearer header)", id)
		}
		if d.Capabilities().Modes["search"] == nil {
			t.Errorf("%s: missing the always-available search mode", id)
		}
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("missing family %q", id)
		}
	}
}

// TestSiteCaps pins the per-site capability differences: AvistaZ/PrivateHD advertise
// tmdbid (movie) + tvdbid (tv); CinemaZ advertises neither; ExoticaZ advertises only
// search with XXX categories (no movie/tv search).
func TestSiteCaps(t *testing.T) {
	t.Parallel()
	caps := buildDriver(t, "avistaz", "").Capabilities()
	if !slices.Contains(caps.Modes["movie-search"], "tmdbid") {
		t.Errorf("avistaz movie-search should advertise tmdbid: %v", caps.Modes["movie-search"])
	}
	if !slices.Contains(caps.Modes["tv-search"], "tvdbid") {
		t.Errorf("avistaz tv-search should advertise tvdbid: %v", caps.Modes["tv-search"])
	}

	cz := buildDriver(t, "cinemaz", "").Capabilities()
	if slices.Contains(cz.Modes["movie-search"], "tmdbid") {
		t.Errorf("cinemaz movie-search must NOT advertise tmdbid: %v", cz.Modes["movie-search"])
	}
	if slices.Contains(cz.Modes["tv-search"], "tvdbid") {
		t.Errorf("cinemaz tv-search must NOT advertise tvdbid: %v", cz.Modes["tv-search"])
	}
	if !slices.Contains(cz.Modes["tv-search"], "imdbid") {
		t.Errorf("cinemaz tv-search should still advertise imdbid: %v", cz.Modes["tv-search"])
	}

	xz := buildDriver(t, "exoticaz", "").Capabilities()
	if len(xz.Modes["movie-search"]) != 0 || len(xz.Modes["tv-search"]) != 0 {
		t.Errorf("exoticaz must not advertise movie/tv search: %v", xz.Modes)
	}
	// XXX tracker category 1 ("Video Clip") maps to XXX/x264 (6040).
	if got := xz.CategoryMap.MapTrackerCatToNewznab("1"); !slices.Contains(got, 6040) {
		t.Errorf("exoticaz cat 1 -> %v, want it to include 6040 (XXX/x264)", got)
	}
}

// TestMovieTVCategoryMapping proves the Movies/TV type-param reverse mapping works:
// a Movies query resolves to tracker type "1", TV to "2".
func TestMovieTVCategoryMapping(t *testing.T) {
	t.Parallel()
	caps := buildDriver(t, "avistaz", "").Capabilities()
	if got := caps.MapTorznabCapsToTrackers([]int{2040}); !slices.Contains(got, "1") { // Movies/HD
		t.Errorf("Movies/HD -> %v, want type 1", got)
	}
	if got := caps.MapTorznabCapsToTrackers([]int{5040}); !slices.Contains(got, "2") { // TV/HD
		t.Errorf("TV/HD -> %v, want type 2", got)
	}
}
