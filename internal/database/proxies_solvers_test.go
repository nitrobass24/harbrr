package database_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

func TestProxyRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.Proxies{}
	now := time.Now().UTC().Truncate(time.Second)

	// Two-phase insert: row first (mints id), then the sealed secret.
	id, err := repo.InsertProxy(ctx, db, domain.Proxy{
		Name: "home", Type: domain.ProxyTypeSOCKS5, URLEncrypted: "", KeyID: "", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertProxy: %v", err)
	}
	if err := repo.SetProxySecret(ctx, db, id, "enc(url)", "key-1"); err != nil {
		t.Fatalf("SetProxySecret: %v", err)
	}

	got, err := repo.GetProxy(ctx, db, id)
	if err != nil {
		t.Fatalf("GetProxy: %v", err)
	}
	if got.Name != "home" || got.Type != domain.ProxyTypeSOCKS5 || got.URLEncrypted != "enc(url)" || got.KeyID != "key-1" {
		t.Fatalf("GetProxy = %+v", got)
	}

	got.Name, got.Type, got.URLEncrypted, got.UpdatedAt = "work", domain.ProxyTypeHTTP, "enc(url2)", now.Add(time.Minute)
	if err := repo.UpdateProxy(ctx, db, got); err != nil {
		t.Fatalf("UpdateProxy: %v", err)
	}
	after, _ := repo.GetProxy(ctx, db, id)
	if after.Name != "work" || after.Type != domain.ProxyTypeHTTP || after.URLEncrypted != "enc(url2)" {
		t.Fatalf("after update = %+v", after)
	}

	list, err := repo.ListProxies(ctx, db)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListProxies = %v, %d rows", err, len(list))
	}

	if err := repo.DeleteProxy(ctx, db, id); err != nil {
		t.Fatalf("DeleteProxy: %v", err)
	}
	if _, err := repo.GetProxy(ctx, db, id); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("GetProxy after delete = %v, want ErrNotFound", err)
	}
}

func TestSolverRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.Solvers{}
	now := time.Now().UTC().Truncate(time.Second)

	id, err := repo.InsertSolver(ctx, db, domain.Solver{
		Name: "fs", Type: domain.SolverTypeFlaresolverr, MaxTimeout: 90, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertSolver: %v", err)
	}
	if err := repo.SetSolverSecret(ctx, db, id, "enc(url)", "key-1"); err != nil {
		t.Fatalf("SetSolverSecret: %v", err)
	}

	got, err := repo.GetSolver(ctx, db, id)
	if err != nil {
		t.Fatalf("GetSolver: %v", err)
	}
	if got.Type != domain.SolverTypeFlaresolverr || got.MaxTimeout != 90 || got.URLEncrypted != "enc(url)" {
		t.Fatalf("GetSolver = %+v", got)
	}

	if err := repo.DeleteSolver(ctx, db, id); err != nil {
		t.Fatalf("DeleteSolver: %v", err)
	}
	if _, err := repo.GetSolver(ctx, db, id); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("GetSolver after delete = %v, want ErrNotFound", err)
	}
}

// TestInstanceRefsSetAndCleared covers the proxy_id/solver_id FK round-trip and
// the ON DELETE SET NULL behavior (foreign_keys pragma is ON).
func TestInstanceRefsSetAndCleared(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	instances := database.Instances{}
	proxies := database.Proxies{}
	now := time.Now().UTC().Truncate(time.Second)

	instID, err := instances.Insert(ctx, db, domain.IndexerInstance{
		Slug: "ix", DefinitionID: "def", Name: "IX", Enabled: true, Protocol: "torrent", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("Insert instance: %v", err)
	}
	proxyID, err := proxies.InsertProxy(ctx, db, domain.Proxy{Name: "p", Type: domain.ProxyTypeHTTP, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("InsertProxy: %v", err)
	}

	if err := instances.SetRefs(ctx, db, instID, &proxyID, nil, now); err != nil {
		t.Fatalf("SetRefs: %v", err)
	}
	got, _ := instances.GetByID(ctx, db, instID)
	if got.ProxyID == nil || *got.ProxyID != proxyID || got.SolverID != nil {
		t.Fatalf("refs after SetRefs = proxy %v solver %v", got.ProxyID, got.SolverID)
	}

	// Deleting the proxy nulls the instance reference (ON DELETE SET NULL).
	if err := proxies.DeleteProxy(ctx, db, proxyID); err != nil {
		t.Fatalf("DeleteProxy: %v", err)
	}
	got, _ = instances.GetByID(ctx, db, instID)
	if got.ProxyID != nil {
		t.Fatalf("proxy_id = %v after resource delete, want nil (ON DELETE SET NULL)", *got.ProxyID)
	}
}
