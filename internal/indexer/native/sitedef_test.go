package native

import (
	"reflect"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

// TestSiteDefinition proves Definition() fills the per-family constants
// (type/encoding/description format), defaults the language to en-US unless the
// site sets one, and carries the site data through unchanged.
func TestSiteDefinition(t *testing.T) {
	t.Parallel()

	settings := []loader.SettingsField{FieldUsername, FieldPasskey}
	caps := loader.Caps{CategoryMappings: Cats(Cat{ID: "1", Newznab: "Movies", Desc: "Film"})}

	tests := []struct {
		name     string
		site     Site
		wantLang string
	}{
		{
			name: "defaults",
			site: Site{
				ID: "example", Name: "Example", Link: "https://example.org/",
				Driver: "HTML-scrape", DelaySeconds: 2.5,
				Settings: settings, Caps: caps,
			},
			wantLang: "en-US",
		},
		{
			name: "explicit language",
			site: Site{
				ID: "exemplu", Name: "Exemplu", Link: "https://exemplu.ro/",
				Driver: "Exemplu", DelaySeconds: 24.0, Language: "ro-RO",
				Settings: settings, Caps: caps,
			},
			wantLang: "ro-RO",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := tt.site.Definition()
			if d.ID != tt.site.ID || d.Name != tt.site.Name {
				t.Errorf("ID/Name = %q/%q, want %q/%q", d.ID, d.Name, tt.site.ID, tt.site.Name)
			}
			wantDesc := tt.site.Name + " (native " + tt.site.Driver + " driver)"
			if d.Description != wantDesc {
				t.Errorf("Description = %q, want %q", d.Description, wantDesc)
			}
			if d.Language != tt.wantLang {
				t.Errorf("Language = %q, want %q", d.Language, tt.wantLang)
			}
			if d.Type != "private" || d.Encoding != "UTF-8" {
				t.Errorf("Type/Encoding = %q/%q, want private/UTF-8", d.Type, d.Encoding)
			}
			if !reflect.DeepEqual(d.Links, []string{tt.site.Link}) {
				t.Errorf("Links = %v, want [%s]", d.Links, tt.site.Link)
			}
			if d.RequestDelay == nil || *d.RequestDelay != tt.site.DelaySeconds {
				t.Errorf("RequestDelay = %v, want %v", d.RequestDelay, tt.site.DelaySeconds)
			}
			if !reflect.DeepEqual(d.Settings, tt.site.Settings) {
				t.Errorf("Settings = %v, want %v", d.Settings, tt.site.Settings)
			}
			if !reflect.DeepEqual(d.Caps, tt.site.Caps) {
				t.Errorf("Caps = %v, want %v", d.Caps, tt.site.Caps)
			}
		})
	}
}

// TestCats proves each Cat field lands on the right CategoryMapping field — the
// invariant the old positional helpers could silently violate.
func TestCats(t *testing.T) {
	t.Parallel()

	got := Cats(
		Cat{ID: "72", Newznab: "Movies", Desc: "Movies"},
		Cat{ID: "1", Newznab: "TV/Anime", Desc: "TV Series"},
		Cat{ID: "2", Newznab: "TV"},
	)
	want := []loader.CategoryMapping{
		{ID: loader.Scalar{Value: "72", Set: true}, Cat: "Movies", Desc: "Movies"},
		{ID: loader.Scalar{Value: "1", Set: true}, Cat: "TV/Anime", Desc: "TV Series"},
		{ID: loader.Scalar{Value: "2", Set: true}, Cat: "TV"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Cats = %+v, want %+v", got, want)
	}
}

// TestFieldsComplete proves every kit field carries a non-empty Name/Label/Type.
func TestFieldsComplete(t *testing.T) {
	t.Parallel()

	fields := []loader.SettingsField{
		FieldUsername, FieldPassword, FieldAPIKey, FieldPasskey, FieldCookie,
		FieldUserAgent, FieldPID, FieldFreeleechOnly, FieldUseFreeleechToken,
	}
	for _, f := range fields {
		if f.Name == "" || f.Label == "" || f.Type == "" {
			t.Errorf("field %+v has an empty Name, Label, or Type", f)
		}
	}
}
