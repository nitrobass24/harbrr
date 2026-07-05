package appsync

import (
	"context"
	"errors"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

func TestCreateProfileDefaultsAndNormalize(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	// Omitted toggles default to true; categories are deduped and sorted.
	p, err := f.svc.CreateProfile(ctx, CreateProfileParams{
		Name: "movies", Categories: []int{5000, 2000, 2000}, MinSeeders: 3,
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if !p.EnableRss || !p.EnableAutomaticSearch || !p.EnableInteractiveSearch {
		t.Errorf("omitted toggles should default to true: %+v", p)
	}
	if !equalIntSlice(p.Categories, []int{2000, 5000}) {
		t.Errorf("categories = %v, want [2000 5000] (deduped+sorted)", p.Categories)
	}
	if p.MinSeeders != 3 {
		t.Errorf("minSeeders = %d, want 3", p.MinSeeders)
	}

	// An explicit false toggle is preserved (distinct from the default).
	no := false
	quiet, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "quiet", EnableRss: &no})
	if err != nil {
		t.Fatalf("CreateProfile quiet: %v", err)
	}
	if quiet.EnableRss {
		t.Errorf("explicit false enableRss not preserved: %+v", quiet)
	}
}

func TestCreateProfileValidation(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	bad := map[string]CreateProfileParams{
		"blank name":        {Name: "  "},
		"category too low":  {Name: "a", Categories: []int{0}},
		"category too high": {Name: "b", Categories: []int{1_000_000}},
		"negative seeders":  {Name: "c", MinSeeders: -1},
	}
	for name, p := range bad {
		if _, err := f.svc.CreateProfile(ctx, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestCreateProfileDuplicateName(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	if _, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "dup"}); err != nil {
		t.Fatalf("first CreateProfile: %v", err)
	}
	if _, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "dup"}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate name err = %v, want ErrConflict", err)
	}
}

func TestUpdateProfileClearsAndValidates(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()
	p, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "p", Categories: []int{5000}, MinSeeders: 2})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	// A present-but-empty categories slice clears the set.
	empty := []int{}
	if err := f.svc.UpdateProfile(ctx, p.ID, UpdateProfileParams{Categories: &empty}); err != nil {
		t.Fatalf("clear categories: %v", err)
	}
	got, err := f.svc.GetProfile(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if len(got.Categories) != 0 {
		t.Errorf("categories = %v, want cleared", got.Categories)
	}

	// A negative minSeeders patch is rejected.
	neg := -1
	if err := f.svc.UpdateProfile(ctx, p.ID, UpdateProfileParams{MinSeeders: &neg}); !errors.Is(err, ErrInvalid) {
		t.Errorf("negative minSeeders update = %v, want ErrInvalid", err)
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
	if err := f.svc.UpdateProfile(ctx, b.ID, UpdateProfileParams{Name: &blank}); !errors.Is(err, ErrInvalid) {
		t.Errorf("blank name update = %v, want ErrInvalid", err)
	}

	// Renaming onto an existing name is a conflict (the UPDATE-path unique mapping).
	taken := "a"
	if err := f.svc.UpdateProfile(ctx, b.ID, UpdateProfileParams{Name: &taken}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate rename = %v, want ErrConflict", err)
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

// TestUpdateProfileGuardsReferencingConnections pins the in-use overlap guard: once a
// connection references a profile, narrowing the profile's categories to a set the
// connection's kind cannot consume is rejected — otherwise a full-sync connection
// would category-filter to zero indexers and delete everything it manages.
func TestUpdateProfileGuardsReferencingConnections(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	tv, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "tv", Categories: []int{5000}})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	conn, err := f.svc.CreateConnection(ctx, connParams("s", domain.AppKindSonarr, "http://s5:8989", &tv.ID))
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	// Narrowing to books-only would empty the Sonarr connection's gate — rejected.
	books := []int{7000}
	if err := f.svc.UpdateProfile(ctx, tv.ID, UpdateProfileParams{Categories: &books}); !errors.Is(err, ErrInvalid) {
		t.Errorf("in-use narrowing to books = %v, want ErrInvalid", err)
	}

	// Clearing to empty is always fine (empty = no filter).
	empty := []int{}
	if err := f.svc.UpdateProfile(ctx, tv.ID, UpdateProfileParams{Categories: &empty}); err != nil {
		t.Fatalf("clear categories while referenced: %v", err)
	}

	// After detaching the connection, the same narrowing succeeds.
	if err := f.svc.UpdateConnection(ctx, conn.ID, UpdateConnectionParams{
		SyncProfileID: RefUpdate{Present: true, Value: nil},
	}); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if err := f.svc.UpdateProfile(ctx, tv.ID, UpdateProfileParams{Categories: &books}); err != nil {
		t.Errorf("narrowing after detach = %v, want nil", err)
	}
}

func TestDeleteProfileNotFound(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	if err := f.svc.DeleteProfile(context.Background(), 99999); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("delete unknown id = %v, want ErrNotFound", err)
	}
}

// TestConnectionProfileRefValidation exercises validateProfileRef through both the
// create and update connection paths: unknown ref, qui ref, and zero-overlap ref all
// 400; an overlapping ref persists; a present-nil clears it.
func TestConnectionProfileRefValidation(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	// A books-only profile (7000) never overlaps Sonarr's TV range; a TV profile does.
	books, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "books", Categories: []int{7000}})
	if err != nil {
		t.Fatalf("CreateProfile books: %v", err)
	}
	tv, err := f.svc.CreateProfile(ctx, CreateProfileParams{Name: "tv", Categories: []int{5000}})
	if err != nil {
		t.Fatalf("CreateProfile tv: %v", err)
	}
	unknown := int64(999999)

	refCases := map[string]struct {
		kind    string
		baseURL string
		profile *int64
	}{
		"unknown ref":      {domain.AppKindSonarr, "http://s1:8989", &unknown},
		"qui ref":          {domain.AppKindQui, "http://qui:7000", &tv.ID},
		"zero-overlap ref": {domain.AppKindSonarr, "http://s3:8989", &books.ID},
	}
	for name, tc := range refCases {
		_, err := f.svc.CreateConnection(ctx, connParams(name, tc.kind, tc.baseURL, tc.profile))
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: create err = %v, want ErrInvalid", name, err)
		}
	}

	// An overlapping profile ref is accepted and persisted.
	conn, err := f.svc.CreateConnection(ctx, connParams("valid", domain.AppKindSonarr, "http://s4:8989", &tv.ID))
	if err != nil {
		t.Fatalf("CreateConnection with valid ref: %v", err)
	}
	if conn.SyncProfileID == nil || *conn.SyncProfileID != tv.ID {
		t.Fatalf("profile id not persisted: %v", conn.SyncProfileID)
	}

	// Update: an unknown ref is a 400; a present-nil clears it.
	if err := f.svc.UpdateConnection(ctx, conn.ID, UpdateConnectionParams{
		SyncProfileID: RefUpdate{Present: true, Value: &unknown},
	}); !errors.Is(err, ErrInvalid) {
		t.Errorf("update unknown ref = %v, want ErrInvalid", err)
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

func equalIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
