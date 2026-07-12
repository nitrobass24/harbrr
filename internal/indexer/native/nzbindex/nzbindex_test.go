package nzbindex

import (
	"errors"
	stdhttp "net/http"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// TestFamilies proves Families() surfaces exactly one usenet family whose factory builds a
// working driver with non-nil capabilities.
func TestFamilies(t *testing.T) {
	t.Parallel()
	fams := Families()
	if len(fams) != 1 {
		t.Fatalf("Families() = %d, want 1", len(fams))
	}
	f := fams[0]
	if f.Definition == nil || f.Definition.ID != "nzbindex" {
		t.Fatalf("family definition id = %v, want nzbindex", f.Definition)
	}
	if f.Definition.EffectiveProtocol() != loader.ProtocolUsenet {
		t.Errorf("protocol = %q, want usenet", f.Definition.EffectiveProtocol())
	}
	d, err := f.Factory(native.Params{Def: f.Definition})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if d.Capabilities() == nil {
		t.Error("Capabilities() = nil")
	}
}

// TestDriverFlags pins the serve-path capability answers: NZBIndex needs no resolver, its
// public download needs no auth (served bare), and it forwards paging upstream.
func TestDriverFlags(t *testing.T) {
	t.Parallel()
	d := testDriver(t, nil, nil)
	if d.NeedsResolver() {
		t.Error("NeedsResolver() = true, want false")
	}
	if d.DownloadNeedsAuth() {
		t.Error("DownloadNeedsAuth() = true, want false (public keyless download)")
	}
	if !d.SupportsOffsetPaging() {
		t.Error("SupportsOffsetPaging() = false, want true")
	}
}

// TestDefinitionHasOptionalApikey proves the single settable field is the optional apikey
// (name "apikey" so the secret store auto-classifies it as a secret).
func TestDefinitionHasOptionalApikey(t *testing.T) {
	t.Parallel()
	def := Definition()
	if def.Type != "public" {
		t.Errorf("type = %q, want public", def.Type)
	}
	if len(def.Settings) != 1 || def.Settings[0].Name != "apikey" {
		t.Fatalf("settings = %+v, want a single apikey field", def.Settings)
	}
}

// TestSearchEndToEnd proves Search issues the request, parses the golden, and returns the
// two valid releases (the third fixture row is skipped).
func TestSearchEndToEnd(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(*stdhttp.Request) *stdhttp.Response {
		return jsonResponse(string(readGolden(t, "search.json")))
	}}
	d := testDriver(t, nil, doer)
	releases, err := d.Search(t.Context(), search.Query{Keywords: "test"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases = %d, want 2", len(releases))
	}
	if releases[0].Title != "Ubuntu Gubuntu 11.10 Unity Edition (64bit)" {
		t.Errorf("first title = %q", releases[0].Title)
	}
}

// TestSearchStatusClassification proves the HTTP status → error mapping: 401 is a login
// failure, 429/503/403 are rate limits, other non-2xx is a plain error.
func TestSearchStatusClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code       int
		wantLogin  bool
		wantRate   bool
		wantErrAny bool
	}{
		{stdhttp.StatusUnauthorized, true, false, false},
		{stdhttp.StatusTooManyRequests, false, true, false},
		{stdhttp.StatusServiceUnavailable, false, true, false},
		{stdhttp.StatusForbidden, false, true, false},
		{stdhttp.StatusInternalServerError, false, false, true},
	}
	for _, tt := range tests {
		t.Run(stdhttp.StatusText(tt.code), func(t *testing.T) {
			t.Parallel()
			doer := &scriptDoer{handler: func(*stdhttp.Request) *stdhttp.Response { return statusResponse(tt.code) }}
			d := testDriver(t, nil, doer)
			_, err := d.Search(t.Context(), search.Query{Keywords: "x"})
			if err == nil {
				t.Fatalf("status %d: want an error", tt.code)
			}
			if tt.wantLogin && !errors.Is(err, login.ErrLoginFailed) {
				t.Errorf("status %d: err = %v, want ErrLoginFailed", tt.code, err)
			}
			var rl *search.RateLimitedError
			if tt.wantRate && !errors.As(err, &rl) {
				t.Errorf("status %d: err = %v, want RateLimitedError", tt.code, err)
			}
		})
	}
}
