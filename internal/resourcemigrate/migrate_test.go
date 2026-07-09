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

// addCorruptSecret inserts a settings row whose ValueEncrypted is not valid
// ciphertext under the keyring, so any later decrypt of it fails. It carries no real
// secret — the value exists only to trip the decrypt path.
func addCorruptSecret(t *testing.T, db *database.DB, kr *secrets.Keyring, instID int64, name string) {
	t.Helper()
	if err := instRepo.InsertSetting(context.Background(), db, instID, domain.IndexerSetting{
		Name: name, ValueEncrypted: "not-valid-ciphertext", KeyID: kr.KeyID(), IsSecret: true,
	}); err != nil {
		t.Fatalf("insert corrupt secret %q: %v", name, err)
	}
}

// TestMigrateRollsBackOnDecryptFailure exercises the rollback invariant the Run doc
// promises (~44-46): a secret that can't be decrypted mid-migration must roll the
// whole transaction back — no partial resources, the done flag stays unset so the
// next boot retries, and the instance's inline settings survive intact. The instance
// here folds its proxy FIRST (creating a resource + stripping its inline settings
// inside the tx); its corrupt flaresolverr_url then fails decrypt, so that
// already-applied proxy work is what the rollback must undo.
func TestMigrateRollsBackOnDecryptFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, kr := setup(t)

	id := addInstance(t, db, "a")
	// A valid inline proxy that folds successfully (resource created, settings stripped)...
	addPlain(t, db, id, "proxy_type", "socks5")
	addSecret(t, db, kr, id, "proxy_url", "socks5://10.0.0.9:1080")
	// ...then a FlareSolverr solver whose URL ciphertext is corrupt: decrypt fails.
	addPlain(t, db, id, "solver_type", "flaresolverr")
	addCorruptSecret(t, db, kr, id, "flaresolverr_url")

	if err := resourcemigrate.Run(ctx, db, kr, time.Now, zerolog.Nop()); err == nil {
		t.Fatal("Run: want error from the corrupt secret, got nil")
	}

	// No partial resources: the proxy fold that ran first must be rolled back too.
	proxies, _ := (database.Proxies{}).ListProxies(ctx, db)
	solvers, _ := (database.Solvers{}).ListSolvers(ctx, db)
	if len(proxies) != 0 || len(solvers) != 0 {
		t.Fatalf("partial state after rollback: %d proxies, %d solvers; want 0 and 0", len(proxies), len(solvers))
	}

	// The done flag ("inline_proxy_solver_migrated", the unexported doneFlag) must stay
	// unset so the migration retries next boot; a committed rollback would have set it.
	if _, ok, err := (database.AppMeta{}).Get(ctx, db, "inline_proxy_solver_migrated"); err != nil {
		t.Fatalf("AppMeta.Get: %v", err)
	} else if ok {
		t.Error("done flag set after a rolled-back migration; want unset (should retry)")
	}

	// The instance keeps its inline settings and gains no refs — the fold is fully undone.
	inst, _ := instRepo.GetBySlug(ctx, db, "a")
	if inst.ProxyID != nil || inst.SolverID != nil {
		t.Errorf("instance got refs despite rollback: proxy %v solver %v", inst.ProxyID, inst.SolverID)
	}
	names := map[string]bool{}
	settings, _ := instRepo.Settings(ctx, db, inst.ID)
	for _, s := range settings {
		names[s.Name] = true
	}
	for _, n := range []string{"proxy_type", "proxy_url", "solver_type", "flaresolverr_url"} {
		if !names[n] {
			t.Errorf("inline setting %q was stripped despite rollback", n)
		}
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

// TestMigrateSkipsAlreadyWiredSlot covers the retry-window guard: an instance that
// already references a resource (e.g. the operator wired it via the API after a
// transient first-run failure) must NOT be re-folded — no duplicate resource, and
// the explicit reference is preserved.
func TestMigrateSkipsAlreadyWiredSlot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, kr := setup(t)

	// A pre-existing chosen proxy resource, referenced by the instance.
	now := time.Now().UTC()
	chosen, err := (database.Proxies{}).InsertProxy(ctx, db, domain.Proxy{Name: "chosen", Type: domain.ProxyTypeHTTP, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("InsertProxy: %v", err)
	}
	id := addInstance(t, db, "a")
	if err := instRepo.SetRefs(ctx, db, id, &chosen, nil, now); err != nil {
		t.Fatalf("SetRefs: %v", err)
	}
	// ...but the instance also still carries leftover inline proxy settings (the
	// transient-failure state that left both a ref and inline config).
	addPlain(t, db, id, "proxy_type", "socks5")
	addSecret(t, db, kr, id, "proxy_url", "socks5://leftover:1080")

	if err := resourcemigrate.Run(ctx, db, kr, time.Now, zerolog.Nop()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// No duplicate resource, and the instance still points at the chosen one.
	proxies, _ := (database.Proxies{}).ListProxies(ctx, db)
	if len(proxies) != 1 || proxies[0].ID != chosen {
		t.Fatalf("proxies = %d (want 1, the chosen one), first id %d", len(proxies), func() int64 {
			if len(proxies) > 0 {
				return proxies[0].ID
			}
			return -1
		}())
	}
	inst, _ := instRepo.GetBySlug(ctx, db, "a")
	if inst.ProxyID == nil || *inst.ProxyID != chosen {
		t.Fatalf("proxy_id = %v, want the chosen resource %d", inst.ProxyID, chosen)
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
