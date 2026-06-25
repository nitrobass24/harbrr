package hdbits

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

// TestFamilies proves the catalog has the single HDBits site, it builds without error (so
// mapper.Build accepts the Go-built caps), it carries the RequestDelay, it needs the /dl
// resolver, and it does not require out-of-band download auth.
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
	if f.Definition.ID != "hdbits" {
		t.Errorf("id = %q, want hdbits", f.Definition.ID)
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
		t.Error("NeedsResolver = false, want true (download embeds the passkey)")
	}
	if d.DownloadNeedsAuth() {
		t.Error("DownloadNeedsAuth = true, want false (already routed through /dl)")
	}
}

// TestSettingsSecrets proves both username and passkey are classified as secrets
// (encrypted/redacted): both ride in the secret-bearing POST body, and their names carry
// the "username"/"passkey" tokens.
func TestSettingsSecrets(t *testing.T) {
	t.Parallel()
	def := Families()[0].Definition
	if len(def.Settings) != 2 {
		t.Fatalf("settings = %d, want 2", len(def.Settings))
	}
	got := map[string]bool{}
	for _, s := range def.Settings {
		got[s.Name] = s.IsSecret()
	}
	if !got["username"] {
		t.Error("username should be a secret (rides in the POST body)")
	}
	if !got["passkey"] {
		t.Error("passkey should be a secret")
	}
}

// TestSiteCaps pins the search modes and the integer-keyed category map: the always-
// available basic q mode; movie q+imdbid; tv q+season+ep+imdbid; and the type_category ->
// newznab mappings 1->Movies(2000), 2->TV(5000), 3->TV/Documentary(5080), 4->Audio(3000),
// 5->TV/Sport(5060), 6->Audio(3000), 7->XXX(6000), 8->Other(8000).
func TestSiteCaps(t *testing.T) {
	t.Parallel()
	caps := buildDriver(t).Capabilities()

	if caps.Modes["search"] == nil {
		t.Error("missing the always-available search mode")
	}
	if !slices.Contains(caps.Modes["movie-search"], "imdbid") {
		t.Errorf("movie-search should advertise imdbid: %v", caps.Modes["movie-search"])
	}
	for _, p := range []string{"q", "season", "ep", "imdbid"} {
		if !slices.Contains(caps.Modes["tv-search"], p) {
			t.Errorf("tv-search should advertise %q: %v", p, caps.Modes["tv-search"])
		}
	}

	cases := []struct {
		id   string
		want int
	}{
		{"1", 2000},
		{"2", 5000},
		{"3", 5080},
		{"4", 3000},
		{"5", 5060},
		{"6", 3000},
		{"7", 6000},
		{"8", 8000},
	}
	for _, c := range cases {
		if got := caps.CategoryMap.MapTrackerCatToNewznab(c.id); !slices.Contains(got, c.want) {
			t.Errorf("type_category %q -> %v, want it to include %d", c.id, got, c.want)
		}
	}
}

// TestCategoryParamMapping proves a Movies query resolves to tracker id "1" and a TV query
// to "2" — the forward map a later request builder uses for the category[] body field.
func TestCategoryParamMapping(t *testing.T) {
	t.Parallel()
	caps := buildDriver(t).Capabilities()
	if got := caps.MapTorznabCapsToTrackers([]int{2000}); !slices.Contains(got, "1") {
		t.Errorf("Movies -> %v, want tracker 1", got)
	}
	if got := caps.MapTorznabCapsToTrackers([]int{5000}); !slices.Contains(got, "2") {
		t.Errorf("TV -> %v, want tracker 2", got)
	}
}
