package gazelle

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const (
	brokenStonesCookie   = "session=BS-SYNTHETIC-SESSION-0000000000"
	brokenStonesUsername = "bs-synthetic-user"
	brokenStonesPassword = "BS-SYNTHETIC-PASSWORD-0000000000"
)

// TestBrokenStonesDefinition pins BrokenStones' (#31) declared shape: it is a plain
// formLoginAuth Gazelle site (ADR 0003) with no upstream paging and no AlphaRatio-only
// settings, so its settings/caps/strategy are its own small table entry, not a shared-file
// branch.
func TestBrokenStonesDefinition(t *testing.T) {
	t.Parallel()

	family := familyByID(t, "brokenstones")
	def := family.Definition
	if def.RequestDelay == nil || *def.RequestDelay != brokenStonesDelaySeconds {
		t.Fatalf("RequestDelay = %v, want %v", def.RequestDelay, brokenStonesDelaySeconds)
	}
	if len(def.Links) != 1 || def.Links[0] != "https://brokenstones.is/" {
		t.Fatalf("Links = %v, want [https://brokenstones.is/]", def.Links)
	}

	type settingContract struct {
		secret   bool
		required bool
	}
	wantSettings := map[string]settingContract{
		"username":            {required: true},
		"password":            {secret: true, required: true},
		"use_freeleech_token": {},
	}
	if len(def.Settings) != len(wantSettings) {
		t.Errorf("settings count = %d, want %d", len(def.Settings), len(wantSettings))
	}
	for _, setting := range def.Settings {
		want, ok := wantSettings[setting.Name]
		if !ok {
			t.Errorf("unexpected setting %q", setting.Name)
			continue
		}
		if got := setting.IsSecret(); got != want.secret {
			t.Errorf("setting %q secret = %t, want %t", setting.Name, got, want.secret)
		}
		if setting.Required != want.required {
			t.Errorf("setting %q required = %t, want %t", setting.Name, setting.Required, want.required)
		}
	}

	if _, ok := siteConfigs["brokenstones"].strategy.(formLoginAuth); !ok {
		t.Errorf("brokenstones: strategy = %T, want formLoginAuth", siteConfigs["brokenstones"].strategy)
	}

	driver := buildDriver(t, "brokenstones")
	if pager, ok := driver.(interface{ SupportsOffsetPaging() bool }); ok && pager.SupportsOffsetPaging() {
		t.Fatal("BrokenStones has no upstream paging (unlike AlphaRatio)")
	}
	caps := driver.Capabilities()
	if got := caps.CategoryMap.MapTrackerCatToNewznab("1"); !slices.Contains(got, 4030) {
		t.Errorf("MacOS Apps category = %v, want PC/Mac (4030)", got)
	}
	if got := caps.CategoryMap.MapTrackerCatToNewznab("3"); !slices.Contains(got, 4060) {
		t.Errorf("iOS Apps category = %v, want PC/Mobile-iOS (4060)", got)
	}
	if caps.Modes["movie-search"] != nil || caps.Modes["tv-search"] != nil {
		t.Errorf("movie/tv-search should not be advertised: %+v", caps.Modes)
	}
}

// TestBrokenStonesSearchAndParse exercises a browse response through the shared
// RED/OPS-style non-music parse path (no parseProfile override) against a synthetic
// ajax.php fixture, proving the formLoginAuth session headers ride the request and the
// site's own category table resolves.
func TestBrokenStonesSearchAndParse(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("testdata/brokenstones_response.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	doer := &scriptDoer{resp: mkResp(200, string(body))}
	d := searchDriver(t, "brokenstones", doer)
	d.Cfg = map[string]string{
		"cookie":   brokenStonesCookie,
		"username": brokenStonesUsername,
		"password": brokenStonesPassword,
	}
	d.session = sessionState{cookie: brokenStonesCookie, generation: 1}

	releases, err := d.Search(context.Background(), search.Query{Keywords: "Example App"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(doer.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(doer.reqs))
	}
	req := doer.reqs[0]
	if req.cookie != brokenStonesCookie || req.userAgent != alphaRatioUserAgent {
		t.Errorf("headers cookie=%q user-agent=%q", req.cookie, req.userAgent)
	}

	if len(releases) != 1 {
		t.Fatalf("releases = %d, want 1", len(releases))
	}
	release := releases[0]
	if release.Title != "Example.App.1.2.3.macOS-GROUP" || release.Size != 987654321 {
		t.Errorf("release core fields = %+v", release)
	}
	// #424: this assertion used to pin the ajax.php URL, which is why the bug shipped
	// green — BrokenStones has no download-URL override upstream, so it inherits
	// torrents.php. Its ajax.php answers 500 for a cookie session. Do not "fix" this
	// back to ajax.php.
	if release.Link != "https://brokenstones.is/torrents.php?action=download&id=200" {
		t.Errorf("Link = %q, want the inherited torrents.php download (no downloadViaAjax override)", release.Link)
	}
	if release.Details != "" {
		t.Errorf("Details = %q, want empty (no parseProfile override for BrokenStones)", release.Details)
	}
	if !slices.Equal(release.Categories, []int{4030}) || release.Seeders != 7 || release.Leechers != 1 {
		t.Errorf("release category/swarm = %+v", release)
	}
	if release.MinimumRatio != 0 || release.MinimumSeedTime != 0 {
		t.Errorf("minimums ratio=%v seedtime=%d, want zero (no published Prowlarr values)", release.MinimumRatio, release.MinimumSeedTime)
	}
}
