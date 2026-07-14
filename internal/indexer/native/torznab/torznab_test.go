package torznab

import (
	"strconv"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// TestFamilies proves Families() returns exactly the preset table (morethantv, for
// now), that each is a distinct, torrent-protocol family whose Factory builds a
// working driver, carries a default base-URL link, and NEEDS the resolver (its
// download URL is credentialed) while NOT needing out-of-band download auth.
func TestFamilies(t *testing.T) {
	t.Parallel()
	fams := Families()
	if len(fams) != len(presets) {
		t.Fatalf("Families() = %d families, want %d (the preset table)", len(fams), len(presets))
	}

	seen := map[string]bool{}
	for _, f := range fams {
		if f.Definition == nil || f.Factory == nil {
			t.Fatalf("family %q has nil definition/factory", famID(f))
		}
		id := f.Definition.ID
		if seen[id] {
			t.Errorf("duplicate family id %q", id)
		}
		seen[id] = true

		if f.Definition.EffectiveProtocol() != loader.ProtocolTorrent {
			t.Errorf("family %q protocol = %q, want torrent", id, f.Definition.EffectiveProtocol())
		}
		if _, err := mapper.Build(f.Definition); err != nil {
			t.Errorf("mapper.Build(%q): %v", id, err)
		}
		if len(f.Definition.Links) != 1 || f.Definition.Links[0] == "" {
			t.Errorf("preset %q must carry one default base URL, got %v", id, f.Definition.Links)
		}

		d, err := f.Factory(native.Params{Def: f.Definition, Cfg: map[string]string{"apikey": testAPIKey}})
		if err != nil {
			t.Errorf("factory(%q): %v", id, err)
			continue
		}
		if d.Capabilities() == nil {
			t.Errorf("family %q Capabilities() = nil", id)
		}
		if !d.NeedsResolver() {
			t.Errorf("family %q NeedsResolver = false, want true (credentialed download URL)", id)
		}
		if d.DownloadNeedsAuth() {
			t.Errorf("family %q DownloadNeedsAuth = true, want false (already routed via NeedsResolver)", id)
		}
		if d.SupportsOffsetPaging() {
			t.Errorf("family %q SupportsOffsetPaging = true, want false (Base default; MTV sends no offset)", id)
		}
	}

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

// TestSettingsAPIKeyIsSecret proves the apikey field is classified as a secret
// (encrypted at rest, redacted by the API) and that the keyInfo field is a
// never-secret info display field.
func TestSettingsAPIKeyIsSecret(t *testing.T) {
	t.Parallel()
	fams := Families()
	if len(fams) == 0 {
		t.Fatal("Families() returned no families")
	}
	got := map[string]loader.SettingsField{}
	for _, s := range fams[0].Definition.Settings {
		got[s.Name] = s
	}
	apikey, ok := got["apikey"]
	if !ok || !apikey.IsSecret() {
		t.Errorf("apikey field missing or not a secret: %+v", apikey)
	}
	keyInfo, ok := got["keyInfo"]
	if !ok {
		t.Fatal("keyInfo field missing")
	}
	if keyInfo.IsSecret() {
		t.Error("keyInfo should never be classified secret (it is display-only)")
	}
}

// TestPresetCaps proves the MoreThanTV preset advertises exactly the eight
// pass-through categories (Jackett's SetCapabilities), each mapped 1:1 to itself, and
// the tv-search/movie-search param sets Jackett declares.
func TestPresetCaps(t *testing.T) {
	t.Parallel()
	d, err := New(native.Params{Def: presetDefinition(presets[0]), Cfg: map[string]string{"apikey": testAPIKey}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	caps := d.Capabilities()

	wantCats := []int{5030, 5040, 5045, 5060, 2030, 2040, 2045, 2050}
	for _, id := range wantCats {
		got := caps.CategoryMap.MapTrackerCatToNewznab(strconv.Itoa(id))
		if len(got) != 1 || got[0] != id {
			t.Errorf("category %d -> %v, want [%d] (pass-through)", id, got, id)
		}
	}
	if len(caps.Modes["tv-search"]) == 0 {
		t.Error("missing tv-search mode")
	}
	if len(caps.Modes["movie-search"]) == 0 {
		t.Error("missing movie-search mode")
	}
	if len(caps.Modes["music-search"]) != 0 || len(caps.Modes["book-search"]) != 0 {
		t.Error("torznab preset should not advertise music/book search")
	}
}

// TestNewValidatesAPIKeyLength proves the 32-char apikey validation at construction:
// a missing, short, or long key is rejected with a clear, secret-free error; a valid
// key builds cleanly.
func TestNewValidatesAPIKeyLength(t *testing.T) {
	t.Parallel()
	def := presetDefinition(presets[0])
	cases := []struct {
		name   string
		apikey string
		wantOK bool
	}{
		{"missing", "", false},
		{"too short", "short", false},
		{"too long", testAPIKey + "extra", false},
		{"exactly 32", testAPIKey, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(native.Params{Def: def, Cfg: map[string]string{"apikey": c.apikey}})
			if c.wantOK && err != nil {
				t.Fatalf("New(%q) = %v, want nil", c.name, err)
			}
			if !c.wantOK {
				if err == nil {
					t.Fatalf("New(%q) = nil, want an error", c.name)
				}
				assertNoAPIKey(t, "apikey validation error", err.Error())
			}
		})
	}
}

// TestNewNilDefinition proves a nil definition is rejected rather than panicking.
func TestNewNilDefinition(t *testing.T) {
	t.Parallel()
	_, err := New(native.Params{Cfg: map[string]string{"apikey": testAPIKey}})
	if err == nil {
		t.Fatal("New(nil def) = nil, want an error")
	}
}
