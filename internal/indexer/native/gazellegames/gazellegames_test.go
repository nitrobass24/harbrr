package gazellegames

import (
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// buildDriver constructs the GazelleGames driver from its family definition (no
// doer/creds needed to exercise the capabilities — Search/Grab are not called here).
func buildDriver(t *testing.T) native.Driver {
	t.Helper()
	fams := Families()
	if len(fams) != 1 {
		t.Fatalf("families = %d, want 1", len(fams))
	}
	f := fams[0]
	if f.Factory == nil {
		t.Fatalf("nil factory")
	}
	d, err := f.Factory(native.Params{Def: f.Definition})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	return d
}

// TestFamilies proves the catalog has the GazelleGames site with its expected id, link,
// and RequestDelay; that it builds without error (so mapper.Build accepts the Go-built
// caps); that the download IS routed via the /dl resolver (NeedsResolver true, because
// the URL carries the passkey) and so does NOT additionally need out-of-band header auth
// (DownloadNeedsAuth false).
func TestFamilies(t *testing.T) {
	t.Parallel()
	fams := Families()
	if len(fams) != 1 {
		t.Fatalf("families = %d, want 1", len(fams))
	}
	f := fams[0]
	if f.Definition.ID != "gazellegames" {
		t.Errorf("id = %q, want %q", f.Definition.ID, "gazellegames")
	}
	if len(f.Definition.Links) != 1 || f.Definition.Links[0] != "https://gazellegames.net/" {
		t.Errorf("links = %v, want [https://gazellegames.net/]", f.Definition.Links)
	}
	if f.Definition.RequestDelay == nil || *f.Definition.RequestDelay != requestDelaySeconds {
		t.Errorf("RequestDelay = %v, want %v", f.Definition.RequestDelay, requestDelaySeconds)
	}
	if _, err := mapper.Build(f.Definition); err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}

	d := buildDriver(t)
	if d.Capabilities() == nil {
		t.Fatalf("Capabilities() = nil")
	}
	if !d.NeedsResolver() {
		t.Errorf("NeedsResolver = false, want true (download URL carries the passkey)")
	}
	if d.DownloadNeedsAuth() {
		t.Errorf("DownloadNeedsAuth = true, want false (already routed via /dl by NeedsResolver)")
	}
}

// TestSettingsSecrets proves apikey is classified as a secret (encrypted/redacted)
// because its name carries the "apikey" token, and that it is the only user-entered
// setting (the download passkey is fetched server-side, not user-entered).
func TestSettingsSecrets(t *testing.T) {
	t.Parallel()
	def := Families()[0].Definition
	if len(def.Settings) != 1 {
		t.Fatalf("settings = %d, want 1", len(def.Settings))
	}
	if def.Settings[0].Name != "apikey" {
		t.Fatalf("setting[0] = %q, want %q", def.Settings[0].Name, "apikey")
	}
	if !def.Settings[0].IsSecret() {
		t.Error("apikey should be a secret")
	}
}

// TestSiteCaps pins the basic-only search mode (q; no movie/tv/music/book modes) and the
// numeric group-categoryId -> newznab category map ported from Prowlarr: 1->PC/Games
// (4050), 2->PC/0day(4010), 3->Books/EBook(7020), 4->Audio/Other(3050).
func TestSiteCaps(t *testing.T) {
	t.Parallel()
	caps := buildDriver(t).Capabilities()

	if caps.Modes["search"] == nil {
		t.Error("missing the always-available search mode")
	}
	for _, m := range []string{"movie-search", "tv-search", "music-search", "book-search"} {
		if caps.Modes[m] != nil {
			t.Errorf("%s should not be advertised (GGn is text-search only): %v", m, caps.Modes[m])
		}
	}

	cases := []struct {
		id   string
		want int
	}{
		{"1", 4050},
		{"2", 4010},
		{"3", 7020},
		{"4", 3050},
	}
	for _, c := range cases {
		if got := caps.CategoryMap.MapTrackerCatToNewznab(c.id); !slices.Contains(got, c.want) {
			t.Errorf("cat %q -> %v, want it to include %d", c.id, got, c.want)
		}
	}
}
