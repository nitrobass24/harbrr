package animebytes

import (
	"slices"
	"strings"
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

// TestSiteCaps pins the search modes and the caps category-description map (mirroring
// Prowlarr's SetCapabilities labels): the basic q mode is always present, and the AB
// category descriptions map to the expected newznab categories (TV Series -> TV/Anime
// 5070, Movie -> Movies 2000, Music -> Audio 3000, Manga -> Books 7000, Game -> both
// Console 1000 and PC/Games 4000).
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
		{"Music", 3000}, // Audio (single key for all music)
		{"Manga", 7000}, // Books
		{"Game", 1000},  // Console
		{"Game", 4050},  // PC/Games (game key registered for both)
	}
	for _, tc := range cases {
		if got := caps.CategoryMap.MapTrackerCatDescToNewznab(tc.desc); !slices.Contains(got, tc.want) {
			t.Errorf("%q -> %v, want it to include %d", tc.desc, got, tc.want)
		}
	}
}

// TestCapsToTrackersResolution is the request-side caps contract the category-filter bug
// slipped through: a requested Newznab category must resolve through MapTorznabCapsToTrackers
// to the LITERAL AnimeBytes scrape.php filter keys (e.g. 5070 -> anime[tv_series]/…), not a
// synthetic id, so the request builder emits a flag AnimeBytes actually honours.
func TestCapsToTrackersResolution(t *testing.T) {
	t.Parallel()
	caps := buildDriver(t).Capabilities()

	cases := []struct {
		name     string
		newznab  int
		wantKeys []string
	}{
		{"TV/Anime -> anime[*]", 5070, []string{"anime[tv_series]", "anime[tv_special]", "anime[ova]", "anime[ona]", "anime[dvd_special]", "anime[bd_special]"}},
		{"Movies -> anime[movie]", 2000, []string{"anime[movie]"}},
		{"Audio -> audio", 3000, []string{"audio"}},
		{"Console -> gamec[*]", 1000, []string{"gamec[game]", "gamec[visual_novel]"}},
		{"PC/Games -> gamec[*]", 4050, []string{"gamec[game]", "gamec[visual_novel]"}},
		{"Books -> printedtype[*]", 7000, []string{"printedtype[manga]", "printedtype[oneshot]", "printedtype[anthology]", "printedtype[manhwa]", "printedtype[light_novel]", "printedtype[artbook]"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := caps.MapTorznabCapsToTrackers([]int{tc.newznab})
			for _, key := range tc.wantKeys {
				if !slices.Contains(got, key) {
					t.Errorf("Newznab %d -> %v, want it to include %q", tc.newznab, got, key)
				}
			}
			// Guard against the original defect: the resolved keys must be the bracketed AB
			// form, never the old synthetic underscore ids (e.g. "anime_tv_series").
			for _, k := range got {
				if !strings.Contains(k, "[") && k != "audio" {
					t.Errorf("Newznab %d resolved to non-bracket key %q (want bracketed AB key or audio)", tc.newznab, k)
				}
			}
		})
	}
}
