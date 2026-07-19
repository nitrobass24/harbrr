package resourcemigrate_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/resourcemigrate"
	"github.com/autobrr/harbrr/internal/secrets"
)

// seedAppConn inserts a pre-fold app_connections row (app_id NULL) with a legacy
// credential sealed under the row's own id, discriminator domain.AppSecret — exactly
// what a pre-ADR-0004 row looks like.
func seedAppConn(t *testing.T, db *database.DB, kr *secrets.Keyring, kind, baseURL, cred string, updatedAt time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := (database.AppConnections{}).InsertConnection(ctx, db, domain.AppConnection{
		Name: kind, Kind: kind, BaseURL: baseURL, HarbrrURL: "http://harbrr.local",
		Enabled: true, SyncLevel: domain.SyncLevelFull, IndexScope: domain.IndexScopeAll,
		FreeleechMode: domain.FreeleechModeHonor, CreatedAt: updatedAt, UpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatalf("InsertConnection: %v", err)
	}
	enc, err := kr.Encrypt(id, domain.AppSecret, cred)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := (database.AppConnections{}).SetConnectionSecrets(ctx, db, id, enc, "", kr.KeyID()); err != nil {
		t.Fatalf("SetConnectionSecrets: %v", err)
	}
	return id
}

// seedAnnounceConn inserts a pre-fold announce_connections row, mirroring seedAppConn.
func seedAnnounceConn(t *testing.T, db *database.DB, kr *secrets.Keyring, kind, baseURL, cred string, updatedAt time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := (database.AnnounceConnections{}).InsertAnnounceConnection(ctx, db, domain.AnnounceConnection{
		Name: kind, Kind: kind, BaseURL: baseURL, HarbrrURL: "http://harbrr.local",
		Enabled: true, CreatedAt: updatedAt, UpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatalf("InsertAnnounceConnection: %v", err)
	}
	enc, err := kr.Encrypt(id, domain.AppSecret, cred)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := (database.AnnounceConnections{}).SetAnnounceConnectionSecrets(ctx, db, id, enc, "", kr.KeyID()); err != nil {
		t.Fatalf("SetAnnounceConnectionSecrets: %v", err)
	}
	return id
}

// seedDownloadClient inserts a pre-fold networked download_clients row (app_id NULL,
// Host set), its legacy credential sealed under domain.DownloadClientSecret.
func seedDownloadClient(t *testing.T, db *database.DB, kr *secrets.Keyring, name, kind, host, username, cred string, updatedAt time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := (database.DownloadClients{}).InsertDownloadClient(ctx, db, domain.DownloadClient{
		Name: name, Kind: kind, Host: host, Username: username, Enabled: true,
		CreatedAt: updatedAt, UpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatalf("InsertDownloadClient: %v", err)
	}
	enc, err := kr.Encrypt(id, domain.DownloadClientSecret, cred)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := (database.DownloadClients{}).SetDownloadClientSecret(ctx, db, id, enc, kr.KeyID()); err != nil {
		t.Fatalf("SetDownloadClientSecret: %v", err)
	}
	return id
}

// seedHostlessDownload inserts a host-less (blackhole) download_clients row: no host,
// no credential — the fold's "no identity to fold" case.
func seedHostlessDownload(t *testing.T, db *database.DB, name, kind string, updatedAt time.Time) int64 {
	t.Helper()
	id, err := (database.DownloadClients{}).InsertDownloadClient(context.Background(), db, domain.DownloadClient{
		Name: name, Kind: kind, Enabled: true, CreatedAt: updatedAt, UpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatalf("InsertDownloadClient (hostless): %v", err)
	}
	return id
}

// getApp looks up an App by identity, returning (App{}, false) on ErrNotFound so
// callers can assert absence without a t.Fatalf.
func getApp(t *testing.T, db *database.DB, kind, baseURL string) (domain.App, bool) {
	t.Helper()
	app, err := (database.Apps{}).GetAppByIdentity(context.Background(), db, kind, baseURL)
	if errors.Is(err, database.ErrNotFound) {
		return domain.App{}, false
	}
	if err != nil {
		t.Fatalf("GetAppByIdentity(%s, %s): %v", kind, baseURL, err)
	}
	return app, true
}

// decryptAppCred decrypts an App's own credential under its own id + domain.AppSecret
// — the AAD every fold-created App credential is sealed under, regardless of which
// legacy discriminator the row it was folded from used.
func decryptAppCred(t *testing.T, kr *secrets.Keyring, app domain.App) string {
	t.Helper()
	plain, err := kr.Decrypt(app.ID, domain.AppSecret, app.APIKeyEncrypted)
	if err != nil {
		t.Fatalf("decrypt app %d credential: %v", app.ID, err)
	}
	return plain
}

// TestFoldAppsFoldsAllThreeSurfaces seeds one un-folded row per surface, each with a
// distinct (kind, base_url) identity, and asserts every row gets an App with the
// original credential intact under the App's own AAD.
func TestFoldAppsFoldsAllThreeSurfaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, kr := setup(t)
	now := time.Now().UTC()

	appConnID := seedAppConn(t, db, kr, domain.AppKindSonarr, "http://sonarr:8989", "sonarr-api-key", now)
	annConnID := seedAnnounceConn(t, db, kr, domain.AnnounceKindCrossSeedV6, "http://cross-seed:2468", "cross-seed-api-key", now)
	dlID := seedDownloadClient(t, db, kr, "qbt", domain.DownloadClientKindQBittorrent, "qbt:8080", "admin", "qbt-password", now)

	if err := resourcemigrate.FoldApps(ctx, db, kr, time.Now, zerolog.Nop()); err != nil {
		t.Fatalf("FoldApps: %v", err)
	}

	appConn, err := (database.AppConnections{}).GetConnection(ctx, db, appConnID)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if appConn.AppID == nil {
		t.Fatal("app_connections row: app_id still NULL after fold")
	}
	app, ok := getApp(t, db, domain.AppKindSonarr, "http://sonarr:8989")
	if !ok {
		t.Fatal("no App created for the app-sync connection's identity")
	}
	if app.ID != *appConn.AppID {
		t.Errorf("app_connections app_id = %d, want the resolved App %d", *appConn.AppID, app.ID)
	}
	if got := decryptAppCred(t, kr, app); got != "sonarr-api-key" {
		t.Errorf("app-sync App credential = %q, want original", got)
	}

	annConn, err := (database.AnnounceConnections{}).GetAnnounceConnection(ctx, db, annConnID)
	if err != nil {
		t.Fatalf("GetAnnounceConnection: %v", err)
	}
	if annConn.AppID == nil {
		t.Fatal("announce_connections row: app_id still NULL after fold")
	}
	annApp, ok := getApp(t, db, domain.AnnounceKindCrossSeedV6, "http://cross-seed:2468")
	if !ok {
		t.Fatal("no App created for the announce connection's identity")
	}
	if annApp.ID != *annConn.AppID {
		t.Errorf("announce_connections app_id = %d, want the resolved App %d", *annConn.AppID, annApp.ID)
	}
	if got := decryptAppCred(t, kr, annApp); got != "cross-seed-api-key" {
		t.Errorf("announce App credential = %q, want original", got)
	}

	dl, err := (database.DownloadClients{}).GetDownloadClient(ctx, db, dlID)
	if err != nil {
		t.Fatalf("GetDownloadClient: %v", err)
	}
	if dl.AppID == nil {
		t.Fatal("download_clients row: app_id still NULL after fold")
	}
	dlApp, ok := getApp(t, db, domain.DownloadClientKindQBittorrent, "qbt:8080")
	if !ok {
		t.Fatal("no App created for the download client's identity")
	}
	if dlApp.ID != *dl.AppID {
		t.Errorf("download_clients app_id = %d, want the resolved App %d", *dl.AppID, dlApp.ID)
	}
	if got := decryptAppCred(t, kr, dlApp); got != "qbt-password" {
		t.Errorf("download App credential = %q, want original", got)
	}
}

// TestFoldAppsDedupNewestWins seeds an app-sync row and a download-client row that
// share the same (kind, base_url) identity but carry different credentials and
// updated_at timestamps. The fold must collapse them into ONE App holding the NEWER
// credential, log a conflict warning, and never put either plaintext credential in
// the log.
func TestFoldAppsDedupNewestWins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, kr := setup(t)

	const sharedKind = domain.AppKindQui
	const sharedBaseURL = "http://shared.example:9999"
	older := time.Now().UTC().Add(-time.Hour)
	newer := time.Now().UTC()

	appConnID := seedAppConn(t, db, kr, sharedKind, sharedBaseURL, "newer-secret", newer)
	dlID := seedDownloadClient(t, db, kr, "qui-dl", domain.DownloadClientKindQui, sharedBaseURL, "", "older-secret", older)

	var buf bytes.Buffer
	log := zerolog.New(&buf)
	if err := resourcemigrate.FoldApps(ctx, db, kr, time.Now, log); err != nil {
		t.Fatalf("FoldApps: %v", err)
	}

	apps, err := (database.Apps{}).ListApps(ctx, db)
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("apps after fold = %d, want 1 (deduped)", len(apps))
	}

	appConn, _ := (database.AppConnections{}).GetConnection(ctx, db, appConnID)
	dl, _ := (database.DownloadClients{}).GetDownloadClient(ctx, db, dlID)
	if appConn.AppID == nil || dl.AppID == nil || *appConn.AppID != *dl.AppID {
		t.Fatalf("rows point at different apps: app_conn %v, download %v", appConn.AppID, dl.AppID)
	}
	if *appConn.AppID != apps[0].ID {
		t.Fatalf("rows point at app %d, want the sole App %d", *appConn.AppID, apps[0].ID)
	}

	if got := decryptAppCred(t, kr, apps[0]); got != "newer-secret" {
		t.Errorf("deduped App credential = %q, want the newer credential", got)
	}

	logged := buf.String()
	if !strings.Contains(logged, "different credential") {
		t.Errorf("log missing the dedup conflict warning; got: %s", logged)
	}
	if strings.Contains(logged, "newer-secret") || strings.Contains(logged, "older-secret") {
		t.Error("log contains a plaintext credential; want neither ever logged")
	}
}

// TestFoldAppsSkipsHostlessDownload asserts a host-less download client (blackhole)
// has no network identity to fold: it gets no app_id and no App is created for it.
func TestFoldAppsSkipsHostlessDownload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, kr := setup(t)
	now := time.Now().UTC()

	id := seedHostlessDownload(t, db, "bh", domain.DownloadClientKindBlackhole, now)

	if err := resourcemigrate.FoldApps(ctx, db, kr, time.Now, zerolog.Nop()); err != nil {
		t.Fatalf("FoldApps: %v", err)
	}

	dl, err := (database.DownloadClients{}).GetDownloadClient(ctx, db, id)
	if err != nil {
		t.Fatalf("GetDownloadClient: %v", err)
	}
	if dl.AppID != nil {
		t.Errorf("hostless download client got app_id %v, want nil", dl.AppID)
	}
	if _, ok := getApp(t, db, domain.DownloadClientKindBlackhole, ""); ok {
		t.Error("an App was created for the hostless (blackhole) identity; want none")
	}
	apps, err := (database.Apps{}).ListApps(ctx, db)
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("apps after fold = %d, want 0 (nothing else was seeded)", len(apps))
	}
}

// TestFoldAppsIsIdempotent runs FoldApps twice over the same seed data: the second
// run must be a no-op (the apps_folded flag short-circuits it), producing no
// duplicate Apps.
func TestFoldAppsIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, kr := setup(t)
	now := time.Now().UTC()
	seedAppConn(t, db, kr, domain.AppKindSonarr, "http://sonarr:8989", "sonarr-api-key", now)

	for i := range 2 {
		if err := resourcemigrate.FoldApps(ctx, db, kr, time.Now, zerolog.Nop()); err != nil {
			t.Fatalf("FoldApps #%d: %v", i, err)
		}
	}

	apps, err := (database.Apps{}).ListApps(ctx, db)
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("apps after two runs = %d, want 1 (idempotent)", len(apps))
	}
	done, ok, err := (database.AppMeta{}).Get(ctx, db, "apps_folded")
	if err != nil {
		t.Fatalf("AppMeta.Get: %v", err)
	}
	if !ok || done != "1" {
		t.Errorf("apps_folded flag = %q (ok=%v), want \"1\"", done, ok)
	}
}

// TestFoldAppsSkipsAlreadyFoldedRows covers the retry-window guard (mirrors
// TestMigrateSkipsAlreadyWiredSlot in migrate_test.go): a row that already carries an
// app_id — e.g. an operator wired it via the API after a transient first-run failure —
// must not be re-folded into a second App.
func TestFoldAppsSkipsAlreadyFoldedRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, kr := setup(t)
	now := time.Now().UTC()

	// A pre-existing App, created directly (as if a prior fold or the API created it).
	appRepo := database.Apps{}
	appID, err := appRepo.InsertApp(ctx, db, domain.App{
		Kind: domain.AppKindSonarr, Name: "Sonarr", BaseURL: "http://sonarr:8989",
		HarbrrURL: "http://harbrr.local", Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertApp: %v", err)
	}
	sealed, err := kr.Encrypt(appID, domain.AppSecret, "already-sealed-key")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := appRepo.SetAppSecret(ctx, db, appID, sealed, kr.KeyID()); err != nil {
		t.Fatalf("SetAppSecret: %v", err)
	}

	// A connection already wired to it — no legacy credential to fold, app_id set.
	connID, err := (database.AppConnections{}).InsertConnection(ctx, db, domain.AppConnection{
		Name: "Sonarr", Kind: domain.AppKindSonarr, AppID: &appID, BaseURL: "http://sonarr:8989",
		HarbrrURL: "http://harbrr.local", Enabled: true, SyncLevel: domain.SyncLevelFull,
		IndexScope: domain.IndexScopeAll, FreeleechMode: domain.FreeleechModeHonor,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertConnection: %v", err)
	}

	if err := resourcemigrate.FoldApps(ctx, db, kr, time.Now, zerolog.Nop()); err != nil {
		t.Fatalf("FoldApps: %v", err)
	}

	apps, err := (database.Apps{}).ListApps(ctx, db)
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("apps after fold = %d, want 1 (no duplicate created)", len(apps))
	}
	conn, err := (database.AppConnections{}).GetConnection(ctx, db, connID)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if conn.AppID == nil || *conn.AppID != appID {
		t.Errorf("connection app_id = %v, want unchanged %d", conn.AppID, appID)
	}
}

// TestFoldAppsCredentialRoundTrips runs the fold on one row per surface and confirms
// each App's sealed credential decrypts under (App.ID, domain.AppSecret) back to the
// exact plaintext the legacy row carried — regardless of the legacy discriminator
// (domain.AppSecret for appsync/announce, domain.DownloadClientSecret for download)
// the value was originally sealed under.
func TestFoldAppsCredentialRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, kr := setup(t)
	now := time.Now().UTC()

	tests := []struct {
		name    string
		kind    string
		baseURL string
		cred    string
	}{
		{"app-sync", domain.AppKindRadarr, "http://radarr:7878", "radarr-key-1"},
		{"announce", domain.AnnounceKindQui, "http://qui:7476", "qui-announce-key-1"},
		{"download", domain.DownloadClientKindQBittorrent, "qbt2:8080", "qbt-secret-1"},
	}
	for _, tc := range tests {
		switch tc.name {
		case "app-sync":
			seedAppConn(t, db, kr, tc.kind, tc.baseURL, tc.cred, now)
		case "announce":
			seedAnnounceConn(t, db, kr, tc.kind, tc.baseURL, tc.cred, now)
		case "download":
			seedDownloadClient(t, db, kr, "qbt2", tc.kind, tc.baseURL, "user", tc.cred, now)
		}
	}

	if err := resourcemigrate.FoldApps(ctx, db, kr, time.Now, zerolog.Nop()); err != nil {
		t.Fatalf("FoldApps: %v", err)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, ok := getApp(t, db, tc.kind, tc.baseURL)
			if !ok {
				t.Fatalf("no App created for %s identity (%s, %s)", tc.name, tc.kind, tc.baseURL)
			}
			if got := decryptAppCred(t, kr, app); got != tc.cred {
				t.Errorf("%s App credential round-trip = %q, want %q", tc.name, got, tc.cred)
			}
		})
	}
}
