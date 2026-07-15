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

	"github.com/autobrr/harbrr/internal/connresource"
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

// Service persists proxy resources, encrypting the URL at rest. Create/Update of
// the row and its encrypted secret are sequenced by connresource.Lifecycle (Delete
// stays a bare repo delete — see Delete's doc).
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
	if err := validate(p.Name, p.Type, &p.URL); err != nil {
		return domain.Proxy{}, err
	}
	return s.life.Create(ctx, connresource.CreateSpec[domain.Proxy]{
		Build: func(now time.Time, _ int64) domain.Proxy {
			return domain.Proxy{Name: p.Name, Type: p.Type, CreatedAt: now, UpdatedAt: now}
		},
		Insert: func(ctx context.Context, q dbinterface.Execer, row domain.Proxy) (int64, error) {
			return s.repo.InsertProxy(ctx, q, row)
		},
		Secrets: func(_ domain.Proxy, _ string) []connresource.Secret {
			return []connresource.Secret{{Discriminator: domain.ProxySecretURL, Plaintext: p.URL}}
		},
		SetSecrets: func(ctx context.Context, q dbinterface.Execer, id int64, encrypted []string, keyID string) error {
			return s.repo.SetProxySecret(ctx, q, id, encrypted[0], keyID)
		},
		Finalize: func(row domain.Proxy, id int64, encrypted []string, keyID string) domain.Proxy {
			row.ID, row.URLEncrypted, row.KeyID = id, encrypted[0], keyID
			return row
		},
	})
}

// UpdateParams patches a proxy; nil fields are left unchanged. URL rotates the
// endpoint (re-encrypted in place).
type UpdateParams struct {
	Name *string
	Type *string
	URL  *string
}

// Update applies a patch, re-encrypting the URL when rotated. The read and the
// full-row write run in one transaction so two overlapping PATCHes can't lose each
// other's write.
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
			if err := validate(row.Name, row.Type, nil); err != nil {
				return err
			}
			// A type-only change (URL patch omitted) still has to be family-compatible
			// with the STORED url, which validate skips when passed a nil rawURL.
			// Decrypt it and re-check, so a type flip that would fail at search (e.g.
			// http -> socks5 over an http:// url) is rejected at save instead. The
			// error never includes the decrypted value.
			if p.Type != nil && p.URL == nil {
				stored, err := s.keyring.Decrypt(row.ID, domain.ProxySecretURL, row.URLEncrypted)
				if err != nil {
					return fmt.Errorf("proxy: decrypt url: %w", err)
				}
				if err := validateURL(row.Type, stored); err != nil {
					return err
				}
			}
			return nil
		},
		Rotate: func(row *domain.Proxy) (connresource.Secret, bool, error) {
			if p.URL == nil {
				return connresource.Secret{}, false, nil
			}
			newURL := strings.TrimSpace(*p.URL)
			if err := validateURL(row.Type, newURL); err != nil {
				return connresource.Secret{}, false, err
			}
			return connresource.Secret{Discriminator: domain.ProxySecretURL, Plaintext: newURL}, true, nil
		},
		Apply: func(row *domain.Proxy, encrypted, keyID string) { row.URLEncrypted, row.KeyID = encrypted, keyID },
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

// validate enforces name, an accepted type, and — when the URL is being set — a
// URL whose scheme is present and in the same family as the type. A nil rawURL
// means the URL is not changing (an update patch omitted it), so the
// already-validated stored value is left untouched.
func validate(name, typ string, rawURL *string) error {
	if name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if _, ok := validTypes[typ]; !ok {
		return fmt.Errorf("%w: unknown proxy type %q", ErrInvalid, typ)
	}
	if rawURL == nil {
		return nil
	}
	return validateURL(typ, *rawURL)
}

// validateURL checks the proxy URL is absolute (host + scheme) and its scheme is
// in the same family as the type. buildTransport (internal/indexer/registry)
// routes {http,https} through http.ProxyURL, which honors the URL scheme, and
// {socks5,socks5h} through proxy.FromURL, which needs a socks scheme — so a
// scheme-less URL or a cross-family scheme fails at search time on every
// referencing indexer. Rejecting it here fails at save instead. The error never
// includes the URL value (it can embed user:pass) — only the safe scheme token.
func validateURL(typ, rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("%w: url is required", ErrInvalid)
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: url must be an absolute URL", ErrInvalid)
	}
	if u.Scheme == "" {
		return fmt.Errorf("%w: url must include a scheme (e.g. socks5:// or http://)", ErrInvalid)
	}
	if proxyFamily(typ) != proxyFamily(u.Scheme) {
		return fmt.Errorf("%w: type %q requires a %s url, got scheme %q", ErrInvalid, typ, expectedSchemes(typ), u.Scheme)
	}
	return nil
}

// proxyFamily maps a proxy type or URL scheme to the transport family the search
// build uses: "http" for http/https (http.ProxyURL), "socks" for socks5/socks5h
// (proxy.FromURL), or "" for anything else. A validated type is always "http" or
// "socks", so a foreign scheme (family "") never matches and is rejected.
func proxyFamily(s string) string {
	switch s {
	case domain.ProxyTypeHTTP, domain.ProxyTypeHTTPS:
		return "http"
	case domain.ProxyTypeSOCKS5, domain.ProxyTypeSOCKS5H:
		return "socks"
	default:
		return ""
	}
}

// expectedSchemes names the accepted URL schemes for a type, for the error text.
func expectedSchemes(typ string) string {
	if proxyFamily(typ) == "socks" {
		return "socks5:// or socks5h://"
	}
	return "http:// or https://"
}
