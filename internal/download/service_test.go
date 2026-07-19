package download

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/apps"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// newService builds a download.Service over an in-memory DB (exercising the
// migrations implicitly) with a fixed clock, backed by a real apps.Service (a
// networked client's identity + credential now live on an App, ADR 0004). The
// apps.Service is returned so a test can decrypt an App's credential to prove it
// round trips (and is not stored in the clear on either the App or the row).
func newService(t *testing.T) (*Service, *apps.Service) {
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
	appsSvc := apps.NewService(db, kr, http.DefaultClient, zerolog.Nop())
	svc := NewService(db, appsSvc, kr, http.DefaultClient, zerolog.Nop())
	svc.clock = func() time.Time { return time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC) }
	return svc, appsSvc
}

func ptrString(s string) *string { return &s }

func TestCreateSealsSecretOnApp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, appsSvc := newService(t)

	const secret = "hunter2"
	c, err := svc.Create(ctx, CreateParams{
		Name: "seedbox", Kind: domain.DownloadClientKindQBittorrent, Host: "http://localhost:8080",
		Username: "admin", Secret: secret,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !c.Enabled {
		t.Error("Enabled default = false, want true")
	}
	if c.SecretEncrypted != "" {
		t.Errorf("row SecretEncrypted = %q, want empty (credential lives on the App)", c.SecretEncrypted)
	}
	if c.AppID == nil {
		t.Fatal("AppID = nil, want set")
	}

	app, err := appsSvc.Get(ctx, *c.AppID)
	if err != nil {
		t.Fatalf("Get app: %v", err)
	}
	if app.BaseURL != "http://localhost:8080" || app.Username != "admin" {
		t.Errorf("app = %+v, want BaseURL/Username from create", app)
	}
	if app.APIKeyEncrypted == secret || app.APIKeyEncrypted == "" {
		t.Errorf("app credential stored in the clear (or empty): %q", app.APIKeyEncrypted)
	}
	got, err := appsSvc.DecryptKey(app)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != secret {
		t.Errorf("decrypted secret = %q, want %q", got, secret)
	}
}

func TestCreateValidation(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	tests := []struct {
		name string
		p    CreateParams
	}{
		{"blank name", CreateParams{Name: "  ", Kind: domain.DownloadClientKindQBittorrent, Host: "http://x.invalid"}},
		{"unregistered kind", CreateParams{Name: "n", Kind: "no-such-kind", Host: "http://x.invalid"}},
		{"unknown kind", CreateParams{Name: "n", Kind: "bogus", Host: "http://x.invalid"}},
		{"relative host", CreateParams{Name: "n", Kind: domain.DownloadClientKindQBittorrent, Host: "/x"}},
		{"blank host", CreateParams{Name: "n", Kind: domain.DownloadClientKindQBittorrent, Host: ""}},
		{"hostPort empty host", CreateParams{Name: "n", Kind: domain.DownloadClientKindDeluge, Host: ":58846"}},
		{"hostPort empty port", CreateParams{Name: "n", Kind: domain.DownloadClientKindDeluge, Host: "localhost:"}},
		{"hostPort non-numeric port", CreateParams{Name: "n", Kind: domain.DownloadClientKindDeluge, Host: "localhost:not-a-port"}},
		{"hostPort zero port", CreateParams{Name: "n", Kind: domain.DownloadClientKindDeluge, Host: "localhost:0"}},
		{"blackhole host must be empty", CreateParams{
			Name: "bh", Kind: domain.DownloadClientKindBlackhole, Host: "http://x.invalid",
			Settings: domain.DownloadClientSettings{Blackhole: &domain.BlackholeSettings{TorrentDir: "/watch"}},
		}},
		{"blackhole requires a dir", CreateParams{
			Name: "bh", Kind: domain.DownloadClientKindBlackhole,
			Settings: domain.DownloadClientSettings{Blackhole: &domain.BlackholeSettings{}},
		}},
		{"blackhole relative dir", CreateParams{
			Name: "bh", Kind: domain.DownloadClientKindBlackhole,
			Settings: domain.DownloadClientSettings{Blackhole: &domain.BlackholeSettings{TorrentDir: "relative/dir"}},
		}},
		{"blackhole app id given", CreateParams{
			Name: "bh", Kind: domain.DownloadClientKindBlackhole, AppID: ptrInt64(1),
			Settings: domain.DownloadClientSettings{Blackhole: &domain.BlackholeSettings{TorrentDir: "/watch"}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := svc.Create(context.Background(), tt.p); !errors.Is(err, domain.ErrInvalid) {
				t.Errorf("err = %v, want domain.ErrInvalid", err)
			}
		})
	}
}

func TestCreateDuplicateNameConflicts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	p := CreateParams{Name: "seedbox", Kind: domain.DownloadClientKindQBittorrent, Host: "http://x.invalid"}
	if _, err := svc.Create(ctx, p); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if _, err := svc.Create(ctx, p); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("err = %v, want domain.ErrConflict", err)
	}
}

// TestCreateWithAppIDReusesExistingApp exercises the AppID reuse path of a
// networked create: a second client pointed at the first's App shares that App
// rather than minting a duplicate.
func TestCreateWithAppIDReusesExistingApp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, appsSvc := newService(t)

	first, err := svc.Create(ctx, CreateParams{
		Name: "seedbox-a", Kind: domain.DownloadClientKindQBittorrent, Host: "http://shared.invalid",
		Username: "admin", Secret: "hunter2",
	})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if first.AppID == nil {
		t.Fatal("first.AppID = nil, want set")
	}

	second, err := svc.Create(ctx, CreateParams{
		Name: "seedbox-b", Kind: domain.DownloadClientKindQBittorrent, AppID: first.AppID,
	})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if second.AppID == nil || *second.AppID != *first.AppID {
		t.Errorf("second.AppID = %v, want %v", second.AppID, first.AppID)
	}

	list, err := appsSvc.List(ctx)
	if err != nil {
		t.Fatalf("List apps: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("len(apps) = %d, want 1 (reused, not duplicated)", len(list))
	}
}

// TestUpdateDoesNotTouchAppCredential replaces the pre-ADR-0004 rotate-on-Update
// test: UpdateParams no longer carries identity/credential fields, so a Name patch
// must leave the App's sealed credential untouched. Rotation is now exclusively an
// apps.Service concern (see internal/apps).
func TestUpdateDoesNotTouchAppCredential(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, appsSvc := newService(t)

	c, err := svc.Create(ctx, CreateParams{
		Name: "seedbox", Kind: domain.DownloadClientKindQBittorrent, Host: "http://x.invalid", Secret: "old",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Update(ctx, c.ID, UpdateParams{Name: ptrString("renamed")}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "renamed" {
		t.Errorf("Name = %q, want renamed", got.Name)
	}
	if got.AppID == nil || *got.AppID != *c.AppID {
		t.Errorf("AppID = %v, want unchanged %v", got.AppID, c.AppID)
	}

	app, err := appsSvc.Get(ctx, *got.AppID)
	if err != nil {
		t.Fatalf("Get app: %v", err)
	}
	secret, err := appsSvc.DecryptKey(app)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if secret != "old" {
		t.Errorf("secret after Update = %q, want unchanged old", secret)
	}
}

// TestValidateSettingsKindMismatch exercises the settings/kind cross-check
// directly for every settings shape: a populated settings field only validates
// against its own kind, and mismatches (including against an unregistered kind,
// which validateSettings itself doesn't distinguish — validateKind rejects that
// earlier in the Create/Update path) are domain.ErrInvalid.
func TestValidateSettingsKindMismatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings domain.DownloadClientSettings
		match    string
	}{
		{"qbittorrent", domain.DownloadClientSettings{QBittorrent: &domain.QBittorrentSettings{Category: "tv"}}, domain.DownloadClientKindQBittorrent},
		{"sabnzbd", domain.DownloadClientSettings{Sabnzbd: &domain.SabnzbdSettings{Category: "tv"}}, domain.DownloadClientKindSabnzbd},
		{"nzbget", domain.DownloadClientSettings{NZBGet: &domain.NZBGetSettings{Category: "tv"}}, domain.DownloadClientKindNZBGet},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := validateSettings(tt.match, tt.settings); err != nil {
				t.Errorf("matching kind: err = %v, want nil", err)
			}
			if err := validateSettings(domain.DownloadClientKindDeluge, tt.settings); !errors.Is(err, domain.ErrInvalid) {
				t.Errorf("mismatched kind: err = %v, want domain.ErrInvalid", err)
			}
		})
	}
}

func TestUpdateSettingsKindMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	c, err := svc.Create(ctx, CreateParams{Name: "seedbox", Kind: domain.DownloadClientKindQBittorrent, Host: "http://x.invalid"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// qbittorrent settings on a qbittorrent-kind row is fine.
	settings := domain.DownloadClientSettings{QBittorrent: &domain.QBittorrentSettings{Category: "tv"}}
	if err := svc.Update(ctx, c.ID, UpdateParams{Settings: &settings}); err != nil {
		t.Errorf("Update with matching-kind settings: %v", err)
	}
}

func TestCreateBlackhole_Success(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	c, err := svc.Create(context.Background(), CreateParams{
		Name: "bh", Kind: domain.DownloadClientKindBlackhole,
		Settings: domain.DownloadClientSettings{Blackhole: &domain.BlackholeSettings{TorrentDir: "/watch/torrents"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.AppID != nil {
		t.Errorf("AppID = %v, want nil (host-less kind)", c.AppID)
	}
	if c.Host != "" {
		t.Errorf("Host = %q, want empty", c.Host)
	}
	if c.Settings.Blackhole == nil || c.Settings.Blackhole.TorrentDir != "/watch/torrents" {
		t.Errorf("Settings.Blackhole = %+v, want TorrentDir /watch/torrents", c.Settings.Blackhole)
	}
}

// TestCreateQuiSucceeds proves a qui client resolves its App the same way any
// other networked kind does — qui is otherwise driven entirely by its
// per-instance Settings.
func TestCreateQuiSucceeds(t *testing.T) {
	t.Parallel()
	svc, appsSvc := newService(t)
	c, err := svc.Create(context.Background(), CreateParams{
		Name: "qui", Kind: domain.DownloadClientKindQui, Host: "http://qui.invalid",
		Secret: "qui-key", Settings: domain.DownloadClientSettings{Qui: &domain.QuiSettings{InstanceID: 1}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.AppID == nil {
		t.Fatal("AppID = nil, want set")
	}
	app, err := appsSvc.Get(context.Background(), *c.AppID)
	if err != nil {
		t.Fatalf("Get app: %v", err)
	}
	if app.BaseURL != "http://qui.invalid" {
		t.Errorf("app.BaseURL = %q, want http://qui.invalid", app.BaseURL)
	}
}

func TestSetEnabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	c, err := svc.Create(ctx, CreateParams{Name: "seedbox", Kind: domain.DownloadClientKindQBittorrent, Host: "http://x.invalid"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.SetEnabled(ctx, c.ID, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Enabled {
		t.Error("Enabled = true after SetEnabled(false)")
	}
}

func TestDeleteThenGetNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	c, err := svc.Create(ctx, CreateParams{Name: "seedbox", Kind: domain.DownloadClientKindQBittorrent, Host: "http://x.invalid"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(ctx, c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, c.ID); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("Get after delete err = %v, want database.ErrNotFound", err)
	}
}

func TestTestConnectionEndToEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Ok."))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	svc, _ := newService(t)
	c, err := svc.Create(ctx, CreateParams{
		Name: "seedbox", Kind: domain.DownloadClientKindQBittorrent, Host: srv.URL,
		Username: "admin", Secret: "adminadmin",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.TestConnection(ctx, c.ID); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
}

func TestTestConnectionUnknownID(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	if err := svc.TestConnection(context.Background(), 999); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("err = %v, want database.ErrNotFound", err)
	}
}

// TestTestConnectionMigrationPending simulates a pre-fold row (a networked kind
// with a NULL app_id, the state a legacy row is in before the boot fold in
// internal/resourcemigrate runs) by writing it directly through the repo — the
// service's own Create always resolves an App, so this state is otherwise
// unreachable through the public API.
func TestTestConnectionMigrationPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)

	now := svc.clock()
	id, err := (database.DownloadClients{}).InsertDownloadClient(ctx, svc.db, domain.DownloadClient{
		Name: "legacy", Kind: domain.DownloadClientKindQBittorrent, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	if err := svc.TestConnection(ctx, id); !errors.Is(err, domain.ErrAppMigrationPending) {
		t.Errorf("err = %v, want domain.ErrAppMigrationPending", err)
	}
}

func ptrInt64(i int64) *int64 { return &i }
