package appsync

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

func TestCreateProfileRoundTrip(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	instA, instB := f.source.instances[0].ID, f.source.instances[1].ID
	p, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "movies", IndexerIDs: []int64{instB, instA}})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if p.Name != "movies" {
		t.Errorf("Name = %q, want movies", p.Name)
	}
	if !slices.Equal(p.IndexerIDs, []int64{instA, instB}) {
		t.Errorf("IndexerIDs = %v, want [%d %d] (repo-ordered)", p.IndexerIDs, instA, instB)
	}

	got, err := f.svc.GetProfile(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if !slices.Equal(got.IndexerIDs, []int64{instA, instB}) {
		t.Errorf("GetProfile.IndexerIDs = %v, want [%d %d]", got.IndexerIDs, instA, instB)
	}

	list, err := f.svc.ListProfiles(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListProfiles = %v, %d rows", err, len(list))
	}
}

// TestCreateProfileEmptySelectionMeansAll proves an omitted/empty IndexerIDs is valid
// and round-trips as an empty (non-nil) slice — "every compatible indexer".
func TestCreateProfileEmptySelectionMeansAll(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	p, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "all"})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if p.IndexerIDs == nil || len(p.IndexerIDs) != 0 {
		t.Errorf("IndexerIDs = %v, want empty slice", p.IndexerIDs)
	}
}

func TestCreateProfileValidation(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	if _, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "  "}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("blank name err = %v, want domain.ErrInvalid", err)
	}
}

func TestCreateProfileDuplicateName(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	if _, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "dup"}); err != nil {
		t.Fatalf("first CreateProfile: %v", err)
	}
	if _, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "dup"}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate name err = %v, want domain.ErrConflict", err)
	}
}

func TestUpdateProfileClearsSelectionAndValidates(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	instA := f.source.instances[0].ID
	p, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "p", IndexerIDs: []int64{instA}})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	// A present-but-empty IndexerIDs slice clears the selection.
	empty := []int64{}
	if err := f.svc.UpdateProfile(ctx, p.ID, UpdateProfileParams{IndexerIDs: &empty}); err != nil {
		t.Fatalf("clear selection: %v", err)
	}
	got, err := f.svc.GetProfile(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if len(got.IndexerIDs) != 0 {
		t.Errorf("IndexerIDs = %v, want cleared", got.IndexerIDs)
	}

	// An unknown instance id patch is rejected.
	bad := []int64{99999}
	if err := f.svc.UpdateProfile(ctx, p.ID, UpdateProfileParams{IndexerIDs: &bad}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("update to unknown instance id = %v, want domain.ErrInvalid", err)
	}

	// An unknown id flows through as ErrNotFound.
	if err := f.svc.UpdateProfile(ctx, 99999, UpdateProfileParams{}); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("update unknown id = %v, want ErrNotFound", err)
	}
}

func TestUpdateProfileNamePatch(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	if _, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "a"}); err != nil {
		t.Fatalf("CreateProfile a: %v", err)
	}
	b, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "b"})
	if err != nil {
		t.Fatalf("CreateProfile b: %v", err)
	}

	// A blank name patch is rejected.
	blank := "  "
	if err := f.svc.UpdateProfile(ctx, b.ID, UpdateProfileParams{Name: &blank}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("blank name update = %v, want domain.ErrInvalid", err)
	}

	// Renaming onto an existing name is a conflict (the UPDATE-path unique mapping).
	taken := "a"
	if err := f.svc.UpdateProfile(ctx, b.ID, UpdateProfileParams{Name: &taken}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate rename = %v, want domain.ErrConflict", err)
	}

	// A fresh name lands.
	fresh := "b2"
	if err := f.svc.UpdateProfile(ctx, b.ID, UpdateProfileParams{Name: &fresh}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, err := f.svc.GetProfile(ctx, b.ID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if got.Name != "b2" {
		t.Errorf("name = %q, want b2", got.Name)
	}
}

func TestDeleteProfileNotFound(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	if err := f.svc.DeleteProfile(context.Background(), 99999); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("delete unknown id = %v, want ErrNotFound", err)
	}
}

// TestDeleteProfileRefusedWhileInUse proves DeleteProfile is refused (domain.ErrConflict)
// while any connection still references the profile — the FK's ON DELETE SET NULL would
// otherwise silently widen a full-sync connection to every indexer on its next sync.
// Detaching the connection first lets the delete through.
func TestDeleteProfileRefusedWhileInUse(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	p, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "in-use"})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{
		SyncProfileID: RefUpdate{Present: true, Value: &p.ID},
	}); err != nil {
		t.Fatalf("assign profile: %v", err)
	}

	if err := f.svc.DeleteProfile(ctx, p.ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("delete in-use profile = %v, want domain.ErrConflict", err)
	}

	if err := f.svc.UpdateConnection(ctx, f.conn.ID, UpdateConnectionParams{
		SyncProfileID: RefUpdate{Present: true, Value: nil},
	}); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if err := f.svc.DeleteProfile(ctx, p.ID); err != nil {
		t.Errorf("delete after detach = %v, want nil", err)
	}
}

// TestConnectionProfileRefValidation exercises validateProfileRef through both the
// create and update connection paths: an unknown ref is a 400 for every kind, including
// qui (#365 dropped the qui hard-rejection — a routing set is meaningful for it too); a
// valid ref persists and a present-nil clears it.
func TestConnectionProfileRefValidation(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	prof, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "p"})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	unknown := int64(999999)

	refCases := map[string]struct {
		kind    string
		baseURL string
	}{
		"unknown ref sonarr": {domain.AppKindSonarr, "http://s1:8989"},
		"unknown ref qui":    {domain.AppKindQui, "http://qui:7000"},
	}
	for name, tc := range refCases {
		_, err := f.svc.CreateConnection(ctx, connParams(name, tc.kind, tc.baseURL, &unknown))
		if !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: create err = %v, want domain.ErrInvalid", name, err)
		}
	}

	// A valid ref is accepted and persisted — including for qui.
	conn, err := f.svc.CreateConnection(ctx, connParams("valid-qui", domain.AppKindQui, "http://qui2:7000", &prof.ID))
	if err != nil {
		t.Fatalf("CreateConnection qui with valid ref: %v", err)
	}
	if conn.SyncProfileID == nil || *conn.SyncProfileID != prof.ID {
		t.Fatalf("profile id not persisted: %v", conn.SyncProfileID)
	}

	// Update: an unknown ref is a 400; a present-nil clears it.
	if err := f.svc.UpdateConnection(ctx, conn.ID, UpdateConnectionParams{
		SyncProfileID: RefUpdate{Present: true, Value: &unknown},
	}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("update unknown ref = %v, want domain.ErrInvalid", err)
	}
	if err := f.svc.UpdateConnection(ctx, conn.ID, UpdateConnectionParams{
		SyncProfileID: RefUpdate{Present: true, Value: nil},
	}); err != nil {
		t.Fatalf("clear ref: %v", err)
	}
	got, err := f.svc.GetConnection(ctx, conn.ID)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if got.SyncProfileID != nil {
		t.Errorf("ref = %v after clear, want nil", got.SyncProfileID)
	}
}

func connParams(name, kind, baseURL string, profileID *int64) CreateConnectionParams {
	return CreateConnectionParams{
		Name: name, Kind: kind, BaseURL: baseURL, APIKey: "k",
		HarbrrURL: "http://harbrr:8787", SyncProfileID: profileID,
	}
}
