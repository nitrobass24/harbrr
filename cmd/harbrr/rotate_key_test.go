package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/app"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/notify"
	"github.com/autobrr/harbrr/internal/proxy"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/solver"
)

// keyring builds an encrypting keyring from a synthetic 32-byte hex key (allowed
// in *_test.go per AGENTS.md). b is a 2-hex-char byte repeated to 32 bytes.
func keyring(t *testing.T, b string) *secrets.Keyring {
	t.Helper()
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: strings.Repeat(b, 32)}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open keyring: %v", err)
	}
	return kr
}

// seedEncryptedStore builds a migrated DB with one instance, a real + an empty
// secret encrypted under kr, a plaintext setting, and the canary/key_id under kr.
func seedEncryptedStore(t *testing.T, kr *secrets.Keyring) *database.DB {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(filepath.Join(t.TempDir(), "rot.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ins := database.Instances{}
	id, err := ins.Insert(ctx, db, domain.IndexerInstance{
		Slug: "tt", DefinitionID: "def", Name: "tt", Enabled: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	blob, err := kr.Encrypt(id, "apikey", "SECRET-VALUE")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	for _, s := range []domain.IndexerSetting{
		{Name: "apikey", ValueEncrypted: blob, KeyID: kr.KeyID(), IsSecret: true},
		{Name: "cookie", ValueEncrypted: "", KeyID: kr.KeyID(), IsSecret: true}, // empty secret
		{Name: "sort", Value: "seeders"},
	} {
		if err := ins.InsertSetting(ctx, db, id, s); err != nil {
			t.Fatalf("insert %q: %v", s.Name, err)
		}
	}
	meta := database.AppMeta{}
	canary, err := kr.EncryptCanary()
	if err != nil {
		t.Fatalf("canary: %v", err)
	}
	if err := meta.Set(ctx, db, app.CanaryBlobKey, canary); err != nil {
		t.Fatalf("set canary: %v", err)
	}
	if err := meta.Set(ctx, db, app.CanaryIDKey, kr.KeyID()); err != nil {
		t.Fatalf("set key id: %v", err)
	}
	return db
}

func TestRotateKeys_Success(t *testing.T) {
	t.Parallel()
	krA, krB := keyring(t, "a1"), keyring(t, "b2")
	db := seedEncryptedStore(t, krA)
	ctx := context.Background()

	rep, err := rotateKeys(ctx, db, krA, krB, false)
	if err != nil {
		t.Fatalf("rotateKeys: %v", err)
	}
	if rep.rows != 2 {
		t.Errorf("rotated rows = %d, want 2 (apikey + empty cookie)", rep.rows)
	}

	rows, err := (database.Rotation{}).AllSecrets(ctx, db)
	if err != nil {
		t.Fatalf("AllSecrets: %v", err)
	}
	for _, r := range rows {
		if r.ValueEncrypted == "" {
			continue // empty secret stays empty
		}
		pt, derr := krB.Decrypt(r.InstanceID, r.Name, r.ValueEncrypted)
		if derr != nil {
			t.Fatalf("decrypt %q under new key: %v", r.Name, derr)
		}
		if r.Name == "apikey" && pt != "SECRET-VALUE" {
			t.Errorf("apikey = %q, want SECRET-VALUE", pt)
		}
		// must NOT still decrypt under the old key
		if _, oerr := krA.Decrypt(r.InstanceID, r.Name, r.ValueEncrypted); oerr == nil {
			t.Errorf("%q still decrypts under the OLD key after rotation", r.Name)
		}
	}

	meta := database.AppMeta{}
	storedID, _, _ := meta.Get(ctx, db, app.CanaryIDKey)
	if storedID != krB.KeyID() {
		t.Errorf("stored key id = %q, want new %q", storedID, krB.KeyID())
	}
	blob, _, _ := meta.Get(ctx, db, app.CanaryBlobKey)
	if err := krB.VerifyCanary(storedID, blob); err != nil {
		t.Errorf("canary fails to verify under new key: %v", err)
	}
}

// surfaceSecret is one seeded fixed-AAD ciphertext to prove rotate-key re-encrypts
// it: its table/column, the AAD discriminator + row id it was sealed under, and the
// plaintext it must still decrypt to under the NEW key after rotation.
type surfaceSecret struct {
	table, col, setting string
	id                  int64
	want                string
}

// seedAllSurfaces seeds one real ciphertext row in EVERY fixed-AAD surface column
// under kr — proxies/solvers/notifications through their actual service seal paths
// (so the AAD comes from production code), and the two connection tables via direct
// SQL (their services need heavy deps). Returns the expectations to verify.
func seedAllSurfaces(t *testing.T, db *database.DB, kr *secrets.Keyring) []surfaceSecret {
	t.Helper()
	ctx := context.Background()

	px, err := proxy.NewService(db, kr).Create(ctx, proxy.CreateParams{
		Name: "p", Type: domain.ProxyTypeHTTP, Host: "proxy", Port: 8080, Username: "user", Password: "pass",
	})
	if err != nil {
		t.Fatalf("seed proxy: %v", err)
	}
	sv, err := solver.NewService(db, kr).Create(ctx, solver.CreateParams{
		Name: "s", Type: domain.SolverTypeFlaresolverr, URL: "http://flare:8191",
	})
	if err != nil {
		t.Fatalf("seed solver: %v", err)
	}
	nt, err := notify.NewService(db, kr, &http.Client{}, zerolog.Nop()).CreateNotification(ctx, notify.CreateNotificationParams{
		Name: "n", Type: domain.NotifyTypeWebhook, URL: "http://hook/token-abc",
	})
	if err != nil {
		t.Fatalf("seed notification: %v", err)
	}

	// apps + app_connections + announce_connections: since #269 the app/tool credential
	// lives solely on the apps row (sealed under the App's own id, domain.AppSecret="app");
	// each connection row seals only its minted harbrr key with the connection id as AAD.
	appSonarrID := seedAppRow(t, db, kr, "sonarr", "http://sonarr")
	seedConnRow(t, db, kr, "app_connections", appSonarrID,
		`INSERT INTO app_connections (id,name,kind,app_id,harbrr_api_key_encrypted,key_id,created_at,updated_at)
		 VALUES (1,'ac','sonarr',?,?,?,?,?)`)
	appCrossSeedID := seedAppRow(t, db, kr, "cross-seed", "http://xseed")
	seedConnRow(t, db, kr, "announce_connections", appCrossSeedID,
		`INSERT INTO announce_connections (id,name,kind,app_id,harbrr_api_key_encrypted,key_id,created_at,updated_at)
		 VALUES (1,'nc','cross-seed',?,?,?,?,?)`)

	return []surfaceSecret{
		{table: "proxies", col: "password_encrypted", setting: domain.ProxySecretPassword, id: px.ID, want: "pass"},
		{table: "solvers", col: "url_encrypted", setting: domain.SolverSecretURL, id: sv.ID, want: "http://flare:8191"},
		{table: "notifications", col: "url_encrypted", setting: "url", id: nt.ID, want: "http://hook/token-abc"},
		{table: "apps", col: "api_key_encrypted", setting: domain.AppSecret, id: appSonarrID, want: "APP-KEY-sonarr"},
		{table: "app_connections", col: "harbrr_api_key_encrypted", setting: "harbrr", id: 1, want: "HARBRR-KEY-app_connections"},
		{table: "apps", col: "api_key_encrypted", setting: domain.AppSecret, id: appCrossSeedID, want: "APP-KEY-cross-seed"},
		{table: "announce_connections", col: "harbrr_api_key_encrypted", setting: "harbrr", id: 1, want: "HARBRR-KEY-announce_connections"},
	}
}

// seedAppRow seeds+seals an apps row with a real credential under kr, keyed by kind
// (mirrors internal/apps.Service's insert-then-seal write). Returns the new App's id.
func seedAppRow(t *testing.T, db *database.DB, kr *secrets.Keyring, kind, baseURL string) int64 {
	t.Helper()
	ctx := context.Background()
	ts := time.Now().UTC().Format(time.RFC3339)
	res, err := db.ExecContext(ctx, db.Rebind(
		`INSERT INTO apps (kind,name,base_url,api_key_encrypted,key_id,created_at,updated_at) VALUES (?,?,?,'','',?,?)`,
	),
		kind, kind, baseURL, ts, ts)
	if err != nil {
		t.Fatalf("seed app %s: %v", kind, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("app %s last insert id: %v", kind, err)
	}
	enc, err := kr.Encrypt(id, domain.AppSecret, "APP-KEY-"+kind)
	if err != nil {
		t.Fatalf("seal app %s: %v", kind, err)
	}
	if _, err := db.ExecContext(ctx, db.Rebind(`UPDATE apps SET api_key_encrypted = ?, key_id = ? WHERE id = ?`),
		enc, kr.KeyID(), id); err != nil {
		t.Fatalf("set app %s secret: %v", kind, err)
	}
	return id
}

// seedConnRow inserts one connection row referencing appID, sealing only its minted
// harbrr key under kr with the row id as AAD id and the "harbrr" discriminator (the
// app/tool credential lives on the App — see seedAppRow).
func seedConnRow(t *testing.T, db *database.DB, kr *secrets.Keyring, table string, appID int64, insert string) {
	t.Helper()
	ctx := context.Background()
	harbrrEnc, err := kr.Encrypt(1, "harbrr", "HARBRR-KEY-"+table)
	if err != nil {
		t.Fatalf("seed %s harbrr secret: %v", table, err)
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.ExecContext(ctx, db.Rebind(insert), appID, harbrrEnc, kr.KeyID(), ts, ts); err != nil {
		t.Fatalf("insert %s: %v", table, err)
	}
}

// TestRotateKeys_AllSurfaces is the U8-F1 regression guard: it seeds a ciphertext in
// every non-indexer secret column (the fixed-AAD tables rotate-key used to skip —
// proxies/solvers/notifications, plus apps/app_connections/announce_connections since
// #269 moved the app/tool credential onto apps), rotates old→new, and asserts each one
// still decrypts under the NEW key with its service's AAD, no longer decrypts under the
// OLD key, and had its key_id rotated. Before the original fix, rotate-key never touched
// these rows, so the new-key decrypts would fail.
func TestRotateKeys_AllSurfaces(t *testing.T) {
	t.Parallel()
	krA, krB := keyring(t, "a1"), keyring(t, "b2")
	db := seedEncryptedStore(t, krA)
	ctx := context.Background()
	want := seedAllSurfaces(t, db, krA)

	rep, err := rotateKeys(ctx, db, krA, krB, false)
	if err != nil {
		t.Fatalf("rotateKeys: %v", err)
	}
	// 2 indexer_settings (apikey + empty cookie) + 7 surface rows (proxies, solvers,
	// notifications, 2 apps, app_connections, announce_connections).
	if rep.rows != 9 {
		t.Errorf("rotated rows = %d, want 9", rep.rows)
	}

	for _, w := range want {
		blob, keyID := readSurfaceCipher(t, db, w)
		pt, derr := krB.Decrypt(w.id, w.setting, blob)
		if derr != nil {
			t.Errorf("%s.%s did not rotate: decrypt under NEW key failed: %v", w.table, w.col, derr)
			continue
		}
		if pt != w.want {
			t.Errorf("%s.%s = %q, want %q", w.table, w.col, pt, w.want)
		}
		if _, oerr := krA.Decrypt(w.id, w.setting, blob); oerr == nil {
			t.Errorf("%s.%s still decrypts under the OLD key after rotation", w.table, w.col)
		}
		if keyID != krB.KeyID() {
			t.Errorf("%s.%s key_id = %q, want new %q", w.table, w.col, keyID, krB.KeyID())
		}
	}
}

// readSurfaceCipher reads one surface row's ciphertext column + key_id after rotation.
func readSurfaceCipher(t *testing.T, db *database.DB, w surfaceSecret) (blob, keyID string) {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		db.Rebind("SELECT "+w.col+", key_id FROM "+w.table+" WHERE id = ?"), w.id)
	if err != nil {
		t.Fatalf("read %s.%s: %v", w.table, w.col, err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no row %s id=%d", w.table, w.id)
	}
	if err := rows.Scan(&blob, &keyID); err != nil {
		t.Fatalf("scan %s.%s: %v", w.table, w.col, err)
	}
	return blob, keyID
}

func TestRotateKeys_WrongOldKeyFailsBeforeWrite(t *testing.T) {
	t.Parallel()
	krA, krB, krC := keyring(t, "a1"), keyring(t, "b2"), keyring(t, "c3")
	db := seedEncryptedStore(t, krA)
	ctx := context.Background()

	if _, err := rotateKeys(ctx, db, krC, krB, false); err == nil {
		t.Fatal("want error for a wrong old key")
	}
	storedID, _, _ := (database.AppMeta{}).Get(ctx, db, app.CanaryIDKey)
	if storedID != krA.KeyID() {
		t.Errorf("store mutated on wrong-key rotate: key id = %q, want old", storedID)
	}
}

func TestRotateKeys_DryRunNoWrites(t *testing.T) {
	t.Parallel()
	krA, krB := keyring(t, "a1"), keyring(t, "b2")
	db := seedEncryptedStore(t, krA)
	ctx := context.Background()

	rep, err := rotateKeys(ctx, db, krA, krB, true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if rep.rows != 2 {
		t.Errorf("dry-run rows = %d, want 2", rep.rows)
	}
	storedID, _, _ := (database.AppMeta{}).Get(ctx, db, app.CanaryIDKey)
	if storedID != krA.KeyID() {
		t.Errorf("dry-run mutated the store: key id = %q, want old", storedID)
	}
}

func TestRotateKeys_PlaintextRejected(t *testing.T) {
	t.Parallel()
	krA := keyring(t, "a1")
	db := seedEncryptedStore(t, krA)
	plain, err := secrets.OpenKeyring(secrets.KeyringOptions{AllowPlaintext: true}, zerolog.Nop())
	if err != nil {
		t.Fatalf("plaintext keyring: %v", err)
	}
	if _, err := rotateKeys(context.Background(), db, plain, krA, false); err == nil {
		t.Fatal("want error rotating in plaintext mode")
	}
}
