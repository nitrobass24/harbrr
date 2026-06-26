package newznab

import (
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// TestFamily proves the generic family builds (mapper.Build accepts the placeholder caps),
// is a usenet protocol, carries the RequestDelay, does NOT need the resolver, and DOES need
// out-of-band download auth (the apikey-bearing download is proxied through /dl).
func TestFamily(t *testing.T) {
	t.Parallel()
	f := Family()
	if f.Definition == nil || f.Factory == nil {
		t.Fatal("family has nil definition/factory")
	}
	if f.Definition.ID != "newznab" {
		t.Errorf("id = %q, want newznab", f.Definition.ID)
	}
	if f.Definition.EffectiveProtocol() != loader.ProtocolUsenet {
		t.Errorf("protocol = %q, want usenet", f.Definition.EffectiveProtocol())
	}
	if f.Definition.RequestDelay == nil || *f.Definition.RequestDelay != requestDelaySeconds {
		t.Errorf("RequestDelay = %v, want %v", f.Definition.RequestDelay, requestDelaySeconds)
	}
	if _, err := mapper.Build(f.Definition); err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}

	d, err := f.Factory(native.Params{Def: f.Definition})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if d.Capabilities() == nil {
		t.Fatal("Capabilities() = nil")
	}
	if d.NeedsResolver() {
		t.Error("NeedsResolver = true, want false (no magnet/resolve step)")
	}
	if !d.DownloadNeedsAuth() {
		t.Error("DownloadNeedsAuth = false, want true (apikey-bearing download proxied via /dl)")
	}
}

// TestFamilies proves Families() returns the generic Newznab driver plus every preset, that
// each is a distinct, usenet-protocol family whose Factory builds a working driver, and that
// every preset carries a default base-URL link the generic family deliberately omits.
func TestFamilies(t *testing.T) {
	t.Parallel()
	fams := Families()

	// Generic + the full preset table.
	if want := len(presets) + 1; len(fams) != want {
		t.Fatalf("Families() = %d families, want %d (generic + %d presets)", len(fams), want, len(presets))
	}

	seen := map[string]bool{}
	var generic int
	for _, f := range fams {
		if f.Definition == nil || f.Factory == nil {
			t.Fatalf("family %q has nil definition/factory", famID(f))
		}
		id := f.Definition.ID
		if seen[id] {
			t.Errorf("duplicate family id %q", id)
		}
		seen[id] = true

		if f.Definition.EffectiveProtocol() != loader.ProtocolUsenet {
			t.Errorf("family %q protocol = %q, want usenet", id, f.Definition.EffectiveProtocol())
		}
		if _, err := mapper.Build(f.Definition); err != nil {
			t.Errorf("mapper.Build(%q): %v", id, err)
		}
		d, err := f.Factory(native.Params{Def: f.Definition})
		if err != nil {
			t.Errorf("factory(%q): %v", id, err)
			continue
		}
		if d.Capabilities() == nil {
			t.Errorf("family %q Capabilities() = nil", id)
		}
		if id == "newznab" {
			generic++
			if len(f.Definition.Links) != 0 {
				t.Errorf("generic family should carry no default base URL, got %v", f.Definition.Links)
			}
			continue
		}
		// Every preset carries exactly one default base URL.
		if len(f.Definition.Links) != 1 || f.Definition.Links[0] == "" {
			t.Errorf("preset %q must carry one default base URL, got %v", id, f.Definition.Links)
		}
	}
	if generic != 1 {
		t.Errorf("Families() must contain exactly one generic 'newznab' family, found %d", generic)
	}

	// Every preset in the table is surfaced as a family.
	for _, p := range presets {
		if !seen[p.id] {
			t.Errorf("preset %q (%s) missing from Families()", p.id, p.name)
		}
	}
}

func famID(f native.Family) string {
	if f.Definition == nil {
		return "<nil def>"
	}
	return f.Definition.ID
}

// TestSettingsApikeyIsSecret proves the apikey field is classified as a secret (encrypted at
// rest, redacted by the API) and that apiPath defaults to /api.
func TestSettingsApikeyIsSecret(t *testing.T) {
	t.Parallel()
	def := GenericDefinition()
	got := map[string]loader.SettingsField{}
	for _, s := range def.Settings {
		got[s.Name] = s
	}
	apikey, ok := got["apikey"]
	if !ok || !apikey.IsSecret() {
		t.Errorf("apikey field missing or not a secret: %+v", apikey)
	}
	apiPath, ok := got["apiPath"]
	if !ok {
		t.Fatal("apiPath field missing")
	}
	if apiPath.Default == nil || apiPath.Default.Value != defaultAPIPath {
		t.Errorf("apiPath default = %+v, want %q", apiPath.Default, defaultAPIPath)
	}
}

// TestPlaceholderCaps proves the placeholder advertises all five search modes and maps the
// standard top-level category ids 1:1 to themselves (the seam Leaf 5 replaces with a live
// caps fetch).
func TestPlaceholderCaps(t *testing.T) {
	t.Parallel()
	caps := caps(t)
	for _, mode := range []string{"search", "tv-search", "movie-search", "music-search", "book-search"} {
		if caps.Modes[mode] == nil {
			t.Errorf("missing advertised mode %q", mode)
		}
	}
	for _, id := range []string{"1000", "2000", "3000", "5000", "7000", "8000"} {
		got := caps.CategoryMap.MapTrackerCatToNewznab(id)
		want := mustAtoi(id)
		if !slices.Contains(got, want) {
			t.Errorf("category %q -> %v, want it to include %d", id, got, want)
		}
	}
}

func caps(t *testing.T) *mapper.Capabilities {
	t.Helper()
	d, err := New(native.Params{Def: GenericDefinition()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.Capabilities()
}

func mustAtoi(s string) int {
	switch s {
	case "1000":
		return 1000
	case "2000":
		return 2000
	case "3000":
		return 3000
	case "5000":
		return 5000
	case "7000":
		return 7000
	case "8000":
		return 8000
	}
	return -1
}
