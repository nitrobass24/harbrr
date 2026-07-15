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

	"github.com/autobrr/harbrr/internal/connresource"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// ErrInvalid is the sentinel the API maps to 400 for malformed input.
var ErrInvalid = errors.New("solver: invalid input")

// Service persists solver resources, encrypting the endpoint URL at rest.
// Create/Update of the row and its encrypted secret are sequenced by
// connresource.Lifecycle (Delete stays a bare repo delete — see Delete's doc).
type Service struct {
	db      dbinterface.Querier
	repo    database.Solvers
	keyring *secrets.Keyring
	clock   func() time.Time
	life    *connresource.Lifecycle[domain.Solver]
}

// NewService wires the solver service. clock is injectable for deterministic
// tests (assigning to the returned Service's clock field also retunes its
// Lifecycle, which reads clock through an indirection).
func NewService(db dbinterface.Querier, keyring *secrets.Keyring) *Service {
	s := &Service{db: db, keyring: keyring, clock: time.Now}
	s.life = connresource.New[domain.Solver](db, keyring, func() time.Time { return s.clock() })
	return s
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
	if err := validate(p.Name, p.Type, p.URL, &p.MaxTimeout); err != nil {
		return domain.Solver{}, err
	}
	return s.life.Create(ctx, connresource.CreateSpec[domain.Solver]{
		Build: func(now time.Time, _ int64) domain.Solver {
			return domain.Solver{Name: p.Name, Type: p.Type, MaxTimeout: p.MaxTimeout, CreatedAt: now, UpdatedAt: now}
		},
		Insert: func(ctx context.Context, q dbinterface.Execer, row domain.Solver) (int64, error) {
			return s.repo.InsertSolver(ctx, q, row)
		},
		Secrets: func(_ domain.Solver, _ string) []connresource.Secret {
			return []connresource.Secret{{Discriminator: domain.SolverSecretURL, Plaintext: p.URL}}
		},
		SetSecrets: func(ctx context.Context, q dbinterface.Execer, id int64, encrypted []string, keyID string) error {
			return s.repo.SetSolverSecret(ctx, q, id, encrypted[0], keyID)
		},
		Finalize: func(row domain.Solver, id int64, encrypted []string, keyID string) domain.Solver {
			row.ID, row.URLEncrypted, row.KeyID = id, encrypted[0], keyID
			return row
		},
	})
}

// UpdateParams patches a solver; nil fields are left unchanged.
type UpdateParams struct {
	Name       *string
	Type       *string
	URL        *string
	MaxTimeout *int
}

// Update applies a patch, re-encrypting the URL when rotated. The read and the
// full-row write run in one transaction so two overlapping PATCHes can't lose each
// other's write.
func (s *Service) Update(ctx context.Context, id int64, p UpdateParams) error {
	return s.life.Update(ctx, id, connresource.UpdateSpec[domain.Solver]{
		Get: func(ctx context.Context, q dbinterface.Execer, id int64) (domain.Solver, error) {
			return s.repo.GetSolver(ctx, q, id)
		},
		Patch: func(row *domain.Solver) error {
			if p.Name != nil {
				row.Name = strings.TrimSpace(*p.Name)
			}
			if p.Type != nil {
				row.Type = strings.TrimSpace(*p.Type)
			}
			if p.MaxTimeout != nil {
				row.MaxTimeout = *p.MaxTimeout
			}
			// The URL isn't changing here (Rotate handles it), so validate is passed a
			// placeholder that always satisfies its own well-formedness checks — this
			// call exists to validate name/type/maxTimeout only.
			return validate(row.Name, row.Type, "unchanged://ok", p.MaxTimeout)
		},
		Rotate: func(row *domain.Solver) (connresource.Secret, bool, error) {
			if p.URL == nil {
				return connresource.Secret{}, false, nil
			}
			newURL := strings.TrimSpace(*p.URL)
			if err := validate(row.Name, row.Type, newURL, p.MaxTimeout); err != nil {
				return connresource.Secret{}, false, err
			}
			return connresource.Secret{Discriminator: domain.SolverSecretURL, Plaintext: newURL}, true, nil
		},
		Apply: func(row *domain.Solver, encrypted, keyID string) { row.URLEncrypted, row.KeyID = encrypted, keyID },
		Touch: func(row *domain.Solver, now time.Time) { row.UpdatedAt = now },
		Write: func(ctx context.Context, q dbinterface.Execer, row domain.Solver) error {
			return s.repo.UpdateSolver(ctx, q, row)
		},
	})
}

// Delete removes a solver; referencing instances' solver_id is nulled by the FK.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.repo.DeleteSolver(ctx, s.db, id); err != nil {
		return fmt.Errorf("solver: delete: %w", err)
	}
	return nil
}

// validate enforces name, a known type, a parseable URL, and a timeout within
// [0, domain.FlareMaxTimeoutCapSeconds] (0 = use the solver's default).
func validate(name, typ, rawURL string, maxTimeout *int) error {
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
	// maxTimeout is nil when an update patch omits it — the stored value is left
	// untouched (so an unrelated edit isn't blocked, and an already-stored/imported
	// over-cap value isn't re-checked here). 0 means "use the solver's 60s default";
	// anything above the cap would be silently reset to that default at solve time,
	// so reject it here instead.
	if maxTimeout != nil && (*maxTimeout < 0 || *maxTimeout > domain.FlareMaxTimeoutCapSeconds) {
		return fmt.Errorf("%w: maxTimeout must be between 0 and %d seconds", ErrInvalid, domain.FlareMaxTimeoutCapSeconds)
	}
	return nil
}
