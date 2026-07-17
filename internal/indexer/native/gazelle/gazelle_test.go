package gazelle

import (
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// familyByID returns the family with the given definition id (Families ordering is not
// relied upon by the tests).
func familyByID(t *testing.T, id string) native.Family {
	t.Helper()
	for _, f := range Families() {
		if f.Definition != nil && f.Definition.ID == id {
			return f
		}
	}
	t.Fatalf("family %q not found", id)
	return native.Family{}
}

// buildDriver constructs the driver for one site from its family definition (no
// doer/creds needed to exercise the capabilities — Search/Grab are not called here).
func buildDriver(t *testing.T, id string) native.Driver {
	t.Helper()
	f := familyByID(t, id)
	d, err := f.Factory(native.Params{Def: f.Definition})
	if err != nil {
		t.Fatalf("factory %q: %v", id, err)
	}
	return d
}

// TestFamilies proves the catalog has both Gazelle sites with their expected ids,
// links, and per-site RequestDelay; that each builds without error (so mapper.Build
// accepts the Go-built caps); that the download is NOT routed via the /dl resolver
// (NeedsResolver false) but DOES need out-of-band header auth (DownloadNeedsAuth true).
func TestFamilies(t *testing.T) {
	t.Parallel()
	fams := Families()
	if len(fams) != 3 {
		t.Fatalf("families = %d, want 3", len(fams))
	}

	cases := []struct {
		id    string
		link  string
		delay float64
	}{
		{"redacted", "https://redacted.sh/", redactedDelaySeconds},
		{"orpheus", "https://orpheus.network/", orpheusDelaySeconds},
		{"alpharatio", "https://alpharatio.cc/", alphaRatioDelaySeconds},
	}
	for _, c := range cases {
		f := familyByID(t, c.id)
		if f.Factory == nil {
			t.Fatalf("%q: nil factory", c.id)
		}
		if len(f.Definition.Links) != 1 || f.Definition.Links[0] != c.link {
			t.Errorf("%q links = %v, want [%q]", c.id, f.Definition.Links, c.link)
		}
		if f.Definition.RequestDelay == nil || *f.Definition.RequestDelay != c.delay {
			t.Errorf("%q RequestDelay = %v, want %v", c.id, f.Definition.RequestDelay, c.delay)
		}
		if _, err := mapper.Build(f.Definition); err != nil {
			t.Fatalf("%q mapper.Build: %v", c.id, err)
		}

		d := buildDriver(t, c.id)
		if d.Capabilities() == nil {
			t.Fatalf("%q Capabilities() = nil", c.id)
		}
		if d.NeedsResolver() {
			t.Errorf("%q NeedsResolver = true, want false (download link carries no secret)", c.id)
		}
		if !d.DownloadNeedsAuth() {
			t.Errorf("%q DownloadNeedsAuth = false, want true (Authorization header added server-side)", c.id)
		}
	}
}

// TestSiteAuthStrategy pins each site's declared auth strategy (ADR 0003): RED/OPS use
// apiKeyAuth with the per-site Authorization prefix (RED bare "", OPS "token "),
// AlphaRatio uses formLoginAuth — composed data in siteConfigs, never an id branch.
func TestSiteAuthStrategy(t *testing.T) {
	t.Parallel()
	apiKeyCases := []struct {
		id   string
		want string
	}{
		{"redacted", ""},
		{"orpheus", "token "},
	}
	for _, c := range apiKeyCases {
		cfg, ok := siteConfigs[c.id]
		if !ok {
			t.Fatalf("no site config for %q", c.id)
		}
		strategy, ok := cfg.strategy.(apiKeyAuth)
		if !ok {
			t.Fatalf("%q: strategy = %T, want apiKeyAuth", c.id, cfg.strategy)
		}
		if strategy.prefix != c.want {
			t.Errorf("%q: apiKeyAuth.prefix = %q, want %q", c.id, strategy.prefix, c.want)
		}
	}
	if _, ok := siteConfigs["alpharatio"].strategy.(formLoginAuth); !ok {
		t.Errorf("alpharatio: strategy = %T, want formLoginAuth", siteConfigs["alpharatio"].strategy)
	}
}

// TestSettingsSecrets proves apikey is classified as a secret (encrypted/redacted)
// because its name carries the "apikey" token, while the use_freeleech_token checkbox
// is a plain toggle (never a secret).
func TestSettingsSecrets(t *testing.T) {
	t.Parallel()
	def := familyByID(t, "redacted").Definition
	if len(def.Settings) != 2 {
		t.Fatalf("settings = %d, want 2", len(def.Settings))
	}
	byName := map[string]bool{}
	for _, s := range def.Settings {
		byName[s.Name] = s.IsSecret()
	}
	if !byName["apikey"] {
		t.Error("apikey should be a secret")
	}
	if byName["use_freeleech_token"] {
		t.Error("use_freeleech_token must NOT be a secret (it is a checkbox toggle)")
	}
}

// TestSiteCaps pins the search modes (music q/artist/album/year — no label; basic q;
// book q; no movie/tv modes) and the numeric-id -> newznab category map identical for
// both sites: 1->Audio(3000), 2->PC(4000), 3->Books/EBook(7020), 4->Audio/Audiobook
// (3030), 5->Other(8000), 6->Other(8000), 7->Books/Comics(7030).
func TestSiteCaps(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"redacted", "orpheus"} {
		caps := buildDriver(t, id).Capabilities()

		if caps.Modes["search"] == nil {
			t.Errorf("%q: missing the always-available search mode", id)
		}
		for _, p := range []string{"q", "artist", "album", "year"} {
			if !slices.Contains(caps.Modes["music-search"], p) {
				t.Errorf("%q: music-search should advertise %q: %v", id, p, caps.Modes["music-search"])
			}
		}
		if slices.Contains(caps.Modes["music-search"], "label") {
			t.Errorf("%q: music-search must NOT advertise label (RED/OPS do not): %v", id, caps.Modes["music-search"])
		}
		if caps.Modes["book-search"] == nil {
			t.Errorf("%q: missing the book-search mode", id)
		}
		if caps.Modes["movie-search"] != nil {
			t.Errorf("%q: movie-search should not be advertised: %v", id, caps.Modes["movie-search"])
		}
		if caps.Modes["tv-search"] != nil {
			t.Errorf("%q: tv-search should not be advertised: %v", id, caps.Modes["tv-search"])
		}

		cases := []struct {
			id   string
			want int
		}{
			{"1", 3000},
			{"2", 4000},
			{"3", 7020},
			{"4", 3030},
			{"5", 8000},
			{"6", 8000},
			{"7", 7030},
		}
		for _, c := range cases {
			if got := caps.CategoryMap.MapTrackerCatToNewznab(c.id); !slices.Contains(got, c.want) {
				t.Errorf("%q: cat %q -> %v, want it to include %d", id, c.id, got, c.want)
			}
		}
	}
}
