package speedapp

import (
	stdhttp "net/http"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestRetroFlixDefinition(t *testing.T) {
	t.Parallel()
	def := Definition()
	if def.ID != "retroflix" || def.Name != "RetroFlix" || def.Type != "private" {
		t.Errorf("identity = %q/%q/%q", def.ID, def.Name, def.Type)
	}
	if want := []string{"https://retroflix.club/", "https://retroflix.net/"}; !reflect.DeepEqual(def.Links, want) {
		t.Errorf("Links = %v, want %v", def.Links, want)
	}
	if def.RequestDelay == nil || time.Duration(*def.RequestDelay*float64(time.Second)) != 2100*time.Millisecond {
		t.Errorf("RequestDelay = %v, want 2.1 seconds", def.RequestDelay)
	}
	if len(def.Settings) != 2 {
		t.Fatalf("Settings = %d, want 2", len(def.Settings))
	}
	for _, setting := range def.Settings {
		if !setting.Required || !setting.IsSecret() || setting.Type != "password" {
			t.Errorf("setting %q = required:%v secret:%v type:%q", setting.Name, setting.Required, setting.IsSecret(), setting.Type)
		}
	}

	wantModes := map[string][]string{
		"search":       {"q"},
		"movie-search": {"q", "imdbid"},
		"tv-search":    {"q", "season", "ep", "imdbid"},
		"music-search": {"q"},
		"book-search":  {"q"},
	}
	d := testDriver(t, &scriptDoer{})
	if !reflect.DeepEqual(d.Capabilities().Modes, wantModes) {
		t.Errorf("Modes = %v, want %v", d.Capabilities().Modes, wantModes)
	}
	if !d.Capabilities().AllowTVSearchIMDB {
		t.Error("AllowTVSearchIMDB = false")
	}
	if d.Capabilities().Limits.Default != 100 || d.Capabilities().Limits.Max != 100 {
		t.Errorf("Limits = %+v, want 100/100", d.Capabilities().Limits)
	}

	wantCategories := map[string][]int{
		"401": {2000},
		"402": {5000},
		"406": {3020},
		"407": {5060},
		"408": {3000},
		"409": {7000},
	}
	for trackerID, want := range wantCategories {
		if got := d.categories(mustInt64(t, trackerID)); !reflect.DeepEqual(got, want) {
			t.Errorf("category %s = %v, want %v", trackerID, got, want)
		}
	}
	if got := d.categories(999); got != nil {
		t.Errorf("unmapped category = %v, want nil", got)
	}
}

func TestDriverFlagsAndTest(t *testing.T) {
	t.Parallel()
	doer := &scriptDoer{handler: func(req *stdhttp.Request, _ []byte) (*stdhttp.Response, error) {
		if req.URL.Path != "/api/login" {
			t.Fatalf("request path = %q, want /api/login", req.URL.Path)
		}
		return jsonResponse(stdhttp.StatusOK, `{"token":"`+testToken+`"}`), nil
	}}
	d := testDriver(t, doer)
	if d.NeedsResolver() || !d.DownloadNeedsAuth() || !d.SupportsOffsetPaging() || d.ConsumesSearchMode() {
		t.Errorf("flags resolver=%v downloadAuth=%v paging=%v consumesMode=%v",
			d.NeedsResolver(), d.DownloadNeedsAuth(), d.SupportsOffsetPaging(), d.ConsumesSearchMode())
	}
	if err := d.Test(t.Context()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(doer.records()) != 1 {
		t.Errorf("requests = %d, want one login", len(doer.records()))
	}
}

func mustInt64(t *testing.T, raw string) int64 {
	t.Helper()
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("ParseInt(%q): %v", raw, err)
	}
	return value
}
