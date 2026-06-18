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
}

func (f *fakeSource) List(context.Context) ([]domain.IndexerInstance, error) {
	return f.instances, nil
}

func (f *fakeSource) Categories(_ context.Context, slug string) ([]Category, error) {
	return f.cats[slug], nil
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
		cats: map[string][]Category{
			"tracker-a": {{ID: 5000, Name: "TV"}},
			"tracker-b": {{ID: 2000, Name: "Movies"}},
		},
	}

	stub := newServarrStub()
	srv := httptest.NewServer(stub.handler())
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
