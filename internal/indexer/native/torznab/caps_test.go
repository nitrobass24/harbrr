package torznab

import (
	"context"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// liveCaps is the ?t=caps document a Jackett/Prowlarr-style Torznab server serves: a
// category tree with CHILD ids (5040 under 5000) and a site-specific custom id
// (100001), neither of which the placeholder parent table can express (#643).
const liveCaps = `<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <limits max="100" default="100"/>
  <searching>
    <search available="yes" supportedParams="q"/>
    <tv-search available="yes" supportedParams="q,season,ep,imdbid,tvdbid"/>
    <movie-search available="yes" supportedParams="q,imdbid"/>
  </searching>
  <categories>
    <category id="2000" name="Movies">
      <subcat id="2040" name="HD"/>
    </category>
    <category id="5000" name="TV">
      <subcat id="5040" name="HD"/>
      <subcat id="100001" name="TV Packs"/>
    </category>
  </categories>
</caps>`

// liveFeed is a search response whose items carry the LEAF category ids the live caps
// advertise — the ids that resolved to no category at all before the caps fetch.
const liveFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <item>
      <title>Some Show S01E01 1080p</title>
      <guid>https://tz.example.test/t/1</guid>
      <link>https://tz.example.test/dl/1</link>
      <category>5040</category>
      <torznab:attr name="seeders" value="7"/>
      <torznab:attr name="peers" value="9"/>
    </item>
    <item>
      <title>Some Show S01 Pack</title>
      <guid>https://tz.example.test/t/2</guid>
      <link>https://tz.example.test/dl/2</link>
      <category>100001</category>
      <torznab:attr name="seeders" value="3"/>
      <torznab:attr name="peers" value="4"/>
    </item>
  </channel>
</rss>`

// capsServerDriver wires a generic-torznab driver to an offline server that answers
// ?t=caps with capsBody (or capsStatus when it is not 200) and every other request with
// liveFeed, counting caps fetches and recording the last search URL.
func capsServerDriver(t *testing.T, capsBody string, capsStatus int, sawURL *string) (*driver, *atomic.Int64) {
	t.Helper()
	var capsHits atomic.Int64
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.URL.Query().Get("t") == "caps" {
			capsHits.Add(1)
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(capsStatus)
			_, _ = io.WriteString(w, capsBody)
			return
		}
		if sawURL != nil {
			*sawURL = r.URL.String()
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = io.WriteString(w, liveFeed)
	}))
	t.Cleanup(srv.Close)
	d, err := New(native.Params{
		Def:     genericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey, "apiPath": "/api"},
		Doer:    srv.Client(),
		BaseURL: srv.URL,
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver), &capsHits
}

// TestLiveCapsMapsRequestedChildCategory proves the REQUEST side of #643: with the live
// caps fetched, a requested child category (5040) resolves to an upstream tracker
// category and rides the outbound URL as cat=5040 — where the placeholder parent table
// resolved it to nothing and the request went out unfiltered.
func TestLiveCapsMapsRequestedChildCategory(t *testing.T) {
	t.Parallel()
	var sawURL string
	d, hits := capsServerDriver(t, liveCaps, stdhttp.StatusOK, &sawURL)

	caps := d.Capabilities()
	trackerCats := caps.MapTorznabCapsToTrackers([]int{5040})
	if !slices.Equal(trackerCats, []string{"5040"}) {
		t.Fatalf("MapTorznabCapsToTrackers([5040]) = %v, want [5040]", trackerCats)
	}
	if hits.Load() != 1 {
		t.Errorf("caps fetches = %d, want 1 (lazy on first Capabilities)", hits.Load())
	}

	// core.buildQuery hands the driver exactly these resolved tracker categories.
	if _, err := d.Search(context.Background(), search.Query{Mode: "tv-search", Categories: trackerCats}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(sawURL, "cat=5040") {
		t.Errorf("upstream search URL = %q, want cat=5040", redact(sawURL))
	}
	if !strings.Contains(sawURL, "t=tvsearch") {
		t.Errorf("upstream search URL = %q, want t=tvsearch", redact(sawURL))
	}
	if hits.Load() != 1 {
		t.Errorf("caps fetches after Search = %d, want still 1 (served from cache)", hits.Load())
	}
}

// TestLiveCapsMapsResultCategories proves the RESPONSE side of #643: a result item
// carrying a child id (5040) maps to Categories [5040], and one carrying a custom id
// (100001) maps through the fetched tree to the synthesised custom category — both were
// category-less before.
func TestLiveCapsMapsResultCategories(t *testing.T) {
	t.Parallel()
	d, _ := capsServerDriver(t, liveCaps, stdhttp.StatusOK, nil)

	releases, err := d.Search(context.Background(), search.Query{Mode: "tv-search"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases = %d, want 2", len(releases))
	}
	if got := releases[0].Categories; !slices.Contains(got, 5040) {
		t.Errorf("item category 5040 -> %v, want it to include 5040", got)
	}
	custom := releases[1].Categories
	if len(custom) == 0 {
		t.Fatalf("item category 100001 -> %v, want the custom category from the fetched tree", custom)
	}
	if !slices.ContainsFunc(custom, func(id int) bool { return id >= mapper.CustomCategoryOffset }) {
		t.Errorf("item category 100001 -> %v, want a synthesised custom id >= %d", custom, mapper.CustomCategoryOffset)
	}
}

// TestLiveCapsFallsBackToPlaceholder proves a failing caps fetch leaves today's
// behaviour exactly as it was: Capabilities() serves the placeholder parent table and a
// search still runs and parses, mapping the standard parent ids it always did.
func TestLiveCapsFallsBackToPlaceholder(t *testing.T) {
	t.Parallel()
	d, _ := capsServerDriver(t, "boom", stdhttp.StatusInternalServerError, nil)

	caps := d.Capabilities()
	if caps == nil {
		t.Fatal("Capabilities() = nil on a caps-fetch failure, want the placeholder fallback")
		return // unreachable after Fatal; makes the non-nil guarantee explicit for staticcheck
	}
	for _, id := range []int{1000, 2000, 5000, 8000} {
		if got := caps.CategoryMap.MapTrackerCatToNewznab(strconv.Itoa(id)); !slices.Contains(got, id) {
			t.Errorf("placeholder fallback: %d -> %v, want it to include %d", id, got, id)
		}
	}
	if _, err := d.Search(context.Background(), search.Query{Mode: "tv-search"}); err != nil {
		t.Fatalf("Search with unavailable caps = %v, want it to still run", err)
	}
}

// TestLiveCapsFetchCarriesApikeyAndNeverLeaks proves the caps request authenticates with
// the configured apikey and that the URL redacts it everywhere it could be surfaced.
func TestLiveCapsFetchCarriesApikeyAndNeverLeaks(t *testing.T) {
	t.Parallel()
	var sawCapsURL string
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		sawCapsURL = r.URL.String()
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, liveCaps)
	}))
	t.Cleanup(srv.Close)
	d, err := New(native.Params{
		Def:     genericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey, "apiPath": "/api"},
		Doer:    srv.Client(),
		BaseURL: srv.URL,
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.Capabilities()
	if !strings.Contains(sawCapsURL, "t=caps") || !strings.Contains(sawCapsURL, "apikey="+testAPIKey) {
		t.Fatalf("caps request = %q, want t=caps and the apikey", redact(sawCapsURL))
	}
	assertNoAPIKey(t, "redacted caps URL", redact(srv.URL+sawCapsURL))
}

// TestLiveCapsPersistAndRehydrate proves the fetched caps XML + fetched-at are written
// back through PersistSetting and that a fresh driver built from those persisted
// settings maps the child id with NO network fetch (the cross-restart path the newznab
// sibling already had).
func TestLiveCapsPersistAndRehydrate(t *testing.T) {
	t.Parallel()
	stored := map[string]string{}
	var hits atomic.Int64
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.URL.Query().Get("t") == "caps" {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, liveCaps)
	}))
	t.Cleanup(srv.Close)

	params := native.Params{
		Def:     genericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey, "apiPath": "/api"},
		Doer:    srv.Client(),
		BaseURL: srv.URL,
		Clock:   fixedClock,
		PersistSetting: func(_ context.Context, name, value string) error {
			stored[name] = value
			return nil
		},
	}
	d1, err := New(params)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d1.Capabilities()
	if stored[native.SettingCapsCache] == "" || stored[native.SettingCapsFetchedAt] == "" {
		t.Fatalf("persist did not store the caps cache: %+v", stored)
	}

	hits.Store(0)
	d2, err := New(native.Params{
		Def: genericDefinition(),
		Cfg: map[string]string{
			"apikey":                    testAPIKey,
			"apiPath":                   "/api",
			native.SettingCapsCache:     stored[native.SettingCapsCache],
			native.SettingCapsFetchedAt: stored[native.SettingCapsFetchedAt],
		},
		Doer:    srv.Client(),
		BaseURL: srv.URL,
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New (rehydrate): %v", err)
	}
	if got := d2.Capabilities().MapTorznabCapsToTrackers([]int{5040}); !slices.Equal(got, []string{"5040"}) {
		t.Errorf("rehydrated MapTorznabCapsToTrackers([5040]) = %v, want [5040]", got)
	}
	if hits.Load() != 0 {
		t.Errorf("rehydrated driver fetched caps %d times, want 0 (served from the persisted cache)", hits.Load())
	}
}
