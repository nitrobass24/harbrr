package download

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// newService builds a download.Service over an in-memory DB (exercising migration
// 0017 implicitly) with a fixed clock. The concrete keyring is returned so a test
// can decrypt the stored secret to prove it round trips (and is not stored in the
// clear).
func newService(t *testing.T) (*Service, *secrets.Keyring) {
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
	svc := NewService(db, kr, http.DefaultClient, zerolog.Nop())
	svc.clock = func() time.Time { return time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC) }
	return svc, kr
}

func ptrString(s string) *string { return &s }

func TestCreateEncryptsSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, kr := newService(t)

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
	if c.SecretEncrypted == secret || c.SecretEncrypted == "" {
		t.Errorf("secret stored in the clear (or empty): %q", c.SecretEncrypted)
	}
	got, err := kr.Decrypt(c.ID, domain.DownloadClientSecret, c.SecretEncrypted)
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
		{"unregistered kind", CreateParams{Name: "n", Kind: domain.DownloadClientKindDeluge, Host: "http://x.invalid"}},
		{"unknown kind", CreateParams{Name: "n", Kind: "bogus", Host: "http://x.invalid"}},
		{"relative host", CreateParams{Name: "n", Kind: domain.DownloadClientKindQBittorrent, Host: "/x"}},
		{"blank host", CreateParams{Name: "n", Kind: domain.DownloadClientKindQBittorrent, Host: ""}},
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

func TestUpdatePatchNilKeepsSecretNonNilRotates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, kr := newService(t)

	c, err := svc.Create(ctx, CreateParams{
		Name: "seedbox", Kind: domain.DownloadClientKindQBittorrent, Host: "http://x.invalid", Secret: "old",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// nil Secret leaves it untouched.
	if err := svc.Update(ctx, c.ID, UpdateParams{Name: ptrString("renamed")}); err != nil {
		t.Fatalf("Update (keep secret): %v", err)
	}
	got, err := svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "renamed" {
		t.Errorf("Name = %q, want renamed", got.Name)
	}
	secret, err := kr.Decrypt(got.ID, domain.DownloadClientSecret, got.SecretEncrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if secret != "old" {
		t.Errorf("secret after nil-patch = %q, want unchanged old", secret)
	}

	// non-nil Secret rotates it.
	if err := svc.Update(ctx, c.ID, UpdateParams{Secret: ptrString("new")}); err != nil {
		t.Fatalf("Update (rotate secret): %v", err)
	}
	got, err = svc.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	secret, err = kr.Decrypt(got.ID, domain.DownloadClientSecret, got.SecretEncrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if secret != "new" {
		t.Errorf("secret after rotate = %q, want new", secret)
	}
}

// TestValidateSettingsKindMismatch exercises the settings/kind cross-check
// directly: deluge is still unregistered, so a genuine mismatch against it
// can't be produced through Create/Update (validateKind rejects the
// unregistered kind first) — this is the only reachable path to that case.
func TestValidateSettingsKindMismatch(t *testing.T) {
	t.Parallel()
	settings := domain.DownloadClientSettings{QBittorrent: &domain.QBittorrentSettings{Category: "tv"}}
	if err := validateSettings(domain.DownloadClientKindQBittorrent, settings); err != nil {
		t.Errorf("matching kind: err = %v, want nil", err)
	}
	if err := validateSettings(domain.DownloadClientKindDeluge, settings); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("mismatched kind: err = %v, want domain.ErrInvalid", err)
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
	if c.Host != "" {
		t.Errorf("Host = %q, want empty", c.Host)
	}
	if c.Settings.Blackhole == nil || c.Settings.Blackhole.TorrentDir != "/watch/torrents" {
		t.Errorf("Settings.Blackhole = %+v, want TorrentDir /watch/torrents", c.Settings.Blackhole)
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
