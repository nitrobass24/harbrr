package apps_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/apps"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func ptr[T any](v T) *T { return &v }

// newService builds an apps.Service over an in-memory DB with a real keyring. The
// DB is also returned so a test can insert a referencing row directly (a surface
// table this package deliberately knows nothing about — only its own repo).
func newService(t *testing.T) (*apps.Service, *database.DB) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return apps.NewService(db, kr, http.DefaultClient, zerolog.Nop()), db
}

// TestResolveCreateReuseRotate exercises the three identity-driven outcomes of
// Resolve documented on the type: a new (kind, base_url) mints an App with the
// credential sealed under the App's own id; the same identity with an empty APIKey
// is a pure reuse (credential untouched); the same identity with a NEW non-empty
// APIKey rotates the stored credential in place.
func TestResolveCreateReuseRotate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)

	created, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindQui, Name: "qui", BaseURL: "http://qui:7476", APIKey: "secret-1"})
	if err != nil {
		t.Fatalf("Resolve (create): %v", err)
	}
	if created.ID == 0 {
		t.Fatal("Resolve (create) returned id 0")
	}
	key, err := svc.DecryptKey(created)
	if err != nil || key != "secret-1" {
		t.Fatalf("DecryptKey (create) = %q, err %v, want secret-1", key, err)
	}
	if created.APIKeyEncrypted == "secret-1" {
		t.Error("credential stored in the clear")
	}

	reused, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindQui, BaseURL: "http://qui:7476"})
	if err != nil {
		t.Fatalf("Resolve (reuse, empty key): %v", err)
	}
	if reused.ID != created.ID {
		t.Errorf("reused.ID = %d, want %d (same identity)", reused.ID, created.ID)
	}
	key, err = svc.DecryptKey(reused)
	if err != nil || key != "secret-1" {
		t.Errorf("DecryptKey (reuse) = %q, err %v, want unchanged secret-1", key, err)
	}

	rotated, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindQui, BaseURL: "http://qui:7476", APIKey: "secret-2"})
	if err != nil {
		t.Fatalf("Resolve (rotate): %v", err)
	}
	if rotated.ID != created.ID {
		t.Errorf("rotated.ID = %d, want %d (same identity)", rotated.ID, created.ID)
	}
	key, err = svc.DecryptKey(rotated)
	if err != nil || key != "secret-2" {
		t.Errorf("DecryptKey (rotate) = %q, err %v, want secret-2", key, err)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("len(apps) = %d, want 1 (identity reused, not duplicated)", len(list))
	}
}

// TestResolveByAppID covers the id-driven path: an existing id reuses the app when
// the kind matches, and rejects with domain.ErrInvalid when it doesn't (the App
// belongs to a different kind of surface).
func TestResolveByAppID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)

	app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindQui, BaseURL: "http://qui:7476", APIKey: "k"})
	if err != nil {
		t.Fatalf("Resolve (create): %v", err)
	}

	got, err := svc.Resolve(ctx, apps.Ref{AppID: &app.ID, Kind: domain.AppKindQui})
	if err != nil {
		t.Fatalf("Resolve (by id, matching kind): %v", err)
	}
	if got.ID != app.ID {
		t.Errorf("got.ID = %d, want %d", got.ID, app.ID)
	}

	if _, err := svc.Resolve(ctx, apps.Ref{AppID: &app.ID, Kind: domain.AppKindSonarr}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("Resolve (by id, kind mismatch) err = %v, want domain.ErrInvalid", err)
	}
}

// TestResolveHarbrrURLBackfill covers the reconcile-time backfill: a create call
// that finds an existing app with no harbrr_url yet fills it in from the caller's
// value; a later call with a different value does not overwrite an already-set one.
func TestResolveHarbrrURLBackfill(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)

	app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindSonarr, BaseURL: "http://sonarr:8989", APIKey: "k"})
	if err != nil {
		t.Fatalf("Resolve (create, no harbrr_url): %v", err)
	}
	if app.HarbrrURL != "" {
		t.Fatalf("app.HarbrrURL = %q, want empty before backfill", app.HarbrrURL)
	}

	backfilled, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindSonarr, BaseURL: "http://sonarr:8989", HarbrrURL: "http://harbrr:7575"})
	if err != nil {
		t.Fatalf("Resolve (backfill): %v", err)
	}
	if backfilled.HarbrrURL != "http://harbrr:7575" {
		t.Errorf("HarbrrURL = %q, want http://harbrr:7575", backfilled.HarbrrURL)
	}

	unchanged, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindSonarr, BaseURL: "http://sonarr:8989", HarbrrURL: "http://other:9999"})
	if err != nil {
		t.Fatalf("Resolve (already set): %v", err)
	}
	if unchanged.HarbrrURL != "http://harbrr:7575" {
		t.Errorf("HarbrrURL = %q, want unchanged http://harbrr:7575 (already set)", unchanged.HarbrrURL)
	}
}

func TestUpdateCredentialRotatesAndPatchesFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)

	app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindQui, BaseURL: "http://qui:7476", Username: "old-user", APIKey: "old-key"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if err := svc.UpdateCredential(ctx, app.ID, apps.UpdateParams{
		Name: ptr("renamed"), Username: ptr("new-user"), APIKey: ptr("new-key"),
	}); err != nil {
		t.Fatalf("UpdateCredential: %v", err)
	}

	got, err := svc.Get(ctx, app.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "renamed" || got.Username != "new-user" {
		t.Errorf("got = %+v, want Name renamed, Username new-user", got)
	}
	key, err := svc.DecryptKey(got)
	if err != nil || key != "new-key" {
		t.Errorf("DecryptKey = %q, err %v, want new-key", key, err)
	}
}

func TestUpdateCredentialInvalidAPIKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindQui, BaseURL: "http://qui:7476", APIKey: "old-key"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	tests := []struct {
		name string
		key  string
	}{
		{"blank", "  "},
		{"redacted sentinel", secrets.Redacted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := svc.UpdateCredential(ctx, app.ID, apps.UpdateParams{APIKey: ptr(tt.key)})
			if !errors.Is(err, domain.ErrInvalid) {
				t.Errorf("err = %v, want domain.ErrInvalid", err)
			}
		})
	}
}

func TestDeleteUnreferencedOK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindQui, BaseURL: "http://qui:7476", APIKey: "k"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := svc.Delete(ctx, app.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, app.ID); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("Get after delete err = %v, want database.ErrNotFound", err)
	}
}

func TestDeleteReferencedConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, db := newService(t)
	app, err := svc.Resolve(ctx, apps.Ref{Kind: domain.AppKindSonarr, BaseURL: "http://sonarr:8989", APIKey: "k"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if _, err := (database.AppConnections{}).InsertConnection(ctx, db, domain.AppConnection{
		Name: "sonarr-1", Kind: domain.AppKindSonarr, AppID: &app.ID,
		SyncLevel: domain.SyncLevelFull, IndexScope: domain.IndexScopeAll, FreeleechMode: domain.FreeleechModeHonor,
	}); err != nil {
		t.Fatalf("insert app_connections: %v", err)
	}

	if err := svc.Delete(ctx, app.ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("Delete (referenced) err = %v, want domain.ErrConflict", err)
	}
}

func TestDeleteMissingNotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	if err := svc.Delete(context.Background(), 999); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("err = %v, want database.ErrNotFound", err)
	}
}

// TestUpdateBaseURLPropagatesToSurfaces proves a base_url PATCH rewrites the interim
// base_url/host copies every referencing surface row carries for its UNIQUE(kind,
// base_url) index (else the guard goes stale until #269 drops the copies).
func TestUpdateBaseURLPropagatesToSurfaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, db := newService(t)

	app, err := svc.Resolve(ctx, apps.Ref{
		Kind: domain.AppKindQui, Name: "qui", BaseURL: "http://old:7476",
		APIKey: "k", HarbrrURL: "http://harbrr:7478",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := (database.AppConnections{}).InsertConnection(ctx, db, domain.AppConnection{
		Name: "c", Kind: domain.AppKindQui, AppID: &app.ID, BaseURL: "http://old:7476",
		SyncLevel: domain.SyncLevelFull, IndexScope: domain.IndexScopeAll, FreeleechMode: domain.FreeleechModeBypass,
	}); err != nil {
		t.Fatalf("insert app_connections: %v", err)
	}
	if _, err := (database.AnnounceConnections{}).InsertAnnounceConnection(ctx, db, domain.AnnounceConnection{
		Name: "a", Kind: domain.AnnounceKindQui, AppID: &app.ID, BaseURL: "http://old:7476",
	}); err != nil {
		t.Fatalf("insert announce_connections: %v", err)
	}
	if _, err := (database.DownloadClients{}).InsertDownloadClient(ctx, db, domain.DownloadClient{
		Name: "d", Kind: domain.DownloadClientKindQui, AppID: &app.ID, Host: "http://old:7476",
		Settings: domain.DownloadClientSettings{Qui: &domain.QuiSettings{InstanceID: 1}},
	}); err != nil {
		t.Fatalf("insert download_clients: %v", err)
	}

	if err := svc.UpdateCredential(ctx, app.ID, apps.UpdateParams{BaseURL: ptr("http://new:7476")}); err != nil {
		t.Fatalf("UpdateCredential: %v", err)
	}

	ac, _ := (database.AppConnections{}).GetConnection(ctx, db, 1)
	an, _ := (database.AnnounceConnections{}).GetAnnounceConnection(ctx, db, 1)
	dc, _ := (database.DownloadClients{}).GetDownloadClient(ctx, db, 1)
	if ac.BaseURL != "http://new:7476" || an.BaseURL != "http://new:7476" || dc.Host != "http://new:7476" {
		t.Errorf("copies not propagated: app_conn=%q announce=%q download=%q", ac.BaseURL, an.BaseURL, dc.Host)
	}
}
