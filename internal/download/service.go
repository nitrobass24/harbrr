package download

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/connresource"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// Service persists download clients (encrypting the client's secret) and builds
// their Driver on demand. Create/Update/Delete of the row and its encrypted
// secret are sequenced by connresource.Lifecycle; download mints nothing (like
// notify), so its specs simply leave Minter nil.
type Service struct {
	db      dbinterface.Querier
	repo    database.DownloadClients
	keyring *secrets.Keyring
	client  *http.Client
	clock   func() time.Time
	life    *connresource.Lifecycle[domain.DownloadClient]
	log     zerolog.Logger
}

// NewService wires the download service. client is shared by drivers thin enough
// to use one (nil installs a timeout-bounded default); clock is injectable for
// deterministic tests (assigning to the returned Service's clock field also
// retunes its Lifecycle, which reads clock through an indirection).
func NewService(db dbinterface.Querier, keyring *secrets.Keyring, client *http.Client, log zerolog.Logger) *Service {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	s := &Service{db: db, keyring: keyring, client: client, clock: time.Now, log: log}
	s.life = connresource.New[domain.DownloadClient](db, keyring, func() time.Time { return s.clock() })
	return s
}

// CreateParams is the input to Create. Secret is optional (a credential-free
// qBittorrent instance behind a localhost bypass is valid).
type CreateParams struct {
	Name     string
	Kind     string
	Host     string
	Username string
	Secret   string
	Settings domain.DownloadClientSettings
}

// Create persists a client with its secret encrypted: the row is written first
// (to mint the id the AAD binds to), then the sealed secret, in one transaction.
func (s *Service) Create(ctx context.Context, p CreateParams) (domain.DownloadClient, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Kind = strings.TrimSpace(p.Kind)
	p.Host = strings.TrimSpace(p.Host)
	p.Username = strings.TrimSpace(p.Username)
	if err := validate(p.Name, p.Kind, p.Host, p.Settings); err != nil {
		return domain.DownloadClient{}, err
	}
	return s.life.Create(ctx, connresource.CreateSpec[domain.DownloadClient]{
		Build: func(now time.Time, _ int64) domain.DownloadClient {
			return domain.DownloadClient{
				Name: p.Name, Kind: p.Kind, Enabled: true, Host: p.Host, Username: p.Username,
				Settings: p.Settings, CreatedAt: now, UpdatedAt: now,
			}
		},
		Insert: func(ctx context.Context, q dbinterface.Execer, c domain.DownloadClient) (int64, error) {
			return s.repo.InsertDownloadClient(ctx, q, c)
		},
		Secrets: func(_ domain.DownloadClient, _ string) []connresource.Secret {
			return []connresource.Secret{{Discriminator: domain.DownloadClientSecret, Plaintext: p.Secret}}
		},
		SetSecrets: func(ctx context.Context, q dbinterface.Execer, id int64, encrypted []string, keyID string) error {
			return s.repo.SetDownloadClientSecret(ctx, q, id, encrypted[0], keyID)
		},
		Finalize: func(c domain.DownloadClient, id int64, encrypted []string, keyID string) domain.DownloadClient {
			c.ID, c.SecretEncrypted, c.KeyID = id, encrypted[0], keyID
			return c
		},
		Conflict: func(_ domain.DownloadClient) error {
			return fmt.Errorf("%w: a download client named %q already exists", domain.ErrConflict, p.Name)
		},
	})
}

// UpdateParams patches a client; nil fields are left unchanged. Kind is
// immutable (no field here — the UI disables the kind select on edit). Secret is
// nil = keep stored; a non-nil value (including "") rotates it.
type UpdateParams struct {
	Name     *string
	Host     *string
	Username *string
	Secret   *string
	Settings *domain.DownloadClientSettings
}

// Update applies a patch, re-encrypting the secret when rotated. The read and
// the full-row write run in one transaction so two overlapping PATCHes can't
// lose each other's write.
func (s *Service) Update(ctx context.Context, id int64, p UpdateParams) error {
	return s.life.Update(ctx, id, connresource.UpdateSpec[domain.DownloadClient]{
		Get: func(ctx context.Context, q dbinterface.Execer, id int64) (domain.DownloadClient, error) {
			return s.repo.GetDownloadClient(ctx, q, id)
		},
		Patch: func(c *domain.DownloadClient) error {
			if p.Name != nil {
				c.Name = strings.TrimSpace(*p.Name)
			}
			if p.Host != nil {
				c.Host = strings.TrimSpace(*p.Host)
			}
			if p.Username != nil {
				c.Username = strings.TrimSpace(*p.Username)
			}
			if p.Settings != nil {
				c.Settings = *p.Settings
			}
			return validate(c.Name, c.Kind, c.Host, c.Settings)
		},
		Rotate: func(_ *domain.DownloadClient) (connresource.Secret, bool, error) {
			if p.Secret == nil {
				return connresource.Secret{}, false, nil
			}
			return connresource.Secret{Discriminator: domain.DownloadClientSecret, Plaintext: *p.Secret}, true, nil
		},
		Apply: func(c *domain.DownloadClient, encrypted, keyID string) { c.SecretEncrypted, c.KeyID = encrypted, keyID },
		Touch: func(c *domain.DownloadClient, now time.Time) { c.UpdatedAt = now },
		Write: func(ctx context.Context, q dbinterface.Execer, c domain.DownloadClient) error {
			return s.repo.UpdateDownloadClient(ctx, q, c)
		},
		Conflict: func(c domain.DownloadClient) error {
			return fmt.Errorf("%w: a download client named %q already exists", domain.ErrConflict, c.Name)
		},
	})
}

// List returns all clients (the secret stays encrypted; the handler redacts it).
func (s *Service) List(ctx context.Context) ([]domain.DownloadClient, error) {
	list, err := s.repo.ListDownloadClients(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("download: list: %w", err)
	}
	return list, nil
}

// Get returns one client by id.
func (s *Service) Get(ctx context.Context, id int64) (domain.DownloadClient, error) {
	c, err := s.repo.GetDownloadClient(ctx, s.db, id)
	if err != nil {
		return domain.DownloadClient{}, fmt.Errorf("download: get: %w", err)
	}
	return c, nil
}

// SetEnabled toggles a client's enabled flag.
func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	if err := s.repo.SetDownloadClientEnabled(ctx, s.db, id, enabled, s.clock()); err != nil {
		return fmt.Errorf("download: set enabled: %w", err)
	}
	return nil
}

// Delete removes a client by id (a bare repo delete — download mints nothing to
// revoke, mirroring notify's DeleteNotification).
func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.life.Delete(ctx, id, connresource.DeleteSpec[domain.DownloadClient]{
		Get: func(ctx context.Context, q dbinterface.Execer, id int64) (domain.DownloadClient, error) {
			return s.repo.GetDownloadClient(ctx, q, id)
		},
		Delete: func(ctx context.Context, q dbinterface.Execer, id int64) error {
			return s.repo.DeleteDownloadClient(ctx, q, id)
		},
	})
}

// TestConnection decrypts a client's secret and confirms its driver can reach it.
// Decrypt-then-build stays a Service seam: a future sync-to-apps feature (#237)
// will need the plaintext secret at sync time, reached the same way.
func (s *Service) TestConnection(ctx context.Context, id int64) error {
	c, err := s.repo.GetDownloadClient(ctx, s.db, id)
	if err != nil {
		return fmt.Errorf("download: get: %w", err)
	}
	secret, err := s.keyring.Decrypt(c.ID, domain.DownloadClientSecret, c.SecretEncrypted)
	if err != nil {
		return fmt.Errorf("download: decrypt secret: %w", err)
	}
	driver, err := newDriver(c, secret, s.client)
	if err != nil {
		return err
	}
	if err := driver.Test(ctx); err != nil {
		return fmt.Errorf("download: test connection: %w", err)
	}
	return nil
}

// validate enforces a name, a registered kind, an absolute http(s) host, and
// settings matching the kind.
func validate(name, kind, host string, settings domain.DownloadClientSettings) error {
	if name == "" {
		return fmt.Errorf("%w: name is required", domain.ErrInvalid)
	}
	if !validateKind(kind) {
		return fmt.Errorf("%w: unknown or unregistered download client kind %q", domain.ErrInvalid, kind)
	}
	if _, err := domain.ValidateAbsURL("host", host); err != nil {
		return err
	}
	return validateSettings(kind, settings)
}

// validateSettings rejects a populated settings field that doesn't match kind.
func validateSettings(kind string, settings domain.DownloadClientSettings) error {
	if settings.QBittorrent != nil && kind != domain.DownloadClientKindQBittorrent {
		return fmt.Errorf("%w: qbittorrent settings given for kind %q", domain.ErrInvalid, kind)
	}
	if settings.Sabnzbd != nil && kind != domain.DownloadClientKindSabnzbd {
		return fmt.Errorf("%w: sabnzbd settings given for kind %q", domain.ErrInvalid, kind)
	}
	if settings.NZBGet != nil && kind != domain.DownloadClientKindNZBGet {
		return fmt.Errorf("%w: nzbget settings given for kind %q", domain.ErrInvalid, kind)
	}
	return nil
}
