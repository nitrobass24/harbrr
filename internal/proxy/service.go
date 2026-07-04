// Package proxy manages global, reusable proxy resources that indexer instances
// reference by id. It owns CRUD + at-rest encryption of the proxy URL (which
// routinely embeds user:pass); the engine resolves a referenced proxy into the
// per-request transport config (internal/indexer/registry), and the auto-migration
// folds legacy inline proxy settings into these resources.
package proxy

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
var ErrInvalid = errors.New("proxy: invalid input")

// validTypes is the set of accepted proxy schemes (mirrors buildTransport).
var validTypes = map[string]struct{}{
	domain.ProxyTypeHTTP:    {},
	domain.ProxyTypeHTTPS:   {},
	domain.ProxyTypeSOCKS5:  {},
	domain.ProxyTypeSOCKS5H: {},
}

// Service persists proxy resources, encrypting the URL at rest.
type Service struct {
	db      dbinterface.Querier
	repo    database.Proxies
	keyring *secrets.Keyring
	clock   func() time.Time
}

// NewService wires the proxy service.
func NewService(db dbinterface.Querier, keyring *secrets.Keyring) *Service {
	return &Service{db: db, keyring: keyring, clock: time.Now}
}

// List returns all proxies (URLs stay encrypted; the handler redacts).
func (s *Service) List(ctx context.Context) ([]domain.Proxy, error) {
	out, err := s.repo.ListProxies(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("proxy: list: %w", err)
	}
	return out, nil
}

// Get returns one proxy by id.
func (s *Service) Get(ctx context.Context, id int64) (domain.Proxy, error) {
	p, err := s.repo.GetProxy(ctx, s.db, id)
	if err != nil {
		return domain.Proxy{}, fmt.Errorf("proxy: get: %w", err)
	}
	return p, nil
}

// CreateParams is the input to Create.
type CreateParams struct {
	Name string
	Type string
	URL  string
}

// Create persists a proxy with its URL encrypted: the row is written first (to
// mint the id the AAD binds to), then the sealed secret, in one transaction.
func (s *Service) Create(ctx context.Context, p CreateParams) (domain.Proxy, error) {
	p.Name, p.Type, p.URL = strings.TrimSpace(p.Name), strings.TrimSpace(p.Type), strings.TrimSpace(p.URL)
	if err := validate(p.Name, p.Type, p.URL); err != nil {
		return domain.Proxy{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Proxy{}, fmt.Errorf("proxy: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.clock()
	row := domain.Proxy{Name: p.Name, Type: p.Type, CreatedAt: now, UpdatedAt: now}
	id, err := s.repo.InsertProxy(ctx, tx, row)
	if err != nil {
		return domain.Proxy{}, fmt.Errorf("proxy: insert: %w", err)
	}
	row.ID = id

	enc, err := s.keyring.Encrypt(id, domain.ProxySecretURL, p.URL)
	if err != nil {
		return domain.Proxy{}, fmt.Errorf("proxy: encrypt url: %w", err)
	}
	if err := s.repo.SetProxySecret(ctx, tx, id, enc, s.keyring.KeyID()); err != nil {
		return domain.Proxy{}, fmt.Errorf("proxy: set secret: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Proxy{}, fmt.Errorf("proxy: commit: %w", err)
	}
	row.URLEncrypted, row.KeyID = enc, s.keyring.KeyID()
	return row, nil
}

// UpdateParams patches a proxy; nil fields are left unchanged. URL rotates the
// endpoint (re-encrypted in place).
type UpdateParams struct {
	Name *string
	Type *string
	URL  *string
}

// Update applies a patch, re-encrypting the URL when rotated.
func (s *Service) Update(ctx context.Context, id int64, p UpdateParams) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("proxy: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row, err := s.repo.GetProxy(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("proxy: get: %w", err)
	}
	if p.Name != nil {
		row.Name = strings.TrimSpace(*p.Name)
	}
	if p.Type != nil {
		row.Type = strings.TrimSpace(*p.Type)
	}
	newURL := ""
	if p.URL != nil {
		newURL = strings.TrimSpace(*p.URL)
	}
	if err := validate(row.Name, row.Type, urlForValidation(p.URL, newURL)); err != nil {
		return err
	}

	row.UpdatedAt = s.clock()
	if p.URL != nil {
		enc, err := s.keyring.Encrypt(id, domain.ProxySecretURL, newURL)
		if err != nil {
			return fmt.Errorf("proxy: encrypt url: %w", err)
		}
		row.URLEncrypted, row.KeyID = enc, s.keyring.KeyID()
	}
	if err := s.repo.UpdateProxy(ctx, tx, row); err != nil {
		return fmt.Errorf("proxy: update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("proxy: commit: %w", err)
	}
	return nil
}

// Delete removes a proxy; referencing instances' proxy_id is nulled by the FK.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.repo.DeleteProxy(ctx, s.db, id); err != nil {
		return fmt.Errorf("proxy: delete: %w", err)
	}
	return nil
}

// urlForValidation returns the URL to validate: the new value when rotating,
// otherwise a non-empty placeholder so an unchanged (still-encrypted) URL passes.
func urlForValidation(patch *string, newURL string) string {
	if patch == nil {
		return "unchanged://ok"
	}
	return newURL
}

// validate enforces name, an accepted type, and a parseable absolute URL.
func validate(name, typ, rawURL string) error {
	if name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if _, ok := validTypes[typ]; !ok {
		return fmt.Errorf("%w: unknown proxy type %q", ErrInvalid, typ)
	}
	if rawURL == "" {
		return fmt.Errorf("%w: url is required", ErrInvalid)
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: url must be an absolute URL", ErrInvalid)
	}
	return nil
}
