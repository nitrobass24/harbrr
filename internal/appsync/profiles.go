package appsync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

// maxCategoryID bounds a profile category id: Newznab ids and harbrr's custom range
// (>=100000) all sit well under this, so an id outside (0, maxCategoryID) is a client
// mistake rejected at the boundary.
const maxCategoryID = 1_000_000

// CreateProfileParams is the input to CreateProfile. The three Enable toggles are
// pointers so an omitted toggle defaults to true (the Prowlarr default), distinct from
// an explicit false.
type CreateProfileParams struct {
	Name                    string
	Categories              []int
	MinSeeders              int
	EnableRss               *bool
	EnableAutomaticSearch   *bool
	EnableInteractiveSearch *bool
}

// UpdateProfileParams patches a profile; nil fields are left unchanged. Categories is a
// *[]int so a present-but-empty slice clears the category set (revert to full-category
// behavior), distinct from an omitted field that keeps the stored set.
type UpdateProfileParams struct {
	Name                    *string
	Categories              *[]int
	MinSeeders              *int
	EnableRss               *bool
	EnableAutomaticSearch   *bool
	EnableInteractiveSearch *bool
}

// ListProfiles returns all sync profiles.
func (s *Service) ListProfiles(ctx context.Context) ([]domain.SyncProfile, error) {
	list, err := s.profiles.ListProfiles(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("appsync: list sync profiles: %w", err)
	}
	return list, nil
}

// GetProfile returns one sync profile, or ErrNotFound.
func (s *Service) GetProfile(ctx context.Context, id int64) (domain.SyncProfile, error) {
	p, err := s.profiles.GetProfile(ctx, s.db, id)
	if err != nil {
		return domain.SyncProfile{}, fmt.Errorf("appsync: get sync profile: %w", err)
	}
	return p, nil
}

// CreateProfile validates and persists a new sync profile. A duplicate name maps to
// ErrConflict (the handler's 409).
func (s *Service) CreateProfile(ctx context.Context, p CreateProfileParams) (domain.SyncProfile, error) {
	profile, err := buildProfile(p)
	if err != nil {
		return domain.SyncProfile{}, err
	}
	now := s.clock()
	profile.CreatedAt, profile.UpdatedAt = now, now
	id, err := s.profiles.InsertProfile(ctx, s.db, profile)
	if err != nil {
		if database.IsUniqueViolation(err) {
			return domain.SyncProfile{}, fmt.Errorf("%w: sync profile name %q already in use", ErrConflict, profile.Name)
		}
		return domain.SyncProfile{}, fmt.Errorf("appsync: insert sync profile: %w", err)
	}
	profile.ID = id
	return profile, nil
}

// UpdateProfile applies a patch to an existing profile. A duplicate name maps to
// ErrConflict; an unknown id flows through as ErrNotFound. A category change is
// re-checked against every connection referencing this profile — the same overlap
// guard as assignment — so narrowing a live profile can't zero out a referencing
// connection's category gate (a full-sync connection would then delete every
// indexer it manages on its next sync).
func (s *Service) UpdateProfile(ctx context.Context, id int64, p UpdateProfileParams) error {
	profile, err := s.profiles.GetProfile(ctx, s.db, id)
	if err != nil {
		return fmt.Errorf("appsync: get sync profile: %w", err)
	}
	if err := applyProfileUpdate(&profile, p); err != nil {
		return err
	}
	if p.Categories != nil {
		if err := s.validateProfileInUse(ctx, id, profile.Categories); err != nil {
			return err
		}
	}
	profile.UpdatedAt = s.clock()
	if err := s.profiles.UpdateProfile(ctx, s.db, profile); err != nil {
		if database.IsUniqueViolation(err) {
			return fmt.Errorf("%w: sync profile name %q already in use", ErrConflict, profile.Name)
		}
		return fmt.Errorf("appsync: update sync profile: %w", err)
	}
	return nil
}

// DeleteProfile removes a sync profile. Referencing connections' sync_profile_id is
// nulled by the ON DELETE SET NULL FK (they revert to default behavior on the next sync).
func (s *Service) DeleteProfile(ctx context.Context, id int64) error {
	if err := s.profiles.DeleteProfile(ctx, s.db, id); err != nil {
		return fmt.Errorf("appsync: delete sync profile: %w", err)
	}
	return nil
}

// buildProfile validates a create request and folds in the toggle defaults.
func buildProfile(p CreateProfileParams) (domain.SyncProfile, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return domain.SyncProfile{}, fmt.Errorf("%w: sync profile name is required", ErrInvalid)
	}
	if p.MinSeeders < 0 {
		return domain.SyncProfile{}, fmt.Errorf("%w: min_seeders must not be negative", ErrInvalid)
	}
	cats, err := normalizeCategoryIDs(p.Categories)
	if err != nil {
		return domain.SyncProfile{}, err
	}
	return domain.SyncProfile{
		Name:                    name,
		Categories:              cats,
		MinSeeders:              p.MinSeeders,
		EnableRss:               boolOrTrue(p.EnableRss),
		EnableAutomaticSearch:   boolOrTrue(p.EnableAutomaticSearch),
		EnableInteractiveSearch: boolOrTrue(p.EnableInteractiveSearch),
	}, nil
}

// applyProfileUpdate mutates profile from the non-nil patch fields, validating any it sets.
func applyProfileUpdate(profile *domain.SyncProfile, p UpdateProfileParams) error {
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if name == "" {
			return fmt.Errorf("%w: sync profile name must not be blank", ErrInvalid)
		}
		profile.Name = name
	}
	if p.Categories != nil {
		cats, err := normalizeCategoryIDs(*p.Categories)
		if err != nil {
			return err
		}
		profile.Categories = cats
	}
	if p.MinSeeders != nil {
		if *p.MinSeeders < 0 {
			return fmt.Errorf("%w: min_seeders must not be negative", ErrInvalid)
		}
		profile.MinSeeders = *p.MinSeeders
	}
	if p.EnableRss != nil {
		profile.EnableRss = *p.EnableRss
	}
	if p.EnableAutomaticSearch != nil {
		profile.EnableAutomaticSearch = *p.EnableAutomaticSearch
	}
	if p.EnableInteractiveSearch != nil {
		profile.EnableInteractiveSearch = *p.EnableInteractiveSearch
	}
	return nil
}

// normalizeCategoryIDs bounds-checks, dedupes, and sorts a profile's category ids so the
// stored set is deterministic. An empty input yields an empty (non-nil) slice.
func normalizeCategoryIDs(ids []int) ([]int, error) {
	seen := make(map[int]bool, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || id >= maxCategoryID {
			return nil, fmt.Errorf("%w: category id %d out of range (0 < id < %d)", ErrInvalid, id, maxCategoryID)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Ints(out)
	return out, nil
}

// validateProfileRef checks that a connection's sync-profile reference is usable for its
// kind before it is persisted: the profile must exist (an unknown id is a client mistake
// → ErrInvalid, not a 404); qui never takes a profile; and a non-empty category set must
// overlap the kind's content range — else a full-sync connection would category-filter
// down to zero indexers and silently delete every one it manages. A nil ref is valid.
func (s *Service) validateProfileRef(ctx context.Context, kind string, id *int64) error {
	if id == nil {
		return nil
	}
	if kind == domain.AppKindQui {
		return fmt.Errorf("%w: sync profiles do not apply to qui", ErrInvalid)
	}
	profile, err := s.profiles.GetProfile(ctx, s.db, *id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return fmt.Errorf("%w: sync profile %d does not exist", ErrInvalid, *id)
		}
		return fmt.Errorf("appsync: get sync profile: %w", err)
	}
	if profileOverlapsKind(kind, profile.Categories) {
		return nil
	}
	return fmt.Errorf("%w: sync profile %d has no categories a %s connection can consume", ErrInvalid, *id, kind)
}

// profileOverlapsKind reports whether a profile category set is usable for an app
// kind: an empty set always is (it means "no category filter"), a non-empty set must
// contain at least one category the kind's content range serves.
func profileOverlapsKind(kind string, cats []int) bool {
	if len(cats) == 0 {
		return true
	}
	for _, catID := range cats {
		if categoryServesApp(kind, catID) {
			return true
		}
	}
	return false
}

// validateProfileInUse re-runs the assignment-time overlap guard for every connection
// referencing the profile, against a candidate category set. Rejecting the profile
// edit here (naming the blocking connection) closes the hole where narrowing a live
// profile silently empties a referencing connection's gate.
func (s *Service) validateProfileInUse(ctx context.Context, id int64, cats []int) error {
	if len(cats) == 0 {
		return nil // no filter — usable by every kind
	}
	conns, err := s.repo.ListConnections(ctx, s.db)
	if err != nil {
		return fmt.Errorf("appsync: list connections: %w", err)
	}
	for _, conn := range conns {
		if conn.SyncProfileID == nil || *conn.SyncProfileID != id {
			continue
		}
		if !profileOverlapsKind(conn.Kind, cats) {
			return fmt.Errorf("%w: new category set has no categories the %s connection %q can consume — detach the profile first or keep a %s-range category",
				ErrInvalid, conn.Kind, conn.Name, conn.Kind)
		}
	}
	return nil
}

// boolOrTrue resolves an optional toggle: nil defaults to true (the Prowlarr default).
func boolOrTrue(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}
