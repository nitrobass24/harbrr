package nebulance

import (
	"context"
	"errors"
	stdhttp "net/http"
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

func buildDriver(t *testing.T) *driver {
	t.Helper()
	family := Families()[0]
	built, err := family.Factory(native.Params{Def: family.Definition, Cfg: map[string]string{"apikey": testAPIKey}})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	return built.(*driver)
}

func TestNewRequiresAPIKey(t *testing.T) {
	t.Parallel()
	def := Families()[0].Definition
	for _, tt := range []struct {
		name string
		cfg  map[string]string
	}{
		{name: "missing"},
		{name: "empty", cfg: map[string]string{"apikey": ""}},
		{name: "whitespace", cfg: map[string]string{"apikey": " \t "}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(native.Params{Def: def, Cfg: tt.cfg}); err == nil {
				t.Fatal("New succeeded without an API key")
			}
		})
	}
	if _, err := New(native.Params{Def: def, Cfg: map[string]string{"apikey": testAPIKey}}); err != nil {
		t.Fatalf("New with API key: %v", err)
	}
}

func TestTestRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
		t.Fatal("Test issued a request without an API key")
		return nil, errors.New("unexpected request")
	}}
	driver := liveDriver(t, doer)
	driver.Cfg["apikey"] = ""
	if err := driver.Test(context.Background()); !errors.Is(err, errAPIKeyRequired) {
		t.Errorf("Test err = %v, want errAPIKeyRequired", err)
	}
	if len(doer.reqs) != 0 {
		t.Errorf("Test requests = %d, want 0", len(doer.reqs))
	}
}

func TestTestConfiguredUnauthorizedRemainsAuthFailure(t *testing.T) {
	t.Parallel()
	driver := liveDriver(t, &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
		return response(stdhttp.StatusUnauthorized, "nope"), nil
	}})
	if err := driver.Test(context.Background()); !errors.Is(err, login.ErrLoginFailed) {
		t.Errorf("Test err = %v, want login.ErrLoginFailed", err)
	}
}

func TestFamilies(t *testing.T) {
	t.Parallel()
	families := Families()
	if len(families) != 1 {
		t.Fatalf("families = %d, want 1", len(families))
	}
	family := families[0]
	if family.Definition == nil || family.Factory == nil {
		t.Fatal("family has nil definition/factory")
	}
	if family.Definition.ID != "nebulance" {
		t.Errorf("definition id = %q, want nebulance", family.Definition.ID)
	}
	if family.Definition.RequestDelay == nil || *family.Definition.RequestDelay != requestDelaySeconds {
		t.Errorf("request delay = %v, want %v", family.Definition.RequestDelay, requestDelaySeconds)
	}
	if _, err := mapper.Build(family.Definition); err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}
	if len(family.Definition.Settings) != 1 || !family.Definition.Settings[0].IsSecret() {
		t.Error("apikey setting must be classified as a secret")
	}

	driver := buildDriver(t)
	if !driver.NeedsResolver() {
		t.Error("NeedsResolver = false, want true")
	}
	if driver.DownloadNeedsAuth() {
		t.Error("DownloadNeedsAuth = true, want false")
	}
	if !driver.SupportsOffsetPaging() {
		t.Error("SupportsOffsetPaging = false, want true")
	}
}

func TestCapabilities(t *testing.T) {
	t.Parallel()
	caps := buildDriver(t).Capabilities()
	if !caps.AllowRawSearch {
		t.Error("AllowRawSearch = false, want true")
	}
	for _, parameter := range []string{"q", "season", "ep", "imdbid", "tvmazeid"} {
		if !slices.Contains(caps.Modes["tv-search"], parameter) {
			t.Errorf("tv-search missing %q", parameter)
		}
	}
	for trackerCategory, want := range map[string]int{"1": 5000, "2": 5030, "3": 5040, "4": 5045} {
		if got := caps.CategoryMap.MapTrackerCatToNewznab(trackerCategory); !slices.Contains(got, want) {
			t.Errorf("category %s -> %v, want %d", trackerCategory, got, want)
		}
	}
}
