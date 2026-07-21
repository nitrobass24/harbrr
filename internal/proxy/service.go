// Package proxy manages global, reusable proxy resources that indexer instances
// reference by id. It owns CRUD + at-rest encryption of the proxy password (host,
// port, and username are plain — only the password routinely doubles as a
// credential worth hiding); the engine resolves a referenced proxy into the
// per-request transport config (internal/indexer/registry), and the auto-migration
// folds legacy inline proxy settings into these resources.
package proxy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/autobrr/harbrr/internal/connresource"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// ErrInvalid is the sentinel the API maps to 400 for malformed input. It wraps
// domain.ErrInvalid so the api layer's writeServiceError only needs to check the
// domain sentinel.
var ErrInvalid = fmt.Errorf("proxy: %w", domain.ErrInvalid)

// validTypes is the set of accepted proxy schemes (mirrors buildTransport). The
// type IS the transport scheme now — there is no separate URL to cross-check it
// against.
var validTypes = map[string]struct{}{
	domain.ProxyTypeHTTP:    {},
	domain.ProxyTypeHTTPS:   {},
	domain.ProxyTypeSOCKS5:  {},
	domain.ProxyTypeSOCKS5H: {},
}

// Service persists proxy resources, encrypting the password at rest. Create/Update
// of the row and its encrypted secret are sequenced by connresource.Lifecycle
// (Delete stays a bare repo delete — see Delete's doc).
type Service struct {
	db      dbinterface.Querier
	repo    database.Proxies
	keyring *secrets.Keyring
	clock   func() time.Time
	life    *connresource.Lifecycle[domain.Proxy]
}

// NewService wires the proxy service. clock is injectable for deterministic tests
// (assigning to the returned Service's clock field also retunes its Lifecycle,
// which reads clock through an indirection).
func NewService(db dbinterface.Querier, keyring *secrets.Keyring) *Service {
	s := &Service{db: db, keyring: keyring, clock: time.Now}
	s.life = connresource.New[domain.Proxy](db, keyring, func() time.Time { return s.clock() })
	return s
}

// List returns all proxies (password stays encrypted; the handler omits it).
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

// CreateParams is the input to Create. Password is optional (a credential-free
// proxy is valid).
type CreateParams struct {
	Name     string
	Type     string
	Host     string
	Port     int
	Username string
	Password string
}

// Create persists a proxy with its password encrypted: the row is written first
// (to mint the id the AAD binds to), then the sealed secret, in one transaction.
func (s *Service) Create(ctx context.Context, p CreateParams) (domain.Proxy, error) {
	p.Name, p.Type, p.Host, p.Username = strings.TrimSpace(p.Name), strings.TrimSpace(p.Type), strings.TrimSpace(p.Host), strings.TrimSpace(p.Username)
	if err := validate(p.Name, p.Type, p.Host, p.Port); err != nil {
		return domain.Proxy{}, err
	}
	return s.life.Create(ctx, connresource.CreateSpec[domain.Proxy]{
		Build: func(now time.Time, _ int64) domain.Proxy {
			return domain.Proxy{Name: p.Name, Type: p.Type, Host: p.Host, Port: p.Port, Username: p.Username, CreatedAt: now, UpdatedAt: now}
		},
		Insert: func(ctx context.Context, q dbinterface.Execer, row domain.Proxy) (int64, error) {
			return s.repo.InsertProxy(ctx, q, row)
		},
		Secrets: func(_ domain.Proxy, _ string) []connresource.Secret {
			return []connresource.Secret{{Discriminator: domain.ProxySecretPassword, Plaintext: p.Password}}
		},
		SetSecrets: func(ctx context.Context, q dbinterface.Execer, id int64, encrypted []string, keyID string) error {
			return s.repo.SetProxySecret(ctx, q, id, encrypted[0], keyID)
		},
		Finalize: func(row domain.Proxy, id int64, encrypted []string, keyID string) domain.Proxy {
			row.ID, row.PasswordEncrypted, row.KeyID = id, encrypted[0], keyID
			return row
		},
	})
}

// UpdateParams patches a proxy; nil fields are left unchanged. Password is nil =
// keep stored; a non-nil value (including "") rotates it, so an explicit empty
// string clears a proxy back to credential-free.
type UpdateParams struct {
	Name     *string
	Type     *string
	Host     *string
	Port     *int
	Username *string
	Password *string
}

// Update applies a patch, re-encrypting the password when rotated. The read and
// the full-row write run in one transaction so two overlapping PATCHes can't lose
// each other's write.
func (s *Service) Update(ctx context.Context, id int64, p UpdateParams) error {
	return s.life.Update(ctx, id, connresource.UpdateSpec[domain.Proxy]{
		Get: func(ctx context.Context, q dbinterface.Execer, id int64) (domain.Proxy, error) {
			return s.repo.GetProxy(ctx, q, id)
		},
		Patch: func(row *domain.Proxy) error {
			if p.Name != nil {
				row.Name = strings.TrimSpace(*p.Name)
			}
			if p.Type != nil {
				row.Type = strings.TrimSpace(*p.Type)
			}
			if p.Host != nil {
				row.Host = strings.TrimSpace(*p.Host)
			}
			if p.Port != nil {
				row.Port = *p.Port
			}
			if p.Username != nil {
				row.Username = strings.TrimSpace(*p.Username)
			}
			return validate(row.Name, row.Type, row.Host, row.Port)
		},
		Rotate: func(_ *domain.Proxy) (connresource.Secret, bool, error) {
			if p.Password == nil {
				return connresource.Secret{}, false, nil
			}
			return connresource.Secret{Discriminator: domain.ProxySecretPassword, Plaintext: *p.Password}, true, nil
		},
		Apply: func(row *domain.Proxy, encrypted, keyID string) { row.PasswordEncrypted, row.KeyID = encrypted, keyID },
		Touch: func(row *domain.Proxy, now time.Time) { row.UpdatedAt = now },
		Write: func(ctx context.Context, q dbinterface.Execer, row domain.Proxy) error {
			return s.repo.UpdateProxy(ctx, q, row)
		},
	})
}

// Delete removes a proxy; referencing instances' proxy_id is nulled by the FK.
func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.repo.DeleteProxy(ctx, s.db, id); err != nil {
		return fmt.Errorf("proxy: delete: %w", err)
	}
	return nil
}

// validate enforces a name, an accepted type, a non-empty host, and a port in
// 1..65535.
func validate(name, typ, host string, port int) error {
	if name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if _, ok := validTypes[typ]; !ok {
		return fmt.Errorf("%w: unknown proxy type %q", ErrInvalid, typ)
	}
	if host == "" {
		return fmt.Errorf("%w: host is required", ErrInvalid)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("%w: port must be between 1 and 65535", ErrInvalid)
	}
	return nil
}
