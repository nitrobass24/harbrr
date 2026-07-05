package appsync

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// syncFixture wires a real in-memory DB + auth service + plaintext keyring against an
// httptest Sonarr stub, so Sync exercises the real driver, reconciler, and ledger.
type syncFixture struct {
	svc    *Service
	db     *database.DB
	auth   *auth.Service
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
			{ID: idA, Slug: "tracker-a", Name: "Tracker A", Enabled: true},
			{ID: idB, Slug: "tracker-b", Name: "Tracker B", Enabled: false},
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
	svc := NewService(db, source, authSvc, kr, srv.Client(), zerolog.Nop())

	conn, err := svc.CreateConnection(ctx, CreateConnectionParams{
		Name: "Sonarr", Kind: domain.AppKindSonarr, BaseURL: srv.URL,
		APIKey: "app-key", HarbrrURL: "http://harbrr:8787",
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	return &syncFixture{svc: svc, db: db, auth: authSvc, source: source, stub: stub, conn: conn}
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
	qui := domain.AppConnection{Kind: domain.AppKindQui, IndexScope: domain.IndexScopeAll, HarbrrURL: "http://harbrr"}
	got, err := svc.buildDesired(ctx, src.instances, qui, "k", nil, nil)
	if err != nil {
		t.Fatalf("buildDesired qui: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "torrent-tracker" {
		t.Fatalf("qui desired = %+v, want only torrent-tracker", got)
	}

	// Sonarr keeps both and carries each instance's protocol through to DesiredIndexer.
	sonarr := domain.AppConnection{Kind: domain.AppKindSonarr, IndexScope: domain.IndexScopeAll, HarbrrURL: "http://harbrr"}
	got, err = svc.buildDesired(ctx, src.instances, sonarr, "k", nil, nil)
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
			conn := domain.AppConnection{Kind: tt.kind, IndexScope: domain.IndexScopeAll, HarbrrURL: "http://harbrr"}
			got, err := svc.buildDesired(ctx, src.instances, conn, "k", nil, nil)
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

// TestBuildDesiredWithProfile covers the narrow-only category gate: a profile narrows
// within the app's content type (block-parent and exact matches), the intersection is
// what gets pushed (names preserved), an empty intersection skips the indexer, a profile
// can never cross content types, and an empty-category profile falls back to the kind gate.
func TestBuildDesiredWithProfile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	build := func(kind string, cats []Category, profile *domain.SyncProfile) ([]DesiredIndexer, error) {
		src := &fakeSource{
			instances: []domain.IndexerInstance{{ID: 1, Slug: "trk", Name: "Trk", Enabled: true, Protocol: "torrent"}},
			cats:      map[string][]Category{"trk": cats},
		}
		svc := &Service{source: src}
		conn := domain.AppConnection{Kind: kind, IndexScope: domain.IndexScopeAll, HarbrrURL: "http://harbrr"}
		return svc.buildDesired(ctx, src.instances, conn, "k", nil, profile)
	}
	prof := func(cats ...int) *domain.SyncProfile {
		return &domain.SyncProfile{Categories: cats, EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true}
	}

	tests := []struct {
		name       string
		kind       string
		cats       []Category
		profile    *domain.SyncProfile
		wantPushed bool
		wantCatIDs []int
	}{
		{
			name: "narrow-only: books tracker + books profile on sonarr still skipped",
			kind: domain.AppKindSonarr, cats: []Category{{7000, "Books"}}, profile: prof(7000),
			wantPushed: false,
		},
		{
			name: "music profile excludes audiobook-only lidarr tracker",
			kind: domain.AppKindLidarr, cats: []Category{{3030, "Audiobook"}}, profile: prof(3010, 3040),
			wantPushed: false,
		},
		{
			name: "block parent 2000 covers child 2040 on radarr",
			kind: domain.AppKindRadarr, cats: []Category{{2040, "Movies/HD"}}, profile: prof(2000),
			wantPushed: true, wantCatIDs: []int{2040},
		},
		{
			name: "exact 3030 matches only 3030 on lidarr",
			kind: domain.AppKindLidarr, cats: []Category{{3030, "Audiobook"}, {3010, "MP3"}}, profile: prof(3030),
			wantPushed: true, wantCatIDs: []int{3030},
		},
		{
			name: "intersection pushes only matched cats",
			kind: domain.AppKindSonarr, cats: []Category{{5000, "TV"}, {5040, "TV/HD"}, {5070, "TV/Anime"}}, profile: prof(5070),
			wantPushed: true, wantCatIDs: []int{5070},
		},
		{
			name: "empty-category profile falls back to kind gate (readarr audiobook 3030)",
			kind: domain.AppKindReadarr, cats: []Category{{3030, "Audiobook"}}, profile: prof(),
			wantPushed: true, wantCatIDs: []int{3030},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := build(tt.kind, tt.cats, tt.profile)
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
			if !equalIntSlice(got[0].CategoryIDs(), tt.wantCatIDs) {
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

// TestBuildDesiredProfileTogglesAndMinSeeders proves resolveToggles (enabled AND profile
// toggle, instance-disabled forcing all false) and that MinSeeders is carried.
func TestBuildDesiredProfileTogglesAndMinSeeders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := &fakeSource{
		instances: []domain.IndexerInstance{
			{ID: 1, Slug: "on", Name: "On", Enabled: true, Protocol: "torrent"},
			{ID: 2, Slug: "off", Name: "Off", Enabled: false, Protocol: "torrent"},
		},
		cats: map[string][]Category{"on": {{5000, "TV"}}, "off": {{5000, "TV"}}},
	}
	svc := &Service{source: src}
	profile := &domain.SyncProfile{
		MinSeeders: 4, EnableRss: false, EnableAutomaticSearch: true, EnableInteractiveSearch: false,
	}
	conn := domain.AppConnection{Kind: domain.AppKindSonarr, IndexScope: domain.IndexScopeAll, HarbrrURL: "http://harbrr"}
	got, err := svc.buildDesired(ctx, src.instances, conn, "k", nil, profile)
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
	if off := byslug["off"]; off.EnableRss || off.EnableAutomaticSearch || off.EnableInteractiveSearch {
		t.Errorf("disabled instance toggles must all be false, got rss %v auto %v interactive %v",
			off.EnableRss, off.EnableAutomaticSearch, off.EnableInteractiveSearch)
	}
}

// TestServiceSyncWithProfile is the end-to-end proof: create a profile, assign it to the
// fixture connection, sync, and read the stub-captured bodies for the pushed overrides.
func TestServiceSyncWithProfile(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	// TV parent 5000 covers both trackers (5000 and 5030); mixed toggles + a seeders floor.
	prof, err := f.svc.CreateProfile(ctx, CreateProfileParams{
		Name: "tv", Categories: []int{5000}, MinSeeders: 2,
		EnableRss: ptr(true), EnableAutomaticSearch: ptr(false), EnableInteractiveSearch: ptr(true),
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{
		SyncProfileID: RefUpdate{Present: true, Value: &prof.ID},
	}); err != nil {
		t.Fatalf("assign profile: %v", err)
	}
	if _, err := f.svc.Sync(ctx, f.conn.ID); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	a := f.stub.byName("Tracker A")
	if a == nil {
		t.Fatal("Tracker A not pushed")
	}
	if got := fieldInt(a.Fields, "minimumSeeders"); got != 2 {
		t.Errorf("Tracker A minimumSeeders = %d, want 2", got)
	}
	if !a.EnableRss || a.EnableAutomaticSearch || !a.EnableInteractiveSearch {
		t.Errorf("Tracker A toggles = rss %v auto %v interactive %v, want true/false/true",
			a.EnableRss, a.EnableAutomaticSearch, a.EnableInteractiveSearch)
	}
	// The disabled instance is still pushed, but every toggle is forced false.
	b := f.stub.byName("Tracker B")
	if b == nil {
		t.Fatal("Tracker B not pushed")
	}
	if b.EnableRss || b.EnableAutomaticSearch || b.EnableInteractiveSearch {
		t.Errorf("disabled Tracker B toggles must all be false, got rss %v auto %v interactive %v",
			b.EnableRss, b.EnableAutomaticSearch, b.EnableInteractiveSearch)
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
	if f.conn.SyncLevel != domain.SyncLevelFull || f.conn.IndexScope != domain.IndexScopeAll {
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
	if !errors.Is(err, ErrConflict) {
		t.Errorf("error should still wrap ErrConflict, got %v", err)
	}
	if !strings.Contains(err.Error(), "could not be revoked") {
		t.Errorf("error should surface the revoke failure, got %v", err)
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
		if _, err := f.svc.CreateConnection(ctx, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: err = %v, want ErrInvalid", i, err)
		}
	}
	// A relative URL on update is rejected too.
	if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{BaseURL: ptr("nope")}); !errors.Is(err, ErrInvalid) {
		t.Errorf("update with relative base url = %v, want ErrInvalid", err)
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
	if conn.HarbrrURL != "http://harbrr:8787" {
		t.Errorf("HarbrrURL = %q, want the trimmed value", conn.HarbrrURL)
	}
}

func TestServiceSelectedScopeFunctional(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{IndexScope: ptr(domain.IndexScopeSelected)}); err != nil {
		t.Fatalf("switch to selected scope: %v", err)
	}

	// With nothing selected, a selected-scope sync pushes nothing (no deadlock-as-error).
	rep, err := f.svc.Sync(ctx, f.conn.ID)
	if err != nil {
		t.Fatalf("empty selected Sync: %v", err)
	}
	if len(rep.Results) != 0 || f.stub.created() != 0 {
		t.Fatalf("empty selection pushed something: results=%v created=%d", rep.Results, f.stub.created())
	}

	// Select tracker-a only; sync now pushes exactly that one.
	instA := f.source.instances[0].ID
	if err := f.svc.SetSelectedIndexers(ctx, f.conn.ID, []int64{instA}); err != nil {
		t.Fatalf("SetSelectedIndexers: %v", err)
	}
	rep, err = f.svc.Sync(ctx, f.conn.ID)
	if err != nil {
		t.Fatalf("selected Sync: %v", err)
	}
	if !hasAction(rep.Results, "tracker-a", ActionCreated) || f.stub.created() != 1 {
		t.Fatalf("selected sync should push only tracker-a: results=%v created=%d", rep.Results, f.stub.created())
	}
}

func TestServiceSyncNeverClobbersSelection(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	// scope=all: first sync creates both ledger rows.
	if _, err := f.svc.Sync(ctx, f.conn.ID); err != nil {
		t.Fatalf("initial Sync: %v", err)
	}
	// Deselect tracker-a (keep tracker-b).
	instA, instB := f.source.instances[0].ID, f.source.instances[1].ID
	if err := f.svc.SetSelectedIndexers(ctx, f.conn.ID, []int64{instB}); err != nil {
		t.Fatalf("SetSelectedIndexers: %v", err)
	}
	if selectedOf(t, f, instA) {
		t.Fatalf("tracker-a should be deselected")
	}
	// A re-sync must NOT flip the deselected flag back to true.
	if _, err := f.svc.Sync(ctx, f.conn.ID); err != nil {
		t.Fatalf("re-Sync: %v", err)
	}
	if selectedOf(t, f, instA) {
		t.Errorf("re-sync clobbered the deselected flag back to true")
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

func selectedOf(t *testing.T, f *syncFixture, instID int64) bool {
	t.Helper()
	ledger, err := f.svc.ConnectionIndexers(context.Background(), f.conn.ID)
	if err != nil {
		t.Fatalf("ConnectionIndexers: %v", err)
	}
	for _, l := range ledger {
		if l.InstanceID == instID {
			return l.Selected
		}
	}
	return false
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
		if _, err := f.svc.CreateConnection(ctx, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: err = %v, want ErrInvalid", i, err)
		}
	}
}

func TestServiceUpdateRejectsBlankFields(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	blank := " "
	cases := map[string]UpdateConnectionParams{
		"blank name":       {Name: &blank},
		"blank base url":   {BaseURL: &blank},
		"blank harbrr url": {HarbrrURL: &blank},
		"blank api key":    {APIKey: &blank},
	}
	for name, p := range cases {
		if err := f.svc.UpdateConnection(ctx, f.conn.ID, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	// A non-blank patch still succeeds.
	ok := "Renamed"
	if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{Name: &ok}); err != nil {
		t.Errorf("valid update rejected: %v", err)
	}
}

func TestServiceSetSelectedRejectsUnknownID(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	if err := f.svc.SetSelectedIndexers(ctx, f.conn.ID, []int64{99999}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown instance id err = %v, want ErrInvalid", err)
	}
	// A known id is accepted.
	if err := f.svc.SetSelectedIndexers(ctx, f.conn.ID, []int64{f.source.instances[0].ID}); err != nil {
		t.Errorf("known id rejected: %v", err)
	}
}

func TestServiceCreateDuplicateConflicts(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	dup := CreateConnectionParams{
		Name: "again", Kind: f.conn.Kind, BaseURL: f.conn.BaseURL, APIKey: "k", HarbrrURL: "http://harbrr:8787",
	}
	if _, err := f.svc.CreateConnection(ctx, dup); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate (kind, base_url) err = %v, want ErrConflict", err)
	}
	// The conflicting create must not leak a minted key.
	if keys, _ := f.auth.ListAPIKeys(ctx); len(keys) != 1 {
		t.Errorf("orphan key leaked on conflict: %d keys, want 1", len(keys))
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
