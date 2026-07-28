package backup

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/apps"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/secrets"
)

// legacyTestKey is a synthetic (non-real) 32-byte hex encryption key — present only to
// exercise the keyring in this test file, never a live credential.
const legacyTestKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// newLegacyTestService wires a Service with a working apps.Service + keyring — needed
// because a legacy AppConnRow restore resolves/seals through both (unlike the
// instances-only restore_priority_internal_test.go fixture).
func newLegacyTestService(t *testing.T) (*Service, *database.DB) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: legacyTestKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return &Service{db: db, apps: apps.NewService(db, kr, nil, zerolog.Nop()), keyring: kr}, db
}

// TestRestoreLegacySelectedConnectionMintsRoutingProfile proves a pre-#365 bundle's
// index_scope="selected" connection mints a routing profile from its ledger selection
// (remapped to the restored instance ids) and points the connection at it — the
// operator's routing intent must survive even though index_scope stops being read.
func TestRestoreLegacySelectedConnectionMintsRoutingProfile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, db := newLegacyTestService(t)

	tables := &Tables{
		IndexerInstances: []InstanceRow{
			{ID: 10, Slug: "alpha", DefinitionID: "alpha", Name: "Alpha", Enabled: true, Protocol: "torrent"},
			{ID: 20, Slug: "beta", DefinitionID: "beta", Name: "Beta", Enabled: true, Protocol: "torrent"},
			{ID: 30, Slug: "gamma", DefinitionID: "gamma", Name: "Gamma", Enabled: true, Protocol: "torrent"},
		},
		AppConnections: []AppConnRow{
			{
				ID: 1, Name: "Sonarr", Kind: "sonarr", BaseURL: "http://sonarr:8989",
				APIKey: "k", HarbrrURL: "http://h:7478", Enabled: true, SyncLevel: "full",
				FreeleechMode: "honor",
				IndexScope:    "selected", SelectedInstanceIDs: []int64{10, 30}, // beta (20) excluded
			},
		},
	}
	if err := svc.restore(ctx, tables, true); err != nil {
		t.Fatalf("restore: %v", err)
	}

	conns, err := (database.AppConnections{}).ListConnections(ctx, db)
	if err != nil || len(conns) != 1 {
		t.Fatalf("list connections: %v (%d rows)", err, len(conns))
	}
	conn := conns[0]
	if conn.SyncProfileID == nil {
		t.Fatal("restored connection has no sync profile reference — legacy selection lost")
	}

	profile, err := (database.SyncProfiles{}).GetProfile(ctx, db, *conn.SyncProfileID)
	if err != nil {
		t.Fatalf("get minted profile: %v", err)
	}
	if profile.Name != "Sonarr indexers (restored)" {
		t.Errorf("minted profile name = %q, want %q", profile.Name, "Sonarr indexers (restored)")
	}

	instBySlug := map[string]int64{}
	list, _ := (database.Instances{}).List(ctx, db)
	for _, inst := range list {
		instBySlug[inst.Slug] = inst.ID
	}
	want := map[int64]bool{instBySlug["alpha"]: true, instBySlug["gamma"]: true}
	if len(profile.IndexerIDs) != 2 {
		t.Fatalf("minted profile IndexerIDs = %v, want 2 entries (alpha, gamma)", profile.IndexerIDs)
	}
	for _, id := range profile.IndexerIDs {
		if !want[id] {
			t.Errorf("minted profile selects unexpected instance id %d", id)
		}
	}
	if instBySlug["beta"] == profile.IndexerIDs[0] || instBySlug["beta"] == profile.IndexerIDs[1] {
		t.Error("minted profile selects beta, which the legacy selection excluded")
	}
}

// TestRestoreLegacySelectionNameCollisionSuffixed proves two legacy connections that
// would mint the same profile name ("<name> indexers (restored)") don't collide: the
// second gets a numeric suffix rather than failing the restore.
func TestRestoreLegacySelectionNameCollisionSuffixed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, db := newLegacyTestService(t)

	tables := &Tables{
		IndexerInstances: []InstanceRow{
			{ID: 1, Slug: "one", DefinitionID: "one", Name: "One", Enabled: true, Protocol: "torrent"},
		},
		AppConnections: []AppConnRow{
			{
				ID: 1, Name: "Sonarr", Kind: "sonarr", BaseURL: "http://sonarr-a:8989",
				APIKey: "k", HarbrrURL: "http://h:7478", Enabled: true, SyncLevel: "full",
				FreeleechMode: "honor", IndexScope: "selected", SelectedInstanceIDs: []int64{1},
			},
			{
				ID: 2, Name: "Sonarr", Kind: "sonarr", BaseURL: "http://sonarr-b:8989",
				APIKey: "k", HarbrrURL: "http://h:7478", Enabled: true, SyncLevel: "full",
				FreeleechMode: "honor", IndexScope: "selected", SelectedInstanceIDs: []int64{1},
			},
		},
	}
	if err := svc.restore(ctx, tables, true); err != nil {
		t.Fatalf("restore: %v", err)
	}

	profiles, err := (database.SyncProfiles{}).ListProfiles(ctx, db)
	if err != nil {
		t.Fatalf("list profiles: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("minted profiles = %d, want 2 (one per connection, name-suffixed)", len(profiles))
	}
	names := map[string]bool{}
	for _, p := range profiles {
		names[p.Name] = true
	}
	if !names["Sonarr indexers (restored)"] || !names["Sonarr indexers (restored) (2)"] {
		t.Errorf("minted profile names = %v, want the base name plus a (2)-suffixed collision", names)
	}
}

// TestRestoreOldShapeInstanceTogglesDefaultTrue proves loadInstances normalizes a
// bundled instance's nil search-mode toggles (an old-shape InstanceRow written before
// #365 never carried these fields, so they decode as nil) to true — a restored fleet
// must not silently have every toggle OFF and stop syncing.
func TestRestoreOldShapeInstanceTogglesDefaultTrue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, db := newLegacyTestService(t)

	explicitFalse := false
	tables := &Tables{
		IndexerInstances: []InstanceRow{
			// Old-shape row: no toggle fields set (the JSON zero value, nil).
			{ID: 1, Slug: "old", DefinitionID: "old", Name: "Old", Enabled: true, Protocol: "torrent"},
			// New-shape row: an explicit false survives untouched.
			{
				ID: 2, Slug: "new", DefinitionID: "new", Name: "New", Enabled: true, Protocol: "torrent",
				EnableRss: &explicitFalse, EnableAutomaticSearch: &explicitFalse, EnableInteractiveSearch: &explicitFalse,
			},
		},
	}
	if err := svc.restore(ctx, tables, true); err != nil {
		t.Fatalf("restore: %v", err)
	}

	list, err := (database.Instances{}).List(ctx, db)
	if err != nil {
		t.Fatalf("list instances: %v", err)
	}
	for _, inst := range list {
		switch inst.Slug {
		case "old":
			if !inst.EnableRss || !inst.EnableAutomaticSearch || !inst.EnableInteractiveSearch {
				t.Errorf("old-shape instance toggles = %v/%v/%v, want true/true/true",
					inst.EnableRss, inst.EnableAutomaticSearch, inst.EnableInteractiveSearch)
			}
		case "new":
			if inst.EnableRss || inst.EnableAutomaticSearch || inst.EnableInteractiveSearch {
				t.Errorf("new-shape instance toggles = %v/%v/%v, want false/false/false (explicit)",
					inst.EnableRss, inst.EnableAutomaticSearch, inst.EnableInteractiveSearch)
			}
		}
	}
}
