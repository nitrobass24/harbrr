package appsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/apps"
	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// syncFixture wires a real in-memory DB + auth service + apps service + plaintext
// keyring against an httptest Sonarr stub, so Sync exercises the real driver,
// reconciler, and ledger.
type syncFixture struct {
	svc    *Service
	db     *database.DB
	auth   *auth.Service
	apps   *apps.Service
	source *fakeSource
	stub   *servarrStub
	conn   domain.AppConnection
}

type fakeSource struct {
	instances []domain.IndexerInstance
	cats      map[string][]Category
	caps      map[string][]string
}

func (f *fakeSource) List(context.Context) ([]domain.IndexerInstance, error) {
	return f.instances, nil
}

func (f *fakeSource) Categories(_ context.Context, slug string) ([]Category, error) {
	return f.cats[slug], nil
}

func (f *fakeSource) Capabilities(_ context.Context, slug string) ([]string, error) {
	return f.caps[slug], nil
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{DataDir: t.TempDir(), AllowPlaintext: true}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}

	// Seed two instances so the ledger FK (instance_id) resolves; b is disabled.
	idA := seedInstance(t, db, "tracker-a", "Tracker A", true)
	idB := seedInstance(t, db, "tracker-b", "Tracker B", false)
	source := &fakeSource{
		instances: []domain.IndexerInstance{
			{
				ID: idA, Slug: "tracker-a", Name: "Tracker A", Enabled: true,
				EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
			},
			{
				ID: idB, Slug: "tracker-b", Name: "Tracker B", Enabled: false,
				EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
			},
		},
		// Both carry a TV category so the Sonarr fixture connection accepts them (the
		// content-category filter would otherwise exclude a movie-only indexer).
		cats: map[string][]Category{
			"tracker-a": {{ID: 5000, Name: "TV"}},
			"tracker-b": {{ID: 5030, Name: "TV/HD"}},
		},
	}

	stub := newServarrStub(t)
	srv := httptest.NewServer(stub.handler("/api/v3/indexer"))
	t.Cleanup(srv.Close)

	authSvc := auth.NewService(db)
	appsSvc := apps.NewService(db, kr, srv.Client(), zerolog.Nop())
	svc := NewService(db, source, appsSvc, authSvc, kr, srv.Client(), zerolog.Nop())

	conn, err := svc.CreateConnection(ctx, CreateConnectionParams{
		Name: "Sonarr", Kind: domain.AppKindSonarr, BaseURL: srv.URL,
		APIKey: "app-key", HarbrrURL: "http://harbrr:8787",
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	return &syncFixture{svc: svc, db: db, auth: authSvc, apps: appsSvc, source: source, stub: stub, conn: conn}
}

func TestBuildDesiredQuiSkipsUsenet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := &fakeSource{
		instances: []domain.IndexerInstance{
			{ID: 1, Slug: "torrent-tracker", Name: "Torrent", Enabled: true, Protocol: "torrent"},
			{ID: 2, Slug: "usenet-tracker", Name: "Usenet", Enabled: true, Protocol: "usenet"},
		},
		// TV categories so the Sonarr connection's content-category filter accepts both.
		cats: map[string][]Category{
			"torrent-tracker": {{ID: 5000, Name: "TV"}},
			"usenet-tracker":  {{ID: 5000, Name: "TV"}},
		},
	}
	svc := &Service{source: src}

	// qui is torrent-only: the usenet instance must be filtered out of the desired set.
	qui := domain.AppConnection{Kind: domain.AppKindQui, HarbrrURL: "http://harbrr"}
	got, err := svc.buildDesired(ctx, src.instances, qui, "k", nil)
	if err != nil {
		t.Fatalf("buildDesired qui: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "torrent-tracker" {
		t.Fatalf("qui desired = %+v, want only torrent-tracker", got)
	}

	// Sonarr keeps both and carries each instance's protocol through to DesiredIndexer.
	sonarr := domain.AppConnection{Kind: domain.AppKindSonarr, HarbrrURL: "http://harbrr"}
	got, err = svc.buildDesired(ctx, src.instances, sonarr, "k", nil)
	if err != nil {
		t.Fatalf("buildDesired sonarr: %v", err)
	}
	byProto := map[string]string{}
	for _, d := range got {
		byProto[d.Slug] = d.Protocol
	}
	if byProto["torrent-tracker"] != "torrent" || byProto["usenet-tracker"] != "usenet" {
		t.Fatalf("sonarr desired protocols = %+v, want torrent/usenet preserved", byProto)
	}
}

// TestBuildDesiredNoRePushOnUpgrade is the upgrade-safety proof for #365's reshape: a
// profile-less connection over a DEFAULT-shaped instance (toggles on, no sync
// categories, no min-seeders floor — exactly what a pre-365 row's migrated/defaulted
// shape looks like) must produce a DesiredIndexer whose hash() is bit-for-bit the
// PayloadHash a pre-365 build would have produced for the same instance with no
// profile. hash() itself is untouched by #365 (see target.go); this test proves the new
// per-instance default resolution path (buildDesired → resolveToggles/gateCategories
// reading the instance instead of a profile) still lands on that exact fingerprint, so
// upgrading harbrr never triggers a fleet-wide re-push.
func TestBuildDesiredNoRePushOnUpgrade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := &fakeSource{
		instances: []domain.IndexerInstance{{
			ID: 1, Slug: "trk", Name: "Trk", Enabled: true, Protocol: "torrent", Priority: 25,
			// The post-migration defaults for a row that never had a profile: toggles on,
			// no sync-category narrowing, no min-seeders floor.
			EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
		}},
		cats: map[string][]Category{"trk": {{ID: 5000, Name: "TV"}, {ID: 2000, Name: "Movies"}}},
	}
	svc := &Service{source: src}
	conn := domain.AppConnection{Kind: domain.AppKindQui, HarbrrURL: "http://h"}
	got, err := svc.buildDesired(ctx, src.instances, conn, "k", nil)
	if err != nil {
		t.Fatalf("buildDesired: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("buildDesired = %d results, want 1", len(got))
	}
	d := got[0]
	d.FeedURL = "http://h/api/indexers/trk/results/torznab" // pin the feed URL for the hand-built comparison below

	// The pre-#365 fingerprint formula (mirrors TestHashProfileFieldsBackwardCompat),
	// hand-built from the same field values a pre-365 profile-less DesiredIndexer had.
	cats := d.CategoryIDs()
	sort.Ints(cats)
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%v\x00%v\x00%d\x00%t", d.Name, d.FeedURL, cats, []string{}, d.Priority, d.Enabled)
	want := hex.EncodeToString(h.Sum(nil))
	if got := d.hash(); got != want {
		t.Errorf("buildDesired's profile-less/default-instance hash diverged from the pre-#365 fingerprint:\n got %s\nwant %s", got, want)
	}
}

func TestAppCategoryRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind   string
		lo, hi int
		ok     bool
	}{
		{domain.AppKindRadarr, 2000, 2999, true},
		{domain.AppKindLidarr, 3000, 3999, true},
		{domain.AppKindSonarr, 5000, 5999, true},
		{domain.AppKindWhisparr, 6000, 6999, true},
		{domain.AppKindReadarr, 7000, 7999, true},
		{domain.AppKindQui, 0, 0, false},
		{"nope", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			t.Parallel()
			lo, hi, ok := AppCategoryRange(tt.kind)
			if lo != tt.lo || hi != tt.hi || ok != tt.ok {
				t.Errorf("AppCategoryRange(%q) = (%d, %d, %t), want (%d, %d, %t)", tt.kind, lo, hi, ok, tt.lo, tt.hi, tt.ok)
			}
		})
	}
}

func TestIndexerServesApp(t *testing.T) {
	t.Parallel()
	cats := func(ids ...int) []Category {
		out := make([]Category, 0, len(ids))
		for _, id := range ids {
			out = append(out, Category{ID: id})
		}
		return out
	}
	tests := []struct {
		name string
		kind string
		cats []Category
		want bool
	}{
		{"mam sonarr", domain.AppKindSonarr, cats(3000, 3030, 7000, 7040, 100013), false},
		{"mam radarr", domain.AppKindRadarr, cats(3000, 3030, 7000, 7040, 100013), false},
		{"mam lidarr", domain.AppKindLidarr, cats(3000, 3030, 7000, 7040, 100013), true},
		{"mam readarr", domain.AppKindReadarr, cats(3000, 3030, 7000, 7040, 100013), true},
		{"mam whisparr", domain.AppKindWhisparr, cats(3000, 3030, 7000, 7040, 100013), false},
		{"mam qui", domain.AppKindQui, cats(3000, 3030, 7000, 7040, 100013), true},

		{"movie-only radarr", domain.AppKindRadarr, cats(2000, 2040), true},
		{"movie-only sonarr", domain.AppKindSonarr, cats(2000, 2040), false},
		{"movie-only qui", domain.AppKindQui, cats(2000, 2040), true},

		{"tv+movie radarr", domain.AppKindRadarr, cats(2000, 5000), true},
		{"tv+movie sonarr", domain.AppKindSonarr, cats(2000, 5000), true},
		{"tv+movie lidarr", domain.AppKindLidarr, cats(2000, 5000), false},
		{"tv+movie qui", domain.AppKindQui, cats(2000, 5000), true},

		// Audiobook-only (3030, outside the 7000s Books range): Prowlarr syncs it to
		// both Lidarr and Readarr, so both must accept it; Sonarr/Radarr must not.
		{"audiobook-only readarr", domain.AppKindReadarr, cats(3030), true},
		{"audiobook-only lidarr", domain.AppKindLidarr, cats(3030), true},
		{"audiobook-only sonarr", domain.AppKindSonarr, cats(3030), false},
		{"audiobook-only radarr", domain.AppKindRadarr, cats(3030), false},

		{"custom-only radarr", domain.AppKindRadarr, cats(100013), false},
		{"custom-only sonarr", domain.AppKindSonarr, cats(100013), false},
		{"custom-only lidarr", domain.AppKindLidarr, cats(100013), false},
		{"custom-only readarr", domain.AppKindReadarr, cats(100013), false},
		{"custom-only whisparr", domain.AppKindWhisparr, cats(100013), false},
		{"custom-only qui", domain.AppKindQui, cats(100013), true},

		{"empty radarr", domain.AppKindRadarr, cats(), false},
		{"empty sonarr", domain.AppKindSonarr, cats(), false},
		{"empty lidarr", domain.AppKindLidarr, cats(), false},
		{"empty qui", domain.AppKindQui, cats(), true},

		{"boundary 2999 radarr", domain.AppKindRadarr, cats(2999), true},
		{"boundary 3000 radarr", domain.AppKindRadarr, cats(3000), false},
		{"boundary 3000 lidarr", domain.AppKindLidarr, cats(3000), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IndexerServesApp(tt.kind, tt.cats); got != tt.want {
				t.Errorf("IndexerServesApp(%q, %v) = %t, want %t", tt.kind, tt.cats, got, tt.want)
			}
		})
	}
}

// TestBuildDesiredContentCategoryFilter checks the per-app content-category gate in
// buildDesired: a Servarr connection only receives indexers with a category in its
// Newznab range, while qui (content-neutral) receives all of them.
func TestBuildDesiredContentCategoryFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := &fakeSource{
		instances: []domain.IndexerInstance{
			{ID: 1, Slug: "mam", Name: "MAM", Enabled: true, Protocol: "torrent"},
			{ID: 2, Slug: "movie", Name: "Movie", Enabled: true, Protocol: "torrent"},
			{ID: 3, Slug: "tv", Name: "TV", Enabled: true, Protocol: "torrent"},
		},
		cats: map[string][]Category{
			"mam":   {{ID: 3000, Name: "Audio"}, {ID: 7000, Name: "Books"}},
			"movie": {{ID: 2000, Name: "Movies"}},
			"tv":    {{ID: 5000, Name: "TV"}},
		},
	}
	svc := &Service{source: src}

	tests := []struct {
		kind string
		want []string
	}{
		{domain.AppKindRadarr, []string{"movie"}},
		{domain.AppKindSonarr, []string{"tv"}},
		{domain.AppKindLidarr, []string{"mam"}},
		{domain.AppKindReadarr, []string{"mam"}},
		{domain.AppKindQui, []string{"mam", "movie", "tv"}},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			t.Parallel()
			conn := domain.AppConnection{Kind: tt.kind, HarbrrURL: "http://harbrr"}
			got, err := svc.buildDesired(ctx, src.instances, conn, "k", nil)
			if err != nil {
				t.Fatalf("buildDesired %s: %v", tt.kind, err)
			}
			slugs := make([]string, 0, len(got))
			for _, d := range got {
				slugs = append(slugs, d.Slug)
			}
			if !equalStringSet(slugs, tt.want) {
				t.Errorf("%s desired = %v, want %v", tt.kind, slugs, tt.want)
			}
		})
	}
}

func equalStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]bool, len(got))
	for _, s := range got {
		seen[s] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}

// TestBuildDesiredSyncCategoriesGate covers the narrow-only category gate: an
// instance's own sync categories narrow within the app's content type (block-parent and
// exact matches), the intersection is what gets pushed (names preserved), an empty
// intersection skips the indexer, sync categories can never cross content types, and an
// empty sync-categories set falls back to the plain kind gate.
func TestBuildDesiredSyncCategoriesGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	build := func(kind string, cats []Category, syncCats []int) ([]DesiredIndexer, error) {
		src := &fakeSource{
			instances: []domain.IndexerInstance{{
				ID: 1, Slug: "trk", Name: "Trk", Enabled: true, Protocol: "torrent",
				SyncCategories: syncCats, EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
			}},
			cats: map[string][]Category{"trk": cats},
		}
		svc := &Service{source: src}
		conn := domain.AppConnection{Kind: kind, HarbrrURL: "http://harbrr"}
		return svc.buildDesired(ctx, src.instances, conn, "k", nil)
	}

	tests := []struct {
		name       string
		kind       string
		cats       []Category
		syncCats   []int
		wantPushed bool
		wantCatIDs []int
	}{
		{
			name: "narrow-only: books tracker + books sync-categories on sonarr still skipped",
			kind: domain.AppKindSonarr, cats: []Category{{7000, "Books"}}, syncCats: []int{7000},
			wantPushed: false,
		},
		{
			name: "music sync-categories excludes audiobook-only lidarr tracker",
			kind: domain.AppKindLidarr, cats: []Category{{3030, "Audiobook"}}, syncCats: []int{3010, 3040},
			wantPushed: false,
		},
		{
			name: "block parent 2000 covers child 2040 on radarr",
			kind: domain.AppKindRadarr, cats: []Category{{2040, "Movies/HD"}}, syncCats: []int{2000},
			wantPushed: true, wantCatIDs: []int{2040},
		},
		{
			name: "exact 3030 matches only 3030 on lidarr",
			kind: domain.AppKindLidarr, cats: []Category{{3030, "Audiobook"}, {3010, "MP3"}}, syncCats: []int{3030},
			wantPushed: true, wantCatIDs: []int{3030},
		},
		{
			name: "intersection pushes only matched cats",
			kind: domain.AppKindSonarr, cats: []Category{{5000, "TV"}, {5040, "TV/HD"}, {5070, "TV/Anime"}}, syncCats: []int{5070},
			wantPushed: true, wantCatIDs: []int{5070},
		},
		{
			name: "empty sync categories falls back to kind gate (readarr audiobook 3030)",
			kind: domain.AppKindReadarr, cats: []Category{{3030, "Audiobook"}}, syncCats: nil,
			wantPushed: true, wantCatIDs: []int{3030},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := build(tt.kind, tt.cats, tt.syncCats)
			if err != nil {
				t.Fatalf("buildDesired: %v", err)
			}
			if !tt.wantPushed {
				if len(got) != 0 {
					t.Fatalf("want skipped, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one desired, got %d: %+v", len(got), got)
			}
			if !slices.Equal(got[0].CategoryIDs(), tt.wantCatIDs) {
				t.Errorf("pushed cats = %v, want %v", got[0].CategoryIDs(), tt.wantCatIDs)
			}
			for _, c := range got[0].Categories {
				if c.Name == "" {
					t.Errorf("pushed category %d lost its name", c.ID)
				}
			}
		})
	}
}

// TestBuildDesiredInstanceTogglesAndMinSeeders proves resolveToggles (Enabled AND the
// instance's own toggle, instance-disabled forcing all false) and that MinSeeders is
// carried straight from the instance (#365 moved both per-indexer).
func TestBuildDesiredInstanceTogglesAndMinSeeders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := &fakeSource{
		instances: []domain.IndexerInstance{
			{
				ID: 1, Slug: "on", Name: "On", Enabled: true, Protocol: "torrent", MinSeeders: 4,
				EnableRss: false, EnableAutomaticSearch: true, EnableInteractiveSearch: false,
			},
			{
				ID: 2, Slug: "off", Name: "Off", Enabled: false, Protocol: "torrent",
				EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
			},
		},
		cats: map[string][]Category{"on": {{5000, "TV"}}, "off": {{5000, "TV"}}},
	}
	svc := &Service{source: src}
	conn := domain.AppConnection{Kind: domain.AppKindSonarr, HarbrrURL: "http://harbrr"}
	got, err := svc.buildDesired(ctx, src.instances, conn, "k", nil)
	if err != nil {
		t.Fatalf("buildDesired: %v", err)
	}
	byslug := map[string]DesiredIndexer{}
	for _, d := range got {
		byslug[d.Slug] = d
	}
	if on := byslug["on"]; on.EnableRss || !on.EnableAutomaticSearch || on.EnableInteractiveSearch || on.MinSeeders != 4 {
		t.Errorf("enabled instance = rss %v auto %v interactive %v minSeeders %d, want false/true/false/4",
			on.EnableRss, on.EnableAutomaticSearch, on.EnableInteractiveSearch, on.MinSeeders)
	}
	// Disabled instance: even with every toggle on, Enabled forces the pushed flags false.
	if off := byslug["off"]; off.EnableRss || off.EnableAutomaticSearch || off.EnableInteractiveSearch {
		t.Errorf("disabled instance toggles must all be false, got rss %v auto %v interactive %v",
			off.EnableRss, off.EnableAutomaticSearch, off.EnableInteractiveSearch)
	}
}

// TestBuildDesiredProfileSelectionFilters proves buildDesired's routing gate: a nil
// profile keeps every instance; a profile with an empty IndexerIDs set ALSO keeps every
// instance (mirrors the empty-categories convention); a non-empty set keeps only the
// selected subset.
func TestBuildDesiredProfileSelectionFilters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := &fakeSource{
		instances: []domain.IndexerInstance{
			{ID: 1, Slug: "a", Name: "A", Enabled: true, Protocol: "torrent"},
			{ID: 2, Slug: "b", Name: "B", Enabled: true, Protocol: "torrent"},
		},
		cats: map[string][]Category{"a": {{5000, "TV"}}, "b": {{5000, "TV"}}},
	}
	svc := &Service{source: src}
	conn := domain.AppConnection{Kind: domain.AppKindSonarr, HarbrrURL: "http://harbrr"}

	tests := []struct {
		name    string
		profile *domain.SyncProfile
		want    []string
	}{
		{"nil profile keeps all", nil, []string{"a", "b"}},
		{"empty selection keeps all", &domain.SyncProfile{IndexerIDs: nil}, []string{"a", "b"}},
		{"subset keeps only selected", &domain.SyncProfile{IndexerIDs: []int64{1}}, []string{"a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := svc.buildDesired(ctx, src.instances, conn, "k", tt.profile)
			if err != nil {
				t.Fatalf("buildDesired: %v", err)
			}
			slugs := make([]string, 0, len(got))
			for _, d := range got {
				slugs = append(slugs, d.Slug)
			}
			if !equalStringSet(slugs, tt.want) {
				t.Errorf("desired = %v, want %v", slugs, tt.want)
			}
		})
	}
}

// TestBuildDesiredUsesInstancePriority proves each pushed DesiredIndexer carries its own
// instance's priority (Prowlarr semantics: set per indexer, not per connection) — the
// connection no longer has a priority field to stamp onto every row.
func TestBuildDesiredUsesInstancePriority(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := &fakeSource{
		instances: []domain.IndexerInstance{
			{ID: 1, Slug: "high", Name: "High", Enabled: true, Protocol: "torrent", Priority: 1},
			{ID: 2, Slug: "low", Name: "Low", Enabled: true, Protocol: "torrent", Priority: 50},
		},
		cats: map[string][]Category{"high": {{5000, "TV"}}, "low": {{5000, "TV"}}},
	}
	svc := &Service{source: src}
	conn := domain.AppConnection{Kind: domain.AppKindSonarr, HarbrrURL: "http://harbrr"}
	got, err := svc.buildDesired(ctx, src.instances, conn, "k", nil)
	if err != nil {
		t.Fatalf("buildDesired: %v", err)
	}
	byslug := map[string]DesiredIndexer{}
	for _, d := range got {
		byslug[d.Slug] = d
	}
	if byslug["high"].Priority != 1 {
		t.Errorf("high priority = %d, want 1", byslug["high"].Priority)
	}
	if byslug["low"].Priority != 50 {
		t.Errorf("low priority = %d, want 50", byslug["low"].Priority)
	}
}

// TestServiceSyncWithProfile is the end-to-end proof of the routing-set model (#365):
// creating a profile that selects only tracker-a, assigning it to the fixture
// connection, and syncing pushes exactly that one instance — tracker-b (excluded by
// the profile's selection) is never pushed at all, not merely toggled off.
func TestServiceSyncWithProfile(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	instA := f.source.instances[0].ID
	prof, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "just-a", IndexerIDs: []int64{instA}})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{
		SyncProfileID: RefUpdate{Present: true, Value: &prof.ID},
	}); err != nil {
		t.Fatalf("assign profile: %v", err)
	}
	rep, err := f.svc.Sync(ctx, f.conn.ID)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(rep.Results) != 1 || rep.Results[0].Slug != "tracker-a" {
		t.Fatalf("sync results = %+v, want exactly tracker-a", rep.Results)
	}
	if f.stub.byName("Tracker A") == nil {
		t.Fatal("Tracker A not pushed")
	}
	if f.stub.byName("Tracker B") != nil {
		t.Fatal("Tracker B pushed despite being excluded by the profile's selection")
	}

	// Clearing the selection (present-empty) reverts to every compatible indexer.
	empty := []int64{}
	if err := f.svc.UpdateProfile(ctx, prof.ID, UpdateProfileParams{IndexerIDs: &empty}); err != nil {
		t.Fatalf("clear profile selection: %v", err)
	}
	rep, err = f.svc.Sync(ctx, f.conn.ID)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if len(rep.Results) != 2 {
		t.Fatalf("sync results after clearing selection = %+v, want both trackers", rep.Results)
	}
}

// TestServiceSyncQuiWithProfile proves a qui connection may reference a routing
// profile (#365 dropped the pre-existing hard rejection of profiles for qui).
func TestServiceSyncQuiWithProfile(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	instA := f.source.instances[0].ID
	prof, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "qui-set", IndexerIDs: []int64{instA}})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	quiSrv := httptest.NewServer(newQuiStub().handler())
	t.Cleanup(quiSrv.Close)
	conn, err := f.svc.CreateConnection(ctx, CreateConnectionParams{
		Name: "qui", Kind: domain.AppKindQui, BaseURL: quiSrv.URL, APIKey: "k",
		HarbrrURL: "http://harbrr:8787", SyncProfileID: &prof.ID,
	})
	if err != nil {
		t.Fatalf("CreateConnection (qui with profile): %v", err)
	}
	if conn.SyncProfileID == nil || *conn.SyncProfileID != prof.ID {
		t.Fatalf("qui connection SyncProfileID = %v, want %d", conn.SyncProfileID, prof.ID)
	}
	if _, err := f.svc.Sync(ctx, conn.ID); err != nil {
		t.Fatalf("Sync qui with profile: %v", err)
	}
}

func seedInstance(t *testing.T, db *database.DB, slug, name string, enabled bool) int64 {
	t.Helper()
	now := time.Now().UTC()
	id, err := (database.Instances{}).Insert(context.Background(), db, domain.IndexerInstance{
		Slug: slug, DefinitionID: "def", Name: name, Enabled: enabled, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed instance %q: %v", slug, err)
	}
	return id
}

func TestServiceCreateMintsKeyAndEncrypts(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	keys, _ := f.auth.ListAPIKeys(ctx)
	if len(keys) != 1 {
		t.Fatalf("want one minted key, got %d", len(keys))
	}
	if f.conn.HarbrrAPIKeyID != keys[0].ID {
		t.Errorf("connection key id = %d, want %d", f.conn.HarbrrAPIKeyID, keys[0].ID)
	}
	if f.conn.SyncLevel != domain.SyncLevelFull {
		t.Errorf("defaults not applied: %+v", f.conn)
	}
}

func TestServiceSyncCreatesThenNoop(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	rep, err := f.svc.Sync(ctx, f.conn.ID)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if rep.Status != domain.SyncStatusOK || len(rep.Results) != 2 {
		t.Fatalf("first sync = %+v, want ok with 2 results", rep)
	}
	if f.stub.created() != 2 {
		t.Errorf("stub has %d indexers, want 2", f.stub.created())
	}
	// The disabled instance is pushed inactive, not skipped.
	if got := f.stub.byName("Tracker B"); got == nil || got.EnableRss {
		t.Errorf("disabled instance should be pushed with enableRss=false: %+v", got)
	}
	// The pushed feed URL carries the connection's harbrr URL + slug.
	if got := f.stub.byName("Tracker A"); got == nil || !strings.Contains(fieldString(got.Fields, "baseUrl"), "/indexers/tracker-a/results/torznab") {
		t.Errorf("feed URL projection wrong: %+v", got)
	}

	ledger, _ := f.svc.ConnectionIndexers(ctx, f.conn.ID)
	if len(ledger) != 2 || ledger[0].RemoteID == "" {
		t.Fatalf("ledger = %+v, want 2 rows with remote ids", ledger)
	}

	second, err := f.svc.Sync(ctx, f.conn.ID)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	for _, r := range second.Results {
		if r.Action != ActionNoop {
			t.Errorf("re-sync %q = %q, want noop", r.Slug, r.Action)
		}
	}
}

func TestServiceSyncPrunesOrphanInFull(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	if _, err := f.svc.Sync(ctx, f.conn.ID); err != nil {
		t.Fatalf("initial Sync: %v", err)
	}
	// Remove tracker-b from harbrr exactly as deleting an indexer does: drop the DB row
	// (its ledger row cascades) and the source list. A full sync must then prune the
	// now-orphaned remote indexer in the app.
	if err := (database.Instances{}).Delete(ctx, f.db, "tracker-b"); err != nil {
		t.Fatalf("delete instance: %v", err)
	}
	f.source.instances = f.source.instances[:1]
	rep, err := f.svc.Sync(ctx, f.conn.ID)
	if err != nil {
		t.Fatalf("prune Sync: %v", err)
	}
	if !hasAction(rep.Results, "tracker-b", ActionDeleted) {
		t.Errorf("tracker-b should be deleted: %+v", rep.Results)
	}
	if f.stub.created() != 1 {
		t.Errorf("stub has %d indexers after prune, want 1", f.stub.created())
	}
	if ledger, _ := f.svc.ConnectionIndexers(ctx, f.conn.ID); len(ledger) != 1 {
		t.Errorf("ledger has %d rows after prune, want 1", len(ledger))
	}
}

func TestServiceSyncAddUpdateNeverPrunes(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{SyncLevel: ptr(domain.SyncLevelAddUpdate)}); err != nil {
		t.Fatalf("UpdateConnection: %v", err)
	}
	if _, err := f.svc.Sync(ctx, f.conn.ID); err != nil {
		t.Fatalf("initial Sync: %v", err)
	}
	f.source.instances = f.source.instances[:1]
	if _, err := f.svc.Sync(ctx, f.conn.ID); err != nil {
		t.Fatalf("add_update Sync: %v", err)
	}
	if f.stub.created() != 2 {
		t.Errorf("add_update must not prune: stub has %d indexers, want 2", f.stub.created())
	}
}

func TestServiceSyncSkipsDisabledConnection(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	if err := f.svc.SetEnabled(ctx, f.conn.ID, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	rep, err := f.svc.Sync(ctx, f.conn.ID)
	if err != nil {
		t.Fatalf("Sync disabled: %v", err)
	}
	if rep.Status != StatusSkipped || f.stub.created() != 0 {
		t.Errorf("disabled connection should skip: status=%q created=%d", rep.Status, f.stub.created())
	}
}

func TestServiceTestConnection(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	if err := f.svc.TestConnection(context.Background(), f.conn.ID); err != nil {
		t.Errorf("TestConnection against a healthy stub = %v, want nil", err)
	}
}

func TestServiceDeleteRevokesKey(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	if err := f.svc.DeleteConnection(ctx, f.conn.ID); err != nil {
		t.Fatalf("DeleteConnection: %v", err)
	}
	if keys, _ := f.auth.ListAPIKeys(ctx); len(keys) != 0 {
		t.Errorf("minted key not revoked on delete: %d remain", len(keys))
	}
	if _, err := f.svc.GetConnection(ctx, f.conn.ID); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("connection still present after delete: %v", err)
	}
}

// failRevokeMinter mints real keys (so create reaches the persist step) but always
// fails RevokeAPIKey, exercising the fail-closed revoke paths.
type failRevokeMinter struct{ inner *auth.Service }

func (m failRevokeMinter) MintAPIKey(ctx context.Context, name string) (string, domain.APIKey, error) {
	return m.inner.MintAPIKey(ctx, name)
}

func (m failRevokeMinter) RevokeAPIKey(context.Context, int64) error {
	return errors.New("revoke boom")
}

// TestServiceCreateRevokeFailureFailsClosed: when persistence fails AND the orphan
// key cannot be revoked, the error surfaces the revoke failure (not a swallowed log)
// so the operator knows a live credential is dangling.
func TestServiceCreateRevokeFailureFailsClosed(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	f.svc.minter = failRevokeMinter{inner: f.auth}

	// A duplicate connection makes insertConnection fail (unique violation), so the
	// just-minted key is orphaned and the failing revoke must be surfaced.
	dup := CreateConnectionParams{
		Name: "dup", Kind: f.conn.Kind, BaseURL: f.conn.BaseURL, APIKey: "k", HarbrrURL: "http://harbrr:8787",
	}
	_, err := f.svc.CreateConnection(ctx, dup)
	if err == nil {
		t.Fatal("expected an error from a duplicate create with a failing revoke")
	}
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("error should still wrap domain.ErrConflict, got %v", err)
	}
	if !strings.Contains(err.Error(), "could not be revoked") {
		t.Errorf("error should surface the revoke failure, got %v", err)
	}
}

// TestServiceCreateInvalidProfileRefMintsNoKey: an ordinary invalid profile ref must
// fail before the key mint has side effects — the advisory pre-check runs against
// s.db ahead of MintAPIKey, so a plain client 400 never churns the api-keys table
// (the race-proof check inside insertConnection's transaction still backstops it).
// The failing minter proves the point: were the mint reached, the revoke failure
// would pollute the error.
func TestServiceCreateInvalidProfileRefMintsNoKey(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	f.svc.minter = failRevokeMinter{inner: f.auth}

	missing := int64(999999)
	_, err := f.svc.CreateConnection(ctx, CreateConnectionParams{
		Name: "bad-ref", Kind: domain.AppKindSonarr, BaseURL: "http://sonarr-bad-ref:8989",
		APIKey: "k", HarbrrURL: "http://harbrr:8787", SyncProfileID: &missing,
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("create with missing profile = %v, want domain.ErrInvalid", err)
	}
	if strings.Contains(err.Error(), "could not be revoked") {
		t.Errorf("a pure validation rejection reached the mint/revoke path: %v", err)
	}
	if keys, _ := f.auth.ListAPIKeys(ctx); len(keys) != 1 {
		t.Errorf("invalid create changed the key count: got %d, want 1 (the fixture's)", len(keys))
	}
}

// TestServiceDeleteRevokeFailureFailsClosed: a delete whose key revoke fails returns
// an error rather than swallowing it (the row is gone but the key still authorizes).
func TestServiceDeleteRevokeFailureFailsClosed(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	f.svc.minter = failRevokeMinter{inner: f.auth}

	err := f.svc.DeleteConnection(ctx, f.conn.ID)
	if err == nil || !strings.Contains(err.Error(), "could not be revoked") {
		t.Fatalf("delete with failing revoke = %v, want a surfaced revoke failure", err)
	}
}

// TestServiceCreateRejectsNonAbsoluteURL: BaseURL and HarbrrURL must be absolute
// http(s) URLs (parity with announce), so a relative/malformed value is a 400.
func TestServiceCreateRejectsNonAbsoluteURL(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	bad := []CreateConnectionParams{
		{Name: "n", Kind: domain.AppKindSonarr, BaseURL: "not-a-url", APIKey: "k", HarbrrURL: "http://harbrr:8787"},
		{Name: "n", Kind: domain.AppKindSonarr, BaseURL: "/relative", APIKey: "k", HarbrrURL: "http://harbrr:8787"},
		{Name: "n", Kind: domain.AppKindSonarr, BaseURL: "ftp://h", APIKey: "k", HarbrrURL: "http://harbrr:8787"},
		{Name: "n", Kind: domain.AppKindSonarr, BaseURL: "http://app:7878", APIKey: "k", HarbrrURL: "harbrr"},
		{Name: "n", Kind: domain.AppKindSonarr, BaseURL: "http://:80", APIKey: "k", HarbrrURL: "http://harbrr:8787"}, // empty host, port only
	}
	for i, p := range bad {
		if _, err := f.svc.CreateConnection(ctx, p); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("case %d: err = %v, want domain.ErrInvalid", i, err)
		}
	}
}

func TestServiceCreatePersistsTrimmedURL(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	// Whitespace-padded URLs pass validation (which trims to parse) and must be stored
	// in their trimmed form, not left padded. Radarr avoids the fixture's Sonarr row on
	// the UNIQUE(kind, base_url) constraint.
	conn, err := f.svc.CreateConnection(ctx, CreateConnectionParams{
		Name: "Radarr", Kind: domain.AppKindRadarr,
		BaseURL: "  http://radarr:7878  ", APIKey: "k", HarbrrURL: "\thttp://harbrr:8787\n",
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	if conn.BaseURL != "http://radarr:7878" {
		t.Errorf("BaseURL = %q, want the trimmed value", conn.BaseURL)
	}
	// HarbrrURL is App-level, enriched on read (not returned by Create itself).
	got, err := f.svc.GetConnection(ctx, conn.ID)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if got.HarbrrURL != "http://harbrr:8787" {
		t.Errorf("HarbrrURL = %q, want the trimmed value", got.HarbrrURL)
	}
}

// TestServiceProfileRoutingFunctional proves a routing profile end to end through
// Service.Sync: an empty selection pushes every compatible indexer, a subset pushes
// only its members, and re-syncing after widening the selection back to empty pushes
// everything again — the routing decision is recomputed from the profile every sync,
// not sticky ledger state (#365 dropped the old scope="selected" ledger flag entirely).
func TestServiceProfileRoutingFunctional(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	prof, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "routing"})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{
		SyncProfileID: RefUpdate{Present: true, Value: &prof.ID},
	}); err != nil {
		t.Fatalf("assign profile: %v", err)
	}

	// An empty selection (the profile's default) pushes every compatible indexer.
	rep, err := f.svc.Sync(ctx, f.conn.ID)
	if err != nil {
		t.Fatalf("empty-selection Sync: %v", err)
	}
	if len(rep.Results) != 2 || f.stub.created() != 2 {
		t.Fatalf("empty-selection sync = results %v created %d, want both trackers", rep.Results, f.stub.created())
	}

	// Narrow to tracker-a only; sync now keeps tracker-a and prunes tracker-b (the
	// default sync level is "full", so a no-longer-desired indexer is an orphan).
	instA := f.source.instances[0].ID
	if err := f.svc.UpdateProfile(ctx, prof.ID, UpdateProfileParams{IndexerIDs: &[]int64{instA}}); err != nil {
		t.Fatalf("narrow profile: %v", err)
	}
	rep, err = f.svc.Sync(ctx, f.conn.ID)
	if err != nil {
		t.Fatalf("narrowed Sync: %v", err)
	}
	if !hasAction(rep.Results, "tracker-a", ActionNoop) {
		t.Fatalf("narrowed sync should keep tracker-a: results=%v", rep.Results)
	}
	if !hasAction(rep.Results, "tracker-b", ActionDeleted) {
		t.Fatalf("narrowed sync should prune tracker-b (no longer routed): results=%v", rep.Results)
	}
	if f.stub.created() != 1 {
		t.Errorf("stub has %d indexers after narrowing, want 1 (tracker-b pruned)", f.stub.created())
	}
}

func TestServiceSyncStaleKeyGuard(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	// Revoke the minted key out of band (FK SET NULL nulls the connection's link).
	if err := (database.APIKeys{}).Delete(ctx, f.db, f.conn.HarbrrAPIKeyID); err != nil {
		t.Fatalf("revoke key: %v", err)
	}
	_, err := f.svc.Sync(ctx, f.conn.ID)
	if err == nil {
		t.Fatal("sync with a revoked harbrr key should error, not push a stale key")
	}
	if f.stub.created() != 0 {
		t.Errorf("stale-key sync pushed %d indexers, want 0", f.stub.created())
	}
}

func TestServiceSyncAllPartialFailureContinues(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	// A second connection whose minted key is revoked out of band (FK SET NULL): Sync
	// errors on the stale-key guard before any remote call, so its BaseURL host is never
	// reached (a dead host is fine — CreateConnection validates the URL, never probes it).
	bad, err := f.svc.CreateConnection(ctx, CreateConnectionParams{
		Name: "Sonarr2", Kind: domain.AppKindSonarr, BaseURL: "http://other:8989",
		APIKey: "app-key", HarbrrURL: "http://harbrr:8787",
	})
	if err != nil {
		t.Fatalf("CreateConnection bad: %v", err)
	}
	if err := (database.APIKeys{}).Delete(ctx, f.db, bad.HarbrrAPIKeyID); err != nil {
		t.Fatalf("revoke bad key: %v", err)
	}

	// A third, disabled connection — proves the all-not-enabled-only decision: it comes
	// back skipped (no remote call) rather than being silently omitted.
	off, err := f.svc.CreateConnection(ctx, CreateConnectionParams{
		Name: "Sonarr3", Kind: domain.AppKindSonarr, BaseURL: "http://paused:8989",
		APIKey: "app-key", HarbrrURL: "http://harbrr:8787",
	})
	if err != nil {
		t.Fatalf("CreateConnection off: %v", err)
	}
	if err := f.svc.SetEnabled(ctx, off.ID, false); err != nil {
		t.Fatalf("SetEnabled off: %v", err)
	}

	results, err := f.svc.SyncAll(ctx)
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("SyncAll returned %d results, want 3", len(results))
	}
	byID := make(map[int64]ConnectionSyncResult, len(results))
	for _, r := range results {
		byID[r.ConnectionID] = r
	}

	good := byID[f.conn.ID]
	if good.Error != "" || good.Report.Status != domain.SyncStatusOK || len(good.Report.Results) != 2 {
		t.Errorf("good conn = %+v, want ok status, 2 results, no error", good)
	}
	if got := byID[bad.ID]; got.Error == "" || got.Report.Status != "" {
		t.Errorf("bad conn = %+v, want scrubbed error and empty report", got)
	}
	if got := byID[off.ID]; got.Error != "" || got.Report.Status != StatusSkipped {
		t.Errorf("disabled conn = %+v, want skipped status, no error", got)
	}
	// The healthy connection reached the stub despite the sibling failure.
	if f.stub.created() != 2 {
		t.Errorf("stub has %d indexers, want 2 (healthy conn synced despite sibling failure)", f.stub.created())
	}
}

func TestServiceCreateValidation(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	bad := []CreateConnectionParams{
		{Kind: domain.AppKindSonarr, BaseURL: "x", APIKey: "k", HarbrrURL: "h"},                              // no name
		{Name: "n", Kind: "plex", BaseURL: "x", APIKey: "k", HarbrrURL: "h"},                                 // bad kind
		{Name: "n", Kind: domain.AppKindSonarr, APIKey: "k", HarbrrURL: "h"},                                 // no base url
		{Name: "n", Kind: domain.AppKindSonarr, BaseURL: "x", HarbrrURL: "h"},                                // no api key
		{Name: "n", Kind: domain.AppKindSonarr, BaseURL: "x", APIKey: "k"},                                   // no harbrr url
		{Name: "n", Kind: domain.AppKindSonarr, BaseURL: "x", APIKey: "k", HarbrrURL: "h", SyncLevel: "wat"}, // bad level
	}
	for i, p := range bad {
		if _, err := f.svc.CreateConnection(ctx, p); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("case %d: err = %v, want domain.ErrInvalid", i, err)
		}
	}
}

func TestServiceUpdateRejectsBlankFields(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	blank := " "
	cases := map[string]UpdateConnectionParams{
		"blank name": {Name: &blank},
	}
	for name, p := range cases {
		if err := f.svc.UpdateConnection(ctx, f.conn.ID, p); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: err = %v, want domain.ErrInvalid", name, err)
		}
	}
	// A non-blank patch still succeeds.
	ok := "Renamed"
	if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{Name: &ok}); err != nil {
		t.Errorf("valid update rejected: %v", err)
	}
}

// TestProfileIndexerIDsRejectsUnknownID proves CreateProfile/UpdateProfile route their
// IndexerIDs through validateInstanceIDs — the same guard SetSelectedIndexers used to
// own — turning an unknown instance id into a 400 rather than a repository FK error.
func TestProfileIndexerIDsRejectsUnknownID(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	if _, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "bad", IndexerIDs: []int64{99999}}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("unknown instance id err = %v, want domain.ErrInvalid", err)
	}
	// A known id is accepted.
	p, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "good", IndexerIDs: []int64{f.source.instances[0].ID}})
	if err != nil {
		t.Errorf("known id rejected: %v", err)
	}
	if err := f.svc.UpdateProfile(ctx, p.ID, UpdateProfileParams{IndexerIDs: &[]int64{99999}}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("update to unknown instance id err = %v, want domain.ErrInvalid", err)
	}
}

func TestServiceCreateDuplicateConflicts(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	dup := CreateConnectionParams{
		Name: "again", Kind: f.conn.Kind, BaseURL: f.conn.BaseURL, APIKey: "k", HarbrrURL: "http://harbrr:8787",
	}
	if _, err := f.svc.CreateConnection(ctx, dup); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate (kind, base_url) err = %v, want domain.ErrConflict", err)
	}
	// The conflicting create must not leak a minted key.
	if keys, _ := f.auth.ListAPIKeys(ctx); len(keys) != 1 {
		t.Errorf("orphan key leaked on conflict: %d keys, want 1", len(keys))
	}
}

// TestServiceCreateWithAppIDReusesApp proves the AppID (reuse) create path: given an
// App that already exists (as if another surface — a download client, say — created
// it), attaching an appsync connection to it by id mints no new App and needs no
// inline credential.
func TestServiceCreateWithAppIDReusesApp(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	app, err := f.apps.Resolve(ctx, apps.Ref{
		Kind: domain.AppKindRadarr, BaseURL: "http://shared-radarr:7878", APIKey: "k", HarbrrURL: "http://harbrr:8787",
	})
	if err != nil {
		t.Fatalf("Resolve (prime app): %v", err)
	}
	before, err := f.apps.List(ctx)
	if err != nil {
		t.Fatalf("List apps: %v", err)
	}

	conn, err := f.svc.CreateConnection(ctx, CreateConnectionParams{
		Name: "radarr-via-appid", Kind: domain.AppKindRadarr, AppID: &app.ID,
	})
	if err != nil {
		t.Fatalf("CreateConnection with AppID (no inline credential): %v", err)
	}
	if conn.AppID == nil || *conn.AppID != app.ID {
		t.Fatalf("connection AppID = %v, want %d", conn.AppID, app.ID)
	}
	if conn.BaseURL != app.BaseURL {
		t.Errorf("connection BaseURL = %q, want the App's %q", conn.BaseURL, app.BaseURL)
	}

	after, err := f.apps.List(ctx)
	if err != nil {
		t.Fatalf("List apps after: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("AppID create minted a new App: before=%d after=%d", len(before), len(after))
	}
}

// TestServiceConnectionsShareOneApp pins the App registry's core invariant: two
// connections identifying the same (kind, base_url) — the fixture's own appsync
// connection, and a second resolve standing in for another surface's create (announce,
// download) — share exactly one App row rather than minting a duplicate. (A second
// appsync connection at the literal same kind+base_url is separately rejected by
// app_connections' own UNIQUE(kind, base_url) — see TestServiceCreateDuplicateConflicts
// — so cross-surface sharing is exercised here via apps.Resolve directly.)
func TestServiceConnectionsShareOneApp(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	again, err := f.apps.Resolve(ctx, apps.Ref{Kind: domain.AppKindSonarr, BaseURL: f.conn.BaseURL})
	if err != nil {
		t.Fatalf("Resolve same identity: %v", err)
	}
	if f.conn.AppID == nil || again.ID != *f.conn.AppID {
		t.Errorf("second resolve minted a new App: got id %d, want %v (the fixture connection's App)", again.ID, f.conn.AppID)
	}
	all, err := f.apps.List(ctx)
	if err != nil {
		t.Fatalf("List apps: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("apps = %d, want exactly 1 (shared identity, no duplicate)", len(all))
	}
}

// TestServiceListGetEnrichFromApp proves ListConnections and GetConnection are the
// single read path for identity: both project BaseURL/HarbrrURL from the referenced
// App, not from the connection row's own (legacy) columns.
func TestServiceListGetEnrichFromApp(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	app, err := f.apps.Get(ctx, *f.conn.AppID)
	if err != nil {
		t.Fatalf("apps.Get: %v", err)
	}
	if app.HarbrrURL != "http://harbrr:8787" {
		t.Fatalf("fixture app HarbrrURL = %q, want http://harbrr:8787", app.HarbrrURL)
	}

	got, err := f.svc.GetConnection(ctx, f.conn.ID)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if got.BaseURL != app.BaseURL || got.HarbrrURL != app.HarbrrURL {
		t.Errorf("GetConnection identity = base %q harbrr %q, want the App's base %q harbrr %q",
			got.BaseURL, got.HarbrrURL, app.BaseURL, app.HarbrrURL)
	}

	list, err := f.svc.ListConnections(ctx)
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	var found bool
	for _, c := range list {
		if c.ID != f.conn.ID {
			continue
		}
		found = true
		if c.BaseURL != app.BaseURL || c.HarbrrURL != app.HarbrrURL {
			t.Errorf("ListConnections identity = base %q harbrr %q, want the App's base %q harbrr %q",
				c.BaseURL, c.HarbrrURL, app.BaseURL, app.HarbrrURL)
		}
	}
	if !found {
		t.Fatalf("ListConnections did not include the fixture connection")
	}
}

// --- stub helpers used only by service tests ---

func (s *servarrStub) created() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.indexers)
}

func (s *servarrStub) byName(name string) *servarrIndexer {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, idx := range s.indexers {
		if idx.Name == name {
			cp := idx
			return &cp
		}
	}
	return nil
}

func hasAction(results []SyncResult, slug, action string) bool {
	for _, r := range results {
		if r.Slug == slug && r.Action == action {
			return true
		}
	}
	return false
}

func ptr[T any](v T) *T { return &v }
