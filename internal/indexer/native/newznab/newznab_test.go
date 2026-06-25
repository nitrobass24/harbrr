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
