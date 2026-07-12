package backup_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/backup"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

const (
	keyA = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	keyB = "202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f"

	proxySecret  = "http://user:PROXYSECRET@proxy:8080"
	solverSecret = "http://user:SOLVERSECRET@flare:8191"
	appKey       = "APPKEY-secret"
	appHarbrr    = "APPHARBRR-secret"
	annKey       = "ANNKEY-secret"
	annHarbrr    = "ANNHARBRR-secret"
	notifySecret = "https://hooks.example/NOTIFYSECRET"
	settingKey   = "INDEXERSECRET-value"
	passHash     = "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2g"
)

func openDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func openKeyring(t *testing.T, key string) *secrets.Keyring {
	t.Helper()
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: key}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return kr
}

// seed inserts one row of every backed-up table with representative secrets + FKs, sealed
// under kr, mirroring how the services persist. It returns nothing — the tests read the
// target back through the repos after a round-trip.
func seed(t *testing.T, db *database.DB, kr *secrets.Keyring) {
	t.Helper()
	ctx := context.Background()

	proxyID := seedProxy(t, db, kr)
	solverID := seedSolver(t, db, kr)
	profileID, err := (database.SyncProfiles{}).InsertProfile(ctx, db, domain.SyncProfile{
		Name: "tv", Categories: []int{5000}, EnableRss: true,
	})
	if err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	apiKeyID, err := (database.APIKeys{}).Create(ctx, db, domain.APIKey{Name: "feed", KeyHash: "hash-abc"})
	if err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	seedInstance(t, db, kr, proxyID, solverID)
	seedAppConn(t, db, kr, apiKeyID, profileID)
	seedAnnounceConn(t, db, kr, apiKeyID)
	seedNotification(t, db, kr)

	if err := (database.AppSettings{}).Set(ctx, db, "log.level", "debug", time.Now()); err != nil {
		t.Fatalf("seed app setting: %v", err)
	}
	if _, err := (database.Users{}).Create(ctx, db, domain.User{
		Username: "admin", PasswordHash: passHash, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
}

func seedProxy(t *testing.T, db *database.DB, kr *secrets.Keyring) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := (database.Proxies{}).InsertProxy(ctx, db, domain.Proxy{Name: "px", Type: "http", KeyID: kr.KeyID()})
	if err != nil {
		t.Fatalf("seed proxy: %v", err)
	}
	enc, err := kr.Encrypt(id, domain.ProxySecretURL, proxySecret)
	if err != nil {
		t.Fatalf("seal proxy: %v", err)
	}
	if err := (database.Proxies{}).SetProxySecret(ctx, db, id, enc, kr.KeyID()); err != nil {
		t.Fatalf("set proxy secret: %v", err)
	}
	return id
}

func seedSolver(t *testing.T, db *database.DB, kr *secrets.Keyring) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := (database.Solvers{}).InsertSolver(ctx, db, domain.Solver{Name: "fs", Type: "flaresolverr", KeyID: kr.KeyID(), MaxTimeout: 60})
	if err != nil {
		t.Fatalf("seed solver: %v", err)
	}
	enc, err := kr.Encrypt(id, domain.SolverSecretURL, solverSecret)
	if err != nil {
		t.Fatalf("seal solver: %v", err)
	}
	if err := (database.Solvers{}).SetSolverSecret(ctx, db, id, enc, kr.KeyID()); err != nil {
		t.Fatalf("set solver secret: %v", err)
	}
	return id
}

func seedInstance(t *testing.T, db *database.DB, kr *secrets.Keyring, proxyID, solverID int64) {
	t.Helper()
	ctx := context.Background()
	repo := database.Instances{}
	id, err := repo.Insert(ctx, db, domain.IndexerInstance{
		Slug: "tt", DefinitionID: "tt", Name: "TT", Enabled: true, Protocol: "torrent",
		ProxyID: &proxyID, SolverID: &solverID,
	})
	if err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	enc, err := kr.Encrypt(id, "apikey", settingKey)
	if err != nil {
		t.Fatalf("seal setting: %v", err)
	}
	if err := repo.InsertSetting(ctx, db, id, domain.IndexerSetting{Name: "apikey", ValueEncrypted: enc, KeyID: kr.KeyID(), IsSecret: true}); err != nil {
		t.Fatalf("seed secret setting: %v", err)
	}
	if err := repo.InsertSetting(ctx, db, id, domain.IndexerSetting{Name: "foo", Value: "bar"}); err != nil {
		t.Fatalf("seed plain setting: %v", err)
	}
}

func seedAppConn(t *testing.T, db *database.DB, kr *secrets.Keyring, apiKeyID, profileID int64) {
	t.Helper()
	ctx := context.Background()
	repo := database.AppConnections{}
	id, err := repo.InsertConnection(ctx, db, domain.AppConnection{
		Name: "sonarr", Kind: "sonarr", BaseURL: "http://sonarr:8989", HarbrrURL: "http://h:7478",
		HarbrrAPIKeyID: apiKeyID, KeyID: kr.KeyID(), Enabled: true, SyncLevel: "full",
		IndexScope: "all", FreeleechMode: "honor", Priority: 25, SyncProfileID: &profileID,
	})
	if err != nil {
		t.Fatalf("seed app conn: %v", err)
	}
	appEnc, _ := kr.Encrypt(id, "app", appKey)
	harbrrEnc, _ := kr.Encrypt(id, "harbrr", appHarbrr)
	if err := repo.SetConnectionSecrets(ctx, db, id, appEnc, harbrrEnc, kr.KeyID()); err != nil {
		t.Fatalf("set app conn secrets: %v", err)
	}
}

func seedAnnounceConn(t *testing.T, db *database.DB, kr *secrets.Keyring, apiKeyID int64) {
	t.Helper()
	ctx := context.Background()
	repo := database.AnnounceConnections{}
	id, err := repo.InsertAnnounceConnection(ctx, db, domain.AnnounceConnection{
		Name: "qui", Kind: "qui", BaseURL: "http://qui:7476", HarbrrURL: "http://h:7478",
		HarbrrAPIKeyID: apiKeyID, KeyID: kr.KeyID(), Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed announce conn: %v", err)
	}
	appEnc, _ := kr.Encrypt(id, "app", annKey)
	harbrrEnc, _ := kr.Encrypt(id, "harbrr", annHarbrr)
	if err := repo.SetAnnounceConnectionSecrets(ctx, db, id, appEnc, harbrrEnc, kr.KeyID()); err != nil {
		t.Fatalf("set announce conn secrets: %v", err)
	}
}

func seedNotification(t *testing.T, db *database.DB, kr *secrets.Keyring) {
	t.Helper()
	ctx := context.Background()
	repo := database.Notifications{}
	id, err := repo.InsertNotification(ctx, db, domain.Notification{Name: "wh", Type: "webhook", KeyID: kr.KeyID(), Enabled: true, OnHealthFailure: true})
	if err != nil {
		t.Fatalf("seed notification: %v", err)
	}
	enc, _ := kr.Encrypt(id, "url", notifySecret)
	if err := repo.SetNotificationSecret(ctx, db, id, enc, kr.KeyID()); err != nil {
		t.Fatalf("set notification secret: %v", err)
	}
}

// TestExportImportRoundTripAcrossKeys is the core gate: seed a source under key A, export
// with a passphrase, import into a fresh DB whose at-rest key is B, and verify every
// secret decrypts under B (proving the re-seal) with foreign keys remapped to the new ids.
func TestExportImportRoundTripAcrossKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	srcDB, srcKR := openDB(t), openKeyring(t, keyA)
	seed(t, srcDB, srcKR)

	bundle, err := backup.NewService(srcDB, srcKR, zerolog.Nop()).Export(ctx, backup.ExportParams{Passphrase: "pw"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	// The bundle is sealed: no plaintext secret survives in its bytes.
	for _, secret := range []string{"PROXYSECRET", "SOLVERSECRET", "NOTIFYSECRET", "APPKEY", "ANNKEY", "INDEXERSECRET", passHash} {
		if bytes.Contains(bundle, []byte(secret)) {
			t.Fatalf("bundle leaked plaintext %q", secret)
		}
	}

	dstDB, dstKR := openDB(t), openKeyring(t, keyB)
	if err := backup.NewService(dstDB, dstKR, zerolog.Nop()).Import(ctx, backup.ImportParams{Payload: bundle, Passphrase: "pw", Force: true}); err != nil {
		t.Fatalf("Import: %v", err)
	}

	assertProxyRestored(t, dstDB, dstKR, srcDB, srcKR)
	assertInstanceRestored(t, dstDB, dstKR)
	assertConnectionsRestored(t, dstDB, dstKR)
	assertNotificationRestored(t, dstDB, dstKR)
	assertAdminAndSettingsRestored(t, dstDB)
}

func assertProxyRestored(t *testing.T, dstDB *database.DB, dstKR *secrets.Keyring, srcDB *database.DB, srcKR *secrets.Keyring) {
	t.Helper()
	ctx := context.Background()
	proxies, _ := (database.Proxies{}).ListProxies(ctx, dstDB)
	if len(proxies) != 1 {
		t.Fatalf("restored proxies = %d, want 1", len(proxies))
	}
	url, err := dstKR.Decrypt(proxies[0].ID, domain.ProxySecretURL, proxies[0].URLEncrypted)
	if err != nil || url != proxySecret {
		t.Fatalf("proxy url under key B = %q, err %v; want %q", url, err, proxySecret)
	}
	// Re-seal proof: the target ciphertext differs from the source's (different at-rest key).
	srcProxies, _ := (database.Proxies{}).ListProxies(ctx, srcDB)
	if proxies[0].URLEncrypted == srcProxies[0].URLEncrypted {
		t.Error("proxy ciphertext identical across different at-rest keys (not re-sealed)")
	}
	_ = srcKR
}

func assertInstanceRestored(t *testing.T, dstDB *database.DB, dstKR *secrets.Keyring) {
	t.Helper()
	ctx := context.Background()
	repo := database.Instances{}
	list, _ := repo.List(ctx, dstDB)
	if len(list) != 1 {
		t.Fatalf("restored instances = %d, want 1", len(list))
	}
	inst := list[0]
	// proxy_id/solver_id remapped to the restored parents (points at a real proxy/solver).
	if inst.ProxyID == nil || inst.SolverID == nil {
		t.Fatalf("instance FKs not restored: proxy=%v solver=%v", inst.ProxyID, inst.SolverID)
	}
	if _, err := (database.Proxies{}).GetProxy(ctx, dstDB, *inst.ProxyID); err != nil {
		t.Errorf("instance.proxy_id dangling: %v", err)
	}
	settings, _ := repo.Settings(ctx, dstDB, inst.ID)
	got := map[string]string{}
	for _, s := range settings {
		if s.IsSecret {
			v, err := dstKR.Decrypt(inst.ID, s.Name, s.ValueEncrypted)
			if err != nil {
				t.Fatalf("decrypt setting %q: %v", s.Name, err)
			}
			got[s.Name] = v
		} else {
			got[s.Name] = s.Value
		}
	}
	if got["apikey"] != settingKey || got["foo"] != "bar" {
		t.Errorf("settings restored = %v, want apikey=%q foo=bar", got, settingKey)
	}
}

func assertConnectionsRestored(t *testing.T, dstDB *database.DB, dstKR *secrets.Keyring) {
	t.Helper()
	ctx := context.Background()
	apiKeys, _ := (database.APIKeys{}).List(ctx, dstDB)
	if len(apiKeys) != 1 {
		t.Fatalf("restored api keys = %d, want 1", len(apiKeys))
	}
	newAPIKeyID := apiKeys[0].ID

	apps, _ := (database.AppConnections{}).ListConnections(ctx, dstDB)
	if len(apps) != 1 {
		t.Fatalf("restored app connections = %d, want 1", len(apps))
	}
	ac := apps[0]
	if ac.HarbrrAPIKeyID != newAPIKeyID {
		t.Errorf("app conn harbrr_api_key_id = %d, want remapped %d", ac.HarbrrAPIKeyID, newAPIKeyID)
	}
	if ac.SyncProfileID == nil {
		t.Error("app conn sync_profile_id lost")
	}
	if v, err := dstKR.Decrypt(ac.ID, "app", ac.APIKeyEncrypted); err != nil || v != appKey {
		t.Errorf("app conn app key = %q, err %v; want %q", v, err, appKey)
	}
	if v, err := dstKR.Decrypt(ac.ID, "harbrr", ac.HarbrrAPIKeyEncrypted); err != nil || v != appHarbrr {
		t.Errorf("app conn harbrr key = %q, err %v; want %q", v, err, appHarbrr)
	}

	anns, _ := (database.AnnounceConnections{}).ListAnnounceConnections(ctx, dstDB)
	if len(anns) != 1 {
		t.Fatalf("restored announce connections = %d, want 1", len(anns))
	}
	an := anns[0]
	if an.HarbrrAPIKeyID != newAPIKeyID {
		t.Errorf("announce harbrr_api_key_id = %d, want %d", an.HarbrrAPIKeyID, newAPIKeyID)
	}
	if v, err := dstKR.Decrypt(an.ID, "app", an.APIKeyEncrypted); err != nil || v != annKey {
		t.Errorf("announce tool key = %q, err %v; want %q", v, err, annKey)
	}
}

func assertNotificationRestored(t *testing.T, dstDB *database.DB, dstKR *secrets.Keyring) {
	t.Helper()
	ctx := context.Background()
	list, _ := (database.Notifications{}).ListNotifications(ctx, dstDB)
	if len(list) != 1 {
		t.Fatalf("restored notifications = %d, want 1", len(list))
	}
	if v, err := dstKR.Decrypt(list[0].ID, "url", list[0].URLEncrypted); err != nil || v != notifySecret {
		t.Errorf("notification url = %q, err %v; want %q", v, err, notifySecret)
	}
}

func assertAdminAndSettingsRestored(t *testing.T, dstDB *database.DB) {
	t.Helper()
	ctx := context.Background()
	admin, err := (database.Users{}).GetAdmin(ctx, dstDB)
	if err != nil || admin.Username != "admin" || admin.PasswordHash != passHash {
		t.Errorf("admin = %+v, err %v; want username=admin with carried hash", admin, err)
	}
	v, found, err := (database.AppSettings{}).Get(ctx, dstDB, "log.level")
	if err != nil || !found || v != "debug" {
		t.Errorf("app setting log.level = %q found=%v err=%v; want debug", v, found, err)
	}
}

func TestImportWrongPassphraseFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, kr := openDB(t), openKeyring(t, keyA)
	seed(t, db, kr)
	bundle, err := backup.NewService(db, kr, zerolog.Nop()).Export(ctx, backup.ExportParams{Passphrase: "right"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	dst := openDB(t)
	err = backup.NewService(dst, openKeyring(t, keyB), zerolog.Nop()).Import(ctx, backup.ImportParams{Payload: bundle, Passphrase: "wrong", Force: true})
	if !errors.Is(err, backup.ErrInvalid) {
		t.Fatalf("Import(wrong passphrase) err = %v, want ErrInvalid", err)
	}
	// A failed import touched nothing.
	if n, _ := (database.Proxies{}).ListProxies(ctx, dst); len(n) != 0 {
		t.Errorf("failed import left %d proxies, want 0 (rolled back)", len(n))
	}
}

func TestImportForceGuard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src, srcKR := openDB(t), openKeyring(t, keyA)
	seed(t, src, srcKR)
	bundle, _ := backup.NewService(src, srcKR, zerolog.Nop()).Export(ctx, backup.ExportParams{Passphrase: "pw"})

	// A configured target refuses import without force.
	dst, dstKR := openDB(t), openKeyring(t, keyB)
	seed(t, dst, dstKR)
	svc := backup.NewService(dst, dstKR, zerolog.Nop())
	if err := svc.Import(ctx, backup.ImportParams{Payload: bundle, Passphrase: "pw"}); !errors.Is(err, backup.ErrConflict) {
		t.Fatalf("Import(no force, non-empty) err = %v, want ErrConflict", err)
	}
	// With force it replaces (still exactly one of each, not doubled).
	if err := svc.Import(ctx, backup.ImportParams{Payload: bundle, Passphrase: "pw", Force: true}); err != nil {
		t.Fatalf("Import(force): %v", err)
	}
	if list, _ := (database.Proxies{}).ListProxies(ctx, dst); len(list) != 1 {
		t.Errorf("after force import proxies = %d, want 1 (replaced, not appended)", len(list))
	}
}

func TestImportRejectsForeignBundle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := backup.NewService(openDB(t), openKeyring(t, keyA), zerolog.Nop())
	for _, payload := range []string{`not json`, `{"schemaVersion":999,"salt":"","payload":""}`} {
		if err := svc.Import(ctx, backup.ImportParams{Payload: []byte(payload), Passphrase: "pw", Force: true}); !errors.Is(err, backup.ErrInvalid) {
			t.Errorf("Import(%q) err = %v, want ErrInvalid", payload, err)
		}
	}
}

func TestExportRequiresPassphrase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := backup.NewService(openDB(t), openKeyring(t, keyA), zerolog.Nop())
	if _, err := svc.Export(ctx, backup.ExportParams{Passphrase: "  "}); !errors.Is(err, backup.ErrInvalid) {
		t.Errorf("Export(blank passphrase) err = %v, want ErrInvalid", err)
	}
	if err := svc.Import(ctx, backup.ImportParams{Payload: []byte(`{}`), Passphrase: ""}); !errors.Is(err, backup.ErrInvalid) {
		t.Errorf("Import(blank passphrase) err = %v, want ErrInvalid", err)
	}
}
