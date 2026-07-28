package appsync

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// CreateProfileParams is the input to CreateProfile. IndexerIDs is the profile's
// selected instance set; empty means every compatible indexer.
type CreateProfileParams struct {
	Name       string
	IndexerIDs []int64
}

// UpdateProfileParams patches a profile; nil fields are left unchanged. IndexerIDs is a
// *[]int64 so a present-but-empty slice clears the selection (revert to "every
// compatible indexer"), distinct from an omitted field that keeps the stored set.
type UpdateProfileParams struct {
	Name       *string
	IndexerIDs *[]int64
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
// domain.ErrConflict (the handler's 409). The name row and its indexer selection are
// written in one transaction.
func (s *Service) CreateProfile(ctx context.Context, p CreateProfileParams) (domain.SyncProfile, error) {
	name, err := validateProfileName(p.Name)
	if err != nil {
		return domain.SyncProfile{}, err
	}
	if err := s.validateInstanceIDs(ctx, p.IndexerIDs); err != nil {
		return domain.SyncProfile{}, err
	}
	now := s.clock()
	profile := domain.SyncProfile{Name: name, IndexerIDs: p.IndexerIDs, CreatedAt: now, UpdatedAt: now}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.SyncProfile{}, fmt.Errorf("appsync: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	id, err := s.profiles.InsertProfile(ctx, tx, profile)
	if err != nil {
		if database.IsUniqueViolation(err) {
			return domain.SyncProfile{}, fmt.Errorf("%w: sync profile name %q already in use", domain.ErrConflict, profile.Name)
		}
		return domain.SyncProfile{}, fmt.Errorf("appsync: insert sync profile: %w", err)
	}
	if err := s.profiles.ReplaceProfileIndexers(ctx, tx, id, p.IndexerIDs); err != nil {
		return domain.SyncProfile{}, fmt.Errorf("appsync: write sync profile indexers: %w", err)
	}
	// Re-read the canonical (deduped, ordered) selection so the returned value matches
	// what a subsequent GetProfile would see, rather than echoing the caller's input verbatim.
	ids, err := s.profiles.ListProfileIndexers(ctx, tx, id)
	if err != nil {
		return domain.SyncProfile{}, fmt.Errorf("appsync: read sync profile indexers: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.SyncProfile{}, fmt.Errorf("appsync: commit: %w", err)
	}
	profile.ID, profile.IndexerIDs = id, ids
	return profile, nil
}

// UpdateProfile applies a patch to an existing profile: the row and (when present) its
// indexer selection are written in one transaction.
func (s *Service) UpdateProfile(ctx context.Context, id int64, p UpdateProfileParams) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("appsync: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	profile, err := s.profiles.GetProfile(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("appsync: get sync profile: %w", err)
	}
	if p.Name != nil {
		name, err := validateProfileName(*p.Name)
		if err != nil {
			return err
		}
		profile.Name = name
	}
	if p.IndexerIDs != nil {
		if err := s.validateInstanceIDs(ctx, *p.IndexerIDs); err != nil {
			return err
		}
		profile.IndexerIDs = *p.IndexerIDs
	}
	profile.UpdatedAt = s.clock()
	if err := s.profiles.UpdateProfile(ctx, tx, profile); err != nil {
		if database.IsUniqueViolation(err) {
			return fmt.Errorf("%w: sync profile name %q already in use", domain.ErrConflict, profile.Name)
		}
		return fmt.Errorf("appsync: update sync profile: %w", err)
	}
	if p.IndexerIDs != nil {
		if err := s.profiles.ReplaceProfileIndexers(ctx, tx, id, *p.IndexerIDs); err != nil {
			return fmt.Errorf("appsync: write sync profile indexers: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("appsync: commit: %w", err)
	}
	return nil
}

// DeleteProfile removes a sync profile, refused (domain.ErrConflict) while any
// connection still references it — the FK's ON DELETE SET NULL would otherwise
// silently widen a full-sync connection to every indexer on its next sync. Read and
// delete run in one transaction so a concurrent connection update can't slip between
// the check and the delete.
func (s *Service) DeleteProfile(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("appsync: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	conns, err := s.repo.ListConnections(ctx, tx)
	if err != nil {
		return fmt.Errorf("appsync: list connections: %w", err)
	}
	for _, conn := range conns {
		if conn.SyncProfileID != nil && *conn.SyncProfileID == id {
			return fmt.Errorf("%w: sync profile %d is in use by connection %q", domain.ErrConflict, id, conn.Name)
		}
	}
	if err := s.profiles.DeleteProfile(ctx, tx, id); err != nil {
		return fmt.Errorf("appsync: delete sync profile: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("appsync: commit: %w", err)
	}
	return nil
}

// validateProfileName trims and requires a non-blank profile name.
func validateProfileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("%w: sync profile name is required", domain.ErrInvalid)
	}
	return name, nil
}

// validateProfileRef checks that a connection's sync-profile reference exists before it
// is persisted (an unknown id is a client mistake → domain.ErrInvalid, not a 404). A
// routing set is valid for every app kind, including qui. A nil ref is valid. q is the
// caller's handle (db or tx) so the ref check and the connection write that follows it
// can share one transaction (the UpdateConnection precedent).
func (s *Service) validateProfileRef(ctx context.Context, q dbinterface.Execer, id *int64) error {
	if id == nil {
		return nil
	}
	if _, err := s.profiles.GetProfile(ctx, q, *id); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return fmt.Errorf("%w: sync profile %d does not exist", domain.ErrInvalid, *id)
		}
		return fmt.Errorf("appsync: get sync profile: %w", err)
	}
	return nil
}
