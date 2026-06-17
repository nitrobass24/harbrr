package myanonamouse

import (
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// buildFamilyDriver constructs a driver from the family's definition (no doer/creds
// needed to exercise the capabilities — Search/Grab are not called here).
func buildFamilyDriver(t *testing.T) native.Driver {
	t.Helper()
	for _, f := range Families() {
		if f.Definition.ID == "myanonamouse" {
			d, err := f.Factory(native.Params{Def: f.Definition})
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			return d
		}
	}
	t.Fatal("no myanonamouse family")
	return nil
}

// TestFamilies proves the catalog has the single MAM site, it builds without error
// (so mapper.Build accepts the Go-built caps), it needs the /dl resolver, carries the
// configured RequestDelay, and advertises both the basic and book search modes.
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
	if f.Definition.ID != "myanonamouse" {
		t.Fatalf("family id = %q, want myanonamouse", f.Definition.ID)
	}
	if f.Definition.RequestDelay == nil || *f.Definition.RequestDelay != requestDelaySeconds {
		t.Errorf("RequestDelay = %v, want %v", f.Definition.RequestDelay, requestDelaySeconds)
	}
	d := buildFamilyDriver(t)
	if !d.NeedsResolver() {
		t.Error("NeedsResolver = false, want true (downloads need the mam_id Cookie)")
	}
	if d.DownloadNeedsAuth() {
		t.Error("DownloadNeedsAuth = true, want false (already routed through /dl)")
	}
	caps := d.Capabilities()
	if caps.Modes["search"] == nil {
		t.Error("missing the always-available search mode")
	}
	if caps.Modes["book-search"] == nil {
		t.Error("missing the book-search mode")
	}
}

// TestMamIDIsSecret proves the mam_id setting is classified as a secret (encrypted at
// rest, redacted in API responses) while the search-scope toggles are not.
func TestMamIDIsSecret(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		"mam_id":                true,
		"search_in_description": false,
		"search_in_series":      false,
		"search_in_filenames":   false,
	}
	seen := map[string]bool{}
	for _, s := range credentialSettings() {
		seen[s.Name] = true
		if got := s.IsSecret(); got != want[s.Name] {
			t.Errorf("%s IsSecret = %v, want %v", s.Name, got, want[s.Name])
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("missing setting %q", name)
		}
	}
}

// TestCategoryMapping proves the ported category map resolves tracker ids to the
// expected newznab categories: 13 (AudioBooks) -> Audio/Audiobook (3030); 14
// (E-Books) -> Books/EBook (7020); 61 (Comics) -> Books/Comics (7030); 79
// (Magazines) -> Books/Mags (7010); 80 (Math/Sci/Tech) -> Books/Technical (7040).
// Each also synthesises Jackett's custom (1:1) category at id+100000.
func TestCategoryMapping(t *testing.T) {
	t.Parallel()
	caps := buildFamilyDriver(t).Capabilities()
	cases := []struct {
		id   string
		want int
	}{
		{"13", 3030},
		{"14", 7020},
		{"61", 7030},
		{"79", 7010},
		{"80", 7040},
	}
	for _, tc := range cases {
		if got := caps.CategoryMap.MapTrackerCatToNewznab(tc.id); !slices.Contains(got, tc.want) {
			t.Errorf("cat %s -> %v, want it to include %d", tc.id, got, tc.want)
		}
	}
}
