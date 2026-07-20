package registry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// ptrInt64 is a small helper for the nullable ref params.
func ptrInt64(v int64) *int64 { return &v }

// TestAddDanglingProxyRefIsInvalid pins U12-F1 at the registry level: adding an
// indexer that references a non-existent proxy trips the FK constraint
// (foreign_keys=ON) and must surface as registry.ErrInvalid (→ HTTP 400), not a
// raw sqlite error (→ 500). SolverID gets the same treatment.
func TestAddDanglingProxyRefIsInvalid(t *testing.T) {
	t.Parallel()

	reg, _ := newRegistry(t, nil)
	ctx := context.Background()

	_, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", ProxyID: ptrInt64(999999),
	})
	if err == nil {
		t.Fatal("Add with dangling proxyId succeeded, want an FK-classified error")
	}
	if !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("Add(dangling proxyId) err = %v, want errors.Is ErrInvalid (raw sqlite error would 500)", err)
	}

	_, err = reg.Add(ctx, registry.AddParams{
		Slug: "tt2", DefinitionID: "testtracker", SolverID: ptrInt64(888888),
	})
	if err == nil {
		t.Fatal("Add with dangling solverId succeeded, want an FK-classified error")
	}
	if !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("Add(dangling solverId) err = %v, want errors.Is ErrInvalid", err)
	}
}

// TestUpdateDanglingRefIsInvalid pins the same for Update: PATCHing an existing
// indexer to a non-existent proxy or solver trips the FK on SetRefs and must map
// to registry.ErrInvalid (→ 400), never a raw sqlite error (→ 500) — the same
// SetRefs contract TestAddDanglingProxyRefIsInvalid pins for Add.
func TestUpdateDanglingRefIsInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		update registry.UpdateParams
	}{
		{
			name:   "dangling proxyId",
			update: registry.UpdateParams{ProxyID: registry.RefUpdate{Present: true, Value: ptrInt64(999999)}},
		},
		{
			name:   "dangling solverId",
			update: registry.UpdateParams{SolverID: registry.RefUpdate{Present: true, Value: ptrInt64(888888)}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg, _ := newRegistry(t, nil)
			ctx := context.Background()
			if _, err := reg.Add(ctx, registry.AddParams{Slug: "tt", DefinitionID: "testtracker"}); err != nil {
				t.Fatalf("Add: %v", err)
			}

			err := reg.Update(ctx, "tt", tt.update)
			if err == nil {
				t.Fatalf("Update with %s succeeded, want an FK-classified error", tt.name)
			}
			if !errors.Is(err, registry.ErrInvalid) {
				t.Errorf("Update(%s) err = %v, want errors.Is ErrInvalid", tt.name, err)
			}
		})
	}
}

// TestAddValidProxyRefSucceeds guards against a false FK classification: an
// indexer referencing a proxy that exists must add cleanly.
func TestAddValidProxyRefSucceeds(t *testing.T) {
	t.Parallel()

	reg, db := newRegistry(t, nil)
	ctx := context.Background()

	res, err := db.ExecContext(ctx,
		`INSERT INTO proxies (name, type, host, port, password_encrypted, key_id, created_at, updated_at)
		 VALUES ('p1', 'http', 'proxy.example', 8080, 'enc', 'k1', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert proxy: %v", err)
	}
	proxyID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}

	inst, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", ProxyID: &proxyID,
	})
	if err != nil {
		t.Fatalf("Add with valid proxyId: %v", err)
	}
	if inst.ProxyID == nil || *inst.ProxyID != proxyID {
		t.Errorf("instance ProxyID = %v, want %d", inst.ProxyID, proxyID)
	}
}
