package torznab

import (
	"strconv"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// validCfg returns a Cfg satisfying a preset's key policy, so a table-driven test can
// build every family through its own validation gate.
func validCfg(policy keyPolicy) map[string]string {
	if policy == keyRequired32 {
		return map[string]string{"apikey": testAPIKey}
	}
	if policy == keyRequired {
		return map[string]string{"apikey": "any-length-key"}
	}
	return nil // keyNone, keyOptional
}

// TestFamilies proves Families() returns the generic entry plus every preset, that
// each is a distinct, torrent-protocol family whose Factory builds a working driver,
// that every preset carries a default base-URL link the generic deliberately omits,
// and that each site's NeedsResolver posture matches its table row (per-preset — MTV
// credentialed=true, AnimeTosho public storage links=false, TN unknown=safe true,
// generic unknown=safe true).
func TestFamilies(t *testing.T) {
	t.Parallel()
	fams := Families()
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

		if f.Definition.EffectiveProtocol() != loader.ProtocolTorrent {
			t.Errorf("family %q protocol = %q, want torrent", id, f.Definition.EffectiveProtocol())
		}
		if _, err := mapper.Build(f.Definition); err != nil {
			t.Errorf("mapper.Build(%q): %v", id, err)
		}

		d, err := f.Factory(native.Params{Def: f.Definition, Cfg: validCfg(profileFor(id).policy)})
		if err != nil {
			t.Errorf("factory(%q): %v", id, err)
			continue
		}
		if d.Capabilities() == nil {
			t.Errorf("family %q Capabilities() = nil", id)
		}
		if got, want := d.NeedsResolver(), profileFor(id).needsResolver; got != want {
			t.Errorf("family %q NeedsResolver = %v, want %v (per-preset posture)", id, got, want)
		}
		if d.DownloadNeedsAuth() {
			t.Errorf("family %q DownloadNeedsAuth = true, want false (URL-credentialed links are sealed by NeedsResolver instead)", id)
		}
		if d.SupportsOffsetPaging() {
			t.Errorf("family %q SupportsOffsetPaging = true, want false (Base default; the request generator sends no offset)", id)
		}

		if id == "torznab" {
			generic++
			if len(f.Definition.Links) != 0 {
				t.Errorf("generic family should carry no default base URL, got %v", f.Definition.Links)
			}
			continue
		}
		if len(f.Definition.Links) != 1 || f.Definition.Links[0] == "" {
			t.Errorf("preset %q must carry one default base URL, got %v", id, f.Definition.Links)
		}
	}
	if generic != 1 {
		t.Errorf("Families() must contain exactly one generic 'torznab' family, found %d", generic)
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

// TestPresetDefinitionOverrides proves the per-preset definition facts: AnimeTosho is
// public and keyless (no settings at all); Torrent Network is German (de-DE) and
// private; MoreThanTV is private with the apikey + keyInfo settings pair.
func TestPresetDefinitionOverrides(t *testing.T) {
	t.Parallel()
	defs := map[string]*loader.Definition{}
	for _, p := range presets {
		defs[p.id] = presetDefinition(p)
	}

	mtv := defs["morethantv"]
	if mtv.Type != "private" || mtv.Language != "en-US" {
		t.Errorf("morethantv type/language = %q/%q, want private/en-US", mtv.Type, mtv.Language)
	}
	if len(mtv.Settings) != 2 {
		t.Errorf("morethantv settings = %d fields, want 2 (apikey + keyInfo)", len(mtv.Settings))
	}

	at := defs["animetosho"]
	if at.Type != "public" {
		t.Errorf("animetosho type = %q, want public", at.Type)
	}
	if len(at.Settings) != 0 {
		t.Errorf("animetosho settings = %v, want none (keyless public feed)", at.Settings)
	}

	tn := defs["torrentnetwork"]
	if tn.Type != "private" || tn.Language != "de-DE" {
		t.Errorf("torrentnetwork type/language = %q/%q, want private/de-DE", tn.Type, tn.Language)
	}
	if len(tn.Settings) != 1 || tn.Settings[0].Name != "apikey" {
		t.Errorf("torrentnetwork settings = %v, want the single apikey field (no key-page hint known)", tn.Settings)
	}
}

// TestSettingsAPIKeyIsSecret proves the apikey field is classified as a secret
// (encrypted at rest, redacted by the API) on every definition that carries one, that
// MTV's keyInfo field is a never-secret info display field, and that the generic
// entry's apiPath setting defaults to /api.
func TestSettingsAPIKeyIsSecret(t *testing.T) {
	t.Parallel()
	check := func(def *loader.Definition, wantKeyInfo bool) {
		t.Helper()
		got := map[string]loader.SettingsField{}
		for _, s := range def.Settings {
			got[s.Name] = s
		}
		apikey, ok := got["apikey"]
		if !ok || !apikey.IsSecret() {
			t.Errorf("%s: apikey field missing or not a secret: %+v", def.ID, apikey)
		}
		if keyInfo, ok := got["keyInfo"]; wantKeyInfo {
			if !ok {
				t.Errorf("%s: keyInfo field missing", def.ID)
			} else if keyInfo.IsSecret() {
				t.Errorf("%s: keyInfo should never be classified secret (display-only)", def.ID)
			}
		}
	}
	mtv, _ := presetByID("morethantv")
	tn, _ := presetByID("torrentnetwork")
	check(presetDefinition(mtv), true)
	check(presetDefinition(tn), false)

	generic := GenericDefinition()
	check(generic, false)
	var apiPath *loader.SettingsField
	for i := range generic.Settings {
		if generic.Settings[i].Name == "apiPath" {
			apiPath = &generic.Settings[i]
		}
	}
	if apiPath == nil {
		t.Fatal("generic: apiPath field missing")
	}
	if apiPath.Default == nil || apiPath.Default.Value != defaultAPIPath {
		t.Errorf("generic: apiPath default = %+v, want %q", apiPath.Default, defaultAPIPath)
	}
}

// TestPresetCaps proves the per-preset advertised categories: MoreThanTV's eight
// pass-through ids (Jackett's SetCapabilities), AnimeTosho's {2020, 5070} (Prowlarr's
// preset seed), and the TN/generic full standard parent table (no seed — the remote
// tree is unknown). All advertise exactly search/tv-search/movie-search.
func TestPresetCaps(t *testing.T) {
	t.Parallel()
	capsFor := func(def *loader.Definition, cfg map[string]string) *mapper.Capabilities {
		t.Helper()
		d, err := New(native.Params{Def: def, Cfg: cfg})
		if err != nil {
			t.Fatalf("New(%s): %v", def.ID, err)
		}
		return d.Capabilities()
	}
	assertPassThrough := func(caps *mapper.Capabilities, ids []int, label string) {
		t.Helper()
		for _, id := range ids {
			got := caps.CategoryMap.MapTrackerCatToNewznab(strconv.Itoa(id))
			if len(got) != 1 || got[0] != id {
				t.Errorf("%s: category %d -> %v, want [%d] (pass-through)", label, id, got, id)
			}
		}
	}
	assertModes := func(caps *mapper.Capabilities, label string) {
		t.Helper()
		if len(caps.Modes["tv-search"]) == 0 || len(caps.Modes["movie-search"]) == 0 {
			t.Errorf("%s: missing tv-search/movie-search modes", label)
		}
		if len(caps.Modes["music-search"]) != 0 || len(caps.Modes["book-search"]) != 0 {
			t.Errorf("%s: torznab must not advertise music/book search (the request generator has no params for them)", label)
		}
	}

	mtv, _ := presetByID("morethantv")
	mtvCaps := capsFor(presetDefinition(mtv), map[string]string{"apikey": testAPIKey})
	assertPassThrough(mtvCaps, []int{5030, 5040, 5045, 5060, 2030, 2040, 2045, 2050}, "morethantv")
	assertModes(mtvCaps, "morethantv")

	at, _ := presetByID("animetosho")
	atCaps := capsFor(presetDefinition(at), nil)
	assertPassThrough(atCaps, []int{2020, 5070}, "animetosho")
	assertModes(atCaps, "animetosho")
	if got := atCaps.CategoryMap.MapTrackerCatToNewznab("5030"); len(got) != 0 {
		t.Errorf("animetosho: category 5030 -> %v, want unmapped (only the seeded pair is advertised)", got)
	}

	tn, _ := presetByID("torrentnetwork")
	tnCaps := capsFor(presetDefinition(tn), map[string]string{"apikey": "k"})
	assertPassThrough(tnCaps, []int{1000, 2000, 3000, 5000, 7000, 8000}, "torrentnetwork (full parent table)")
	assertModes(tnCaps, "torrentnetwork")

	genCaps := capsFor(GenericDefinition(), nil)
	assertPassThrough(genCaps, []int{1000, 2000, 3000, 5000, 7000, 8000}, "generic (full parent table)")
	assertModes(genCaps, "generic")
}

// TestNewValidatesAPIKeyPerPolicy proves each site's construction-time key rule:
// MoreThanTV requires exactly 32 chars (Jackett's MTV-specific rule); Torrent Network
// requires a non-empty key of any length (Prowlarr validates nothing); AnimeTosho has
// no key (and any stray configured value is dropped, never sent); the generic entry's
// key is optional and unvalidated. All rejections are clear and secret-free.
func TestNewValidatesAPIKeyPerPolicy(t *testing.T) {
	t.Parallel()
	mtv, _ := presetByID("morethantv")
	at, _ := presetByID("animetosho")
	tn, _ := presetByID("torrentnetwork")
	cases := []struct {
		name   string
		def    *loader.Definition
		apikey string
		wantOK bool
	}{
		{"mtv missing", presetDefinition(mtv), "", false},
		{"mtv too short", presetDefinition(mtv), "short", false},
		{"mtv too long", presetDefinition(mtv), testAPIKey + "extra", false},
		{"mtv exactly 32", presetDefinition(mtv), testAPIKey, true},
		{"tn missing", presetDefinition(tn), "", false},
		{"tn short key ok (no length rule)", presetDefinition(tn), "k", true},
		{"tn long key ok (no length rule)", presetDefinition(tn), testAPIKey + "-and-more", true},
		{"animetosho no key ok", presetDefinition(at), "", true},
		{"animetosho stray key tolerated", presetDefinition(at), "stray", true},
		{"generic no key ok", GenericDefinition(), "", true},
		{"generic any key ok", GenericDefinition(), "whatever-length", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(native.Params{Def: c.def, Cfg: map[string]string{"apikey": c.apikey}})
			if c.wantOK && err != nil {
				t.Fatalf("New = %v, want nil", err)
			}
			if !c.wantOK {
				if err == nil {
					t.Fatal("New = nil, want an error")
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
