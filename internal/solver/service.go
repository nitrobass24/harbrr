// Package solver manages global, reusable anti-bot-solver resources (FlareSolverr
// today) that indexer instances reference by id. It owns CRUD + at-rest encryption
// of the endpoint URL; the engine resolves a referenced solver into the per-request
// solver config (internal/indexer/registry), and the manual-cookie solver stays
// inline per-tracker (it is genuinely per-tracker, so it is not modelled here).
package solver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// ErrInvalid is the sentinel the API maps to 400 for malformed input.
var ErrInvalid = errors.New("solver: invalid input")

// Service persists solver resources, encrypting the endpoint URL at rest.
type Service struct {
	db      dbinterface.Querier
	repo    database.Solvers
	keyring *secrets.Keyring
	clock   func() time.Time
}

// NewService wires the solver service.
func NewService(db dbinterface.Querier, keyring *secrets.Keyring) *Service {
	return &Service{db: db, keyring: keyring, clock: time.Now}
}

// List returns all solvers (URLs stay encrypted; the handler redacts).
func (s *Service) List(ctx context.Context) ([]domain.Solver, error) {
	out, err := s.repo.ListSolvers(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("solver: list: %w", err)
	}
	return out, nil
}

// Get returns one solver by id.
func (s *Service) Get(ctx context.Context, id int64) (domain.Solver, error) {
	row, err := s.repo.GetSolver(ctx, s.db, id)
	if err != nil {
		return domain.Solver{}, fmt.Errorf("solver: get: %w", err)
	}
	return row, nil
}

// CreateParams is the input to Create. Type defaults to flaresolverr when empty.
type CreateParams struct {
	Name       string
	Type       string
	URL        string
	MaxTimeout int
}

// Create persists a solver with its URL encrypted (row first to mint the id the
// AAD binds to, then the sealed secret, in one transaction).
func (s *Service) Create(ctx context.Context, p CreateParams) (domain.Solver, error) {
	p.Name, p.Type, p.URL = strings.TrimSpace(p.Name), strings.TrimSpace(p.Type), strings.TrimSpace(p.URL)
	if p.Type == "" {
		p.Type = domain.SolverTypeFlaresolverr
	}
	if err := validate(p.Name, p.Type, p.URL, p.MaxTimeout); err != nil {
		return domain.Solver{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Solver{}, fmt.Errorf("solver: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.clock()
	row := domain.Solver{Name: p.Name, Type: p.Type, MaxTimeout: p.MaxTimeout, CreatedAt: now, UpdatedAt: now}
	id, err := s.repo.InsertSolver(ctx, tx, row)
	if err != nil {
		return domain.Solver{}, fmt.Errorf("solver: insert: %w", err)
	}
	row.ID = id

	enc, err := s.keyring.Encrypt(id, domain.SolverSecretURL, p.URL)
	if err != nil {
		return domain.Solver{}, fmt.Errorf("solver: encrypt url: %w", err)
	}
	if err := s.repo.SetSolverSecret(ctx, tx, id, enc, s.keyring.KeyID()); err != nil {
		return domain.Solver{}, fmt.Errorf("solver: set secret: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Solver{}, fmt.Errorf("solver: commit: %w", err)
	}
	row.URLEncrypted, row.KeyID = enc, s.keyring.KeyID()
	return row, nil
}

// UpdateParams patches a solver; nil fields are left unchanged.
type UpdateParams struct {
	Name       *string
	Type       *string
	URL        *string
	MaxTimeout *int
}

// Update applies a patch, re-encrypting the URL when rotated.
func (s *Service) Update(ctx context.Context, id int64, p UpdateParams) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("solver: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row, err := s.repo.GetSolver(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("solver: get: %w", err)
	}
	if p.Name != nil {
		row.Name = strings.TrimSpace(*p.Name)
	}
	if p.Type != nil {
		row.Type = strings.TrimSpace(*p.Type)
	}
	if p.MaxTimeout != nil {
		row.MaxTimeout = *p.MaxTimeout
	}
	newURL := ""
	if p.URL != nil {
		newURL = strings.TrimSpace(*p.URL)
	}
	validateURL := "unchanged://ok"
	if p.URL != nil {
		validateURL = newURL
	}
	if err := validate(row.Name, row.Type, validateURL, row.MaxTimeout); err != nil {
		return err
	}

	row.UpdatedAt = s.clock()
	if p.URL != nil {
		enc, err := s.keyring.Encrypt(id, domain.SolverSecretURL, newURL)
		if err != nil {
			return fmt.Errorf("solver: encrypt url: %w", err)
		}
		row.URLEncrypted, row.KeyID = enc, s.keyring.KeyID()
	}
	if err := s.repo.UpdateSolver(ctx, tx, row); err != nil {
		return fmt.Errorf("solver: update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("solver: commit: %w", err)
	}
	return nil
}

// Delete removes a solver; referencing instances' solver_id is nulled by the FK.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.repo.DeleteSolver(ctx, s.db, id); err != nil {
		return fmt.Errorf("solver: delete: %w", err)
	}
	return nil
}

// validate enforces name, a known type, a parseable URL, and a non-negative timeout.
func validate(name, typ, rawURL string, maxTimeout int) error {
	if name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if typ != domain.SolverTypeFlaresolverr {
		return fmt.Errorf("%w: unknown solver type %q", ErrInvalid, typ)
	}
	if rawURL == "" {
		return fmt.Errorf("%w: url is required", ErrInvalid)
	}
	if u, err := url.Parse(rawURL); err != nil || u.Host == "" {
		return fmt.Errorf("%w: url must be an absolute URL", ErrInvalid)
	}
	if maxTimeout < 0 {
		return fmt.Errorf("%w: maxTimeout must be >= 0", ErrInvalid)
	}
	return nil
}
