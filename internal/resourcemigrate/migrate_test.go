package resourcemigrate_test

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/resourcemigrate"
	"github.com/autobrr/harbrr/internal/secrets"
)

const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func setup(t *testing.T) (*database.DB, *secrets.Keyring) {
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
	return db, kr
}

var instRepo database.Instances

func addInstance(t *testing.T, db *database.DB, slug string) int64 {
	t.Helper()
	now := time.Now().UTC()
	id, err := instRepo.Insert(context.Background(), db, domain.IndexerInstance{
		Slug: slug, DefinitionID: "def", Name: slug, Enabled: true, Protocol: "torrent", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("insert instance %q: %v", slug, err)
	}
	return id
}

func addPlain(t *testing.T, db *database.DB, instID int64, name, val string) {
	t.Helper()
	if err := instRepo.InsertSetting(context.Background(), db, instID, domain.IndexerSetting{Name: name, Value: val}); err != nil {
		t.Fatalf("insert plain %q: %v", name, err)
	}
}

func addSecret(t *testing.T, db *database.DB, kr *secrets.Keyring, instID int64, name, val string) {
	t.Helper()
	enc, err := kr.Encrypt(instID, name, val)
	if err != nil {
		t.Fatalf("encrypt %q: %v", name, err)
	}
	if err := instRepo.InsertSetting(context.Background(), db, instID, domain.IndexerSetting{
		Name: name, ValueEncrypted: enc, KeyID: kr.KeyID(), IsSecret: true,
	}); err != nil {
		t.Fatalf("insert secret %q: %v", name, err)
	}
}

func TestMigrateFoldsDedupsAndPreservesCookie(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, kr := setup(t)

	// Two instances share the SAME proxy URL and the SAME FlareSolverr endpoint
	// (must dedup to one resource each). Both also carry inline settings to strip.
	for _, slug := range []string{"a", "b"} {
		id := addInstance(t, db, slug)
		addPlain(t, db, id, "proxy_type", "socks5")
		addSecret(t, db, kr, id, "proxy_url", "socks5://10.0.0.9:1080")
		addPlain(t, db, id, "solver_type", "flaresolverr")
		addSecret(t, db, kr, id, "flaresolverr_url", "http://flaresolverr:8191")
		addPlain(t, db, id, "flaresolverr_max_timeout", "120")
	}
	// A manual-cookie instance must be left entirely inline.
	cookieID := addInstance(t, db, "c")
	addPlain(t, db, cookieID, "solver_type", "manual_cookie")
	addSecret(t, db, kr, cookieID, "cookie", "uid=1; pass=secret")

	if err := resourcemigrate.Run(ctx, db, kr, time.Now, zerolog.Nop()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Dedup: one proxy, one solver despite two instances.
	proxies, _ := (database.Proxies{}).ListProxies(ctx, db)
	solvers, _ := (database.Solvers{}).ListSolvers(ctx, db)
	if len(proxies) != 1 || len(solvers) != 1 {
		t.Fatalf("resources = %d proxies, %d solvers; want 1 and 1", len(proxies), len(solvers))
	}
	if url, _ := kr.Decrypt(proxies[0].ID, domain.ProxySecretURL, proxies[0].URLEncrypted); url != "socks5://10.0.0.9:1080" {
		t.Errorf("proxy url = %q", url)
	}
	if solvers[0].MaxTimeout != 120 {
		t.Errorf("solver maxTimeout = %d, want 120", solvers[0].MaxTimeout)
	}

	// Both instances now reference the shared resources, and their inline settings are gone.
	for _, slug := range []string{"a", "b"} {
		inst, _ := instRepo.GetBySlug(ctx, db, slug)
		if inst.ProxyID == nil || *inst.ProxyID != proxies[0].ID || inst.SolverID == nil || *inst.SolverID != solvers[0].ID {
			t.Errorf("%s refs = proxy %v solver %v", slug, inst.ProxyID, inst.SolverID)
		}
		settings, _ := instRepo.Settings(ctx, db, inst.ID)
		for _, s := range settings {
			switch s.Name {
			case "proxy_type", "proxy_url", "solver_type", "flaresolverr_url", "flaresolverr_max_timeout":
				t.Errorf("%s still has inline setting %q", slug, s.Name)
			}
		}
	}

	// The manual-cookie instance is untouched: no refs, cookie + solver_type intact.
	cInst, _ := instRepo.GetBySlug(ctx, db, "c")
	if cInst.SolverID != nil || cInst.ProxyID != nil {
		t.Errorf("manual-cookie instance got refs: proxy %v solver %v", cInst.ProxyID, cInst.SolverID)
	}
	names := map[string]bool{}
	cSettings, _ := instRepo.Settings(ctx, db, cInst.ID)
	for _, s := range cSettings {
		names[s.Name] = true
	}
	if !names["solver_type"] || !names["cookie"] {
		t.Errorf("manual-cookie settings stripped: %v", names)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, kr := setup(t)
	id := addInstance(t, db, "a")
	addPlain(t, db, id, "proxy_type", "http")
	addSecret(t, db, kr, id, "proxy_url", "http://proxy:3128")

	for i := range 2 {
		if err := resourcemigrate.Run(ctx, db, kr, time.Now, zerolog.Nop()); err != nil {
			t.Fatalf("Run #%d: %v", i, err)
		}
	}
	proxies, _ := (database.Proxies{}).ListProxies(ctx, db)
	if len(proxies) != 1 {
		t.Fatalf("proxies = %d after two runs, want 1 (idempotent)", len(proxies))
	}
}
