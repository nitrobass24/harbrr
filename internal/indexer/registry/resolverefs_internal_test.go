package registry

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/secrets"
)

const resolveTestKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func newResolveRegistry(t *testing.T) (*Registry, *secrets.Keyring, *database.DB) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: resolveTestKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return New(db, loader.New(t.TempDir()), kr), kr, db
}

// seedProxy inserts a proxy with its URL sealed under the proxy's own id (the
// two-phase write the service performs), returning the new id.
func seedProxy(t *testing.T, db *database.DB, kr *secrets.Keyring, typ, url string) int64 {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	id, err := (database.Proxies{}).InsertProxy(ctx, db, domain.Proxy{Name: "p", Type: typ, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("InsertProxy: %v", err)
	}
	enc, err := kr.Encrypt(id, domain.ProxySecretURL, url)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := (database.Proxies{}).SetProxySecret(ctx, db, id, enc, kr.KeyID()); err != nil {
		t.Fatalf("SetProxySecret: %v", err)
	}
	return id
}

func TestResolveResourceRefs(t *testing.T) {
	t.Parallel()
	reg, kr, db := newResolveRegistry(t)
	ctx := context.Background()

	proxyID := seedProxy(t, db, kr, domain.ProxyTypeSOCKS5, "socks5://10.0.0.9:1080")
	solverID, err := (database.Solvers{}).InsertSolver(ctx, db, domain.Solver{
		Name: "fs", Type: domain.SolverTypeFlaresolverr, MaxTimeout: 120, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("InsertSolver: %v", err)
	}
	senc, _ := kr.Encrypt(solverID, domain.SolverSecretURL, "http://flaresolverr:8191")
	if err := (database.Solvers{}).SetSolverSecret(ctx, db, solverID, senc, kr.KeyID()); err != nil {
		t.Fatalf("SetSolverSecret: %v", err)
	}

	t.Run("refs merge decrypted values into cfg", func(t *testing.T) {
		cfg := map[string]string{}
		inst := domain.IndexerInstance{ProxyID: &proxyID, SolverID: &solverID}
		if err := reg.resolveResourceRefs(ctx, inst, cfg); err != nil {
			t.Fatalf("resolveResourceRefs: %v", err)
		}
		if cfg["proxy_type"] != domain.ProxyTypeSOCKS5 || cfg["proxy_url"] != "socks5://10.0.0.9:1080" {
			t.Errorf("proxy cfg = %q %q", cfg["proxy_type"], cfg["proxy_url"])
		}
		if cfg["solver_type"] != domain.SolverTypeFlaresolverr || cfg["flaresolverr_url"] != "http://flaresolverr:8191" || cfg["flaresolverr_max_timeout"] != "120" {
			t.Errorf("solver cfg = %q %q %q", cfg["solver_type"], cfg["flaresolverr_url"], cfg["flaresolverr_max_timeout"])
		}
	})

	t.Run("no refs leave the inline fallback untouched", func(t *testing.T) {
		cfg := map[string]string{"proxy_type": "http", "proxy_url": "http://inline:3128"}
		if err := reg.resolveResourceRefs(ctx, domain.IndexerInstance{}, cfg); err != nil {
			t.Fatalf("resolveResourceRefs: %v", err)
		}
		if cfg["proxy_url"] != "http://inline:3128" {
			t.Errorf("inline proxy_url overwritten: %q", cfg["proxy_url"])
		}
	})

	t.Run("dangling ref is skipped, not fatal", func(t *testing.T) {
		missing := int64(999999)
		cfg := map[string]string{}
		if err := reg.resolveResourceRefs(ctx, domain.IndexerInstance{ProxyID: &missing}, cfg); err != nil {
			t.Fatalf("dangling ref should be skipped, got: %v", err)
		}
		if _, ok := cfg["proxy_url"]; ok {
			t.Errorf("dangling ref set proxy_url = %q", cfg["proxy_url"])
		}
	})
}
