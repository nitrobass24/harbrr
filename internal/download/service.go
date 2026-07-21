package download

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/apps"
	"github.com/autobrr/harbrr/internal/connresource"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// Service persists download clients and builds their Driver on demand. A networked
// client's identity + credential live on a first-class App (ADR 0004) referenced by
// app_id; host-less kinds (blackhole) have no App. Create/Update/Delete of the row are
// sequenced by connresource.Lifecycle; download mints nothing (Minter nil), and the
// credential is sealed on the App, not the row.
type Service struct {
	db     dbinterface.Querier
	repo   database.DownloadClients
	apps   *apps.Service
	client *http.Client
	clock  func() time.Time
	life   *connresource.Lifecycle[domain.DownloadClient]
	log    zerolog.Logger
}

// NewService wires the download service. client is shared by drivers thin enough
// to use one (nil installs a timeout-bounded default); clock is injectable for
// deterministic tests (assigning to the returned Service's clock field also
// retunes its Lifecycle, which reads clock through an indirection).
func NewService(db dbinterface.Querier, appsSvc *apps.Service, keyring *secrets.Keyring, client *http.Client, log zerolog.Logger) *Service {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	s := &Service{db: db, apps: appsSvc, client: client, clock: time.Now, log: log}
	s.life = connresource.New[domain.DownloadClient](db, keyring, func() time.Time { return s.clock() })
	return s
}

// CreateParams is the input to Create. A networked kind references an App either by
// AppID (reuse) or by the inline Host/Username/Secret (get-or-create by identity);
// Secret is optional (a credential-free qBittorrent behind a localhost bypass is
// valid). A host-less kind (blackhole) takes none of these — Settings only.
type CreateParams struct {
	Name     string
	Kind     string
	AppID    *int64
	Host     string
	Username string
	Secret   string
	Settings domain.DownloadClientSettings
}

// Create persists a client. For a networked kind it get-or-creates the App holding the
// identity/credential and stores only app_id on the row; a host-less kind stores
// neither. The row insert runs in the Lifecycle transaction for its name-uniqueness
// mapping (it seals no per-row secret — the credential lives on the App).
func (s *Service) Create(ctx context.Context, p CreateParams) (domain.DownloadClient, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Kind = strings.TrimSpace(p.Kind)
	p.Host = strings.TrimSpace(p.Host)
	p.Username = strings.TrimSpace(p.Username)
	if err := validate(p.Name, p.Kind, p.Host, p.Settings, p.AppID); err != nil {
		return domain.DownloadClient{}, err
	}
	app, err := s.resolveApp(ctx, p)
	if err != nil {
		return domain.DownloadClient{}, err
	}
	var appID *int64
	if app != nil {
		appID = &app.ID
	}
	return s.life.Create(ctx, connresource.CreateSpec[domain.DownloadClient]{
		Build: func(now time.Time, _ int64) domain.DownloadClient {
			return domain.DownloadClient{
				Name: p.Name, Kind: p.Kind, AppID: appID, Enabled: true,
				Settings: p.Settings, CreatedAt: now, UpdatedAt: now,
			}
		},
		Insert: func(ctx context.Context, q dbinterface.Execer, c domain.DownloadClient) (int64, error) {
			return s.repo.InsertDownloadClient(ctx, q, c)
		},
		// The credential is sealed on the App, not the row: the row seals nothing.
		Secrets:    func(_ domain.DownloadClient, _ string) []connresource.Secret { return nil },
		SetSecrets: func(context.Context, dbinterface.Execer, int64, []string, string) error { return nil },
		// Hydrate host/username from the App so the create response matches List/Get.
		Finalize: func(c domain.DownloadClient, id int64, _ []string, _ string) domain.DownloadClient {
			c.ID = id
			if app != nil {
				c.Host, c.Username = app.BaseURL, app.Username
			}
			return c
		},
		Conflict: func(_ domain.DownloadClient) error {
			return fmt.Errorf("%w: a download client named %q already exists", domain.ErrConflict, p.Name)
		},
	})
}

// resolveApp get-or-creates the App for a networked create, returning nil for a
// host-less kind (which has no App).
func (s *Service) resolveApp(ctx context.Context, p CreateParams) (*domain.App, error) {
	if hostless(p.Kind) {
		return nil, nil //nolint:nilnil // host-less kinds intentionally carry no App.
	}
	app, err := s.apps.Resolve(ctx, apps.Ref{
		AppID: p.AppID, Kind: p.Kind, Name: p.Name, BaseURL: p.Host, Username: p.Username, APIKey: p.Secret,
	})
	if err != nil {
		return nil, fmt.Errorf("download: resolve app: %w", err)
	}
	return &app, nil
}

// UpdateParams patches a client; nil fields are left unchanged. Identity and
// credential are App-level now (rotated via the App), so only surface fields remain:
// Name and the kind-specific Settings. Kind is immutable.
type UpdateParams struct {
	Name     *string
	Settings *domain.DownloadClientSettings
}

// Update applies a patch to the row's surface fields. The read and the full-row write
// run in one transaction so two overlapping PATCHes can't lose each other's write.
func (s *Service) Update(ctx context.Context, id int64, p UpdateParams) error {
	return s.life.Update(ctx, id, connresource.UpdateSpec[domain.DownloadClient]{
		Get: func(ctx context.Context, q dbinterface.Execer, id int64) (domain.DownloadClient, error) {
			return s.repo.GetDownloadClient(ctx, q, id)
		},
		Patch: func(c *domain.DownloadClient) error {
			if p.Name != nil {
				c.Name = strings.TrimSpace(*p.Name)
			}
			if p.Settings != nil {
				c.Settings = *p.Settings
			}
			return validateNameKindSettings(c.Name, c.Kind, c.Settings)
		},
		Touch: func(c *domain.DownloadClient, now time.Time) { c.UpdatedAt = now },
		Write: func(ctx context.Context, q dbinterface.Execer, c domain.DownloadClient) error {
			return s.repo.UpdateDownloadClient(ctx, q, c)
		},
		Conflict: func(c domain.DownloadClient) error {
			return fmt.Errorf("%w: a download client named %q already exists", domain.ErrConflict, c.Name)
		},
	})
}

// dcAppID and dcApplyApp are the App-projection accessors apps.EnrichList/EnrichOne
// need: which field on the row holds the App reference, and which fields the App
// projects onto it.
func dcAppID(c *domain.DownloadClient) *int64 { return c.AppID }

func dcApplyApp(c *domain.DownloadClient, a domain.App) { c.Host, c.Username = a.BaseURL, a.Username }

// List returns all clients, each networked one's host/username enriched from its App
// (a single App lookup shared across the list). Blackhole rows keep blank host.
func (s *Service) List(ctx context.Context) ([]domain.DownloadClient, error) {
	list, err := s.repo.ListDownloadClients(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("download: list: %w", err)
	}
	if err := apps.EnrichList(ctx, s.apps, list, dcAppID, dcApplyApp); err != nil {
		return nil, fmt.Errorf("download: enrich clients: %w", err)
	}
	return list, nil
}

// Get returns one client by id, its host/username enriched from its App.
func (s *Service) Get(ctx context.Context, id int64) (domain.DownloadClient, error) {
	c, err := s.repo.GetDownloadClient(ctx, s.db, id)
	if err != nil {
		return domain.DownloadClient{}, fmt.Errorf("download: get: %w", err)
	}
	if err := apps.EnrichOne(ctx, s.apps, &c, dcAppID, dcApplyApp); err != nil {
		return domain.DownloadClient{}, fmt.Errorf("download: enrich client: %w", err)
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

// TestConnection builds a client's driver (resolving identity + credential from its
// App for a networked kind) and confirms it can reach the client.
func (s *Service) TestConnection(ctx context.Context, id int64) error {
	c, err := s.repo.GetDownloadClient(ctx, s.db, id)
	if err != nil {
		return fmt.Errorf("download: get: %w", err)
	}
	driver, err := s.buildDriver(ctx, c)
	if err != nil {
		return err
	}
	if err := driver.Test(ctx); err != nil {
		return fmt.Errorf("download: test connection: %w", err)
	}
	return nil
}

// buildDriver resolves a client's driver: a host-less kind uses its Settings only; a
// networked kind loads its App for the host + decrypted credential. AppID is never nil
// on a networked row here: migration 0021 refuses to apply while one still is, so this
// dereference is safe by construction, not defensively guarded.
func (s *Service) buildDriver(ctx context.Context, c domain.DownloadClient) (Driver, error) {
	if hostless(c.Kind) {
		return newDriver(c, "", s.client)
	}
	app, secret, err := s.apps.Bind(ctx, *c.AppID)
	if err != nil {
		return nil, fmt.Errorf("download: bind app: %w", err)
	}
	c.Host, c.Username = app.BaseURL, app.Username
	return newDriver(c, secret, s.client)
}

// hostless reports whether a kind has no network endpoint of its own (blackhole), and
// therefore no App.
func hostless(kind string) bool { return drivers[kind].host == hostNone }

// validate enforces a name, a registered kind, a host/app reference appropriate to the
// kind, and settings matching the kind.
func validate(name, kind, host string, settings domain.DownloadClientSettings, appID *int64) error {
	if err := validateNameKindSettings(name, kind, settings); err != nil {
		return err
	}
	return validateHost(kind, host, appID)
}

// validateNameKindSettings checks the surface fields shared by create and update.
func validateNameKindSettings(name, kind string, settings domain.DownloadClientSettings) error {
	if name == "" {
		return fmt.Errorf("%w: name is required", domain.ErrInvalid)
	}
	if !validateKind(kind) {
		return fmt.Errorf("%w: unknown or unregistered download client kind %q", domain.ErrInvalid, kind)
	}
	return validateSettings(kind, settings)
}

// validateHost checks the create-time identity reference. A host-less kind
// (blackhole) must carry no host or app. A networked kind references its App either by
// app_id (reuse — host optional/ignored) or by an inline host validated against the
// kind's hostMode (get-or-create).
func validateHost(kind, host string, appID *int64) error {
	if hostless(kind) {
		if host != "" || appID != nil {
			return fmt.Errorf("%w: host-less kind %q takes no host or app", domain.ErrInvalid, kind)
		}
		return nil
	}
	if appID != nil {
		return nil
	}
	if host == "" {
		return fmt.Errorf("%w: host or app is required for kind %q", domain.ErrInvalid, kind)
	}
	// Only two host modes reach here (hostNone was handled above): a "host:port"
	// kind or the default absolute-URL kind.
	if drivers[kind].host == hostPort {
		return validateHostPort(host)
	}
	_, err := domain.ValidateAbsURL("host", host)
	return err
}

// validateHostPort enforces a non-empty host and a numeric port in 1-65535.
// net.SplitHostPort alone is not enough: it accepts ":58846" (empty host),
// "localhost:" (empty port), and "localhost:abc" (non-numeric port).
func validateHostPort(host string) error {
	h, portStr, err := net.SplitHostPort(host)
	if err != nil || h == "" {
		return fmt.Errorf("%w: host must be host:port (e.g. localhost:58846)", domain.ErrInvalid)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%w: host must be host:port with a numeric port 1-65535 (e.g. localhost:58846)", domain.ErrInvalid)
	}
	return nil
}

// validateSettings rejects a populated settings field that doesn't match kind,
// enforces the one required field among the kind-specific settings (qui's
// InstanceID — qui is keyed by int instance id, so an unset/zero id can never be
// a valid target), and validates the kind-specific settings that need it.
func validateSettings(kind string, settings domain.DownloadClientSettings) error {
	// One row per settings shape: a populated field whose kind doesn't own it is a
	// mismatch. Adding a kind is one row here, not another guard clause.
	mismatches := []struct {
		name string
		set  bool
		kind string
	}{
		{"qbittorrent", settings.QBittorrent != nil, domain.DownloadClientKindQBittorrent},
		{"blackhole", settings.Blackhole != nil, domain.DownloadClientKindBlackhole},
		{"sabnzbd", settings.Sabnzbd != nil, domain.DownloadClientKindSabnzbd},
		{"nzbget", settings.NZBGet != nil, domain.DownloadClientKindNZBGet},
		{"qui", settings.Qui != nil, domain.DownloadClientKindQui},
		{"flood", settings.Flood != nil, domain.DownloadClientKindFlood},
		{"download-station", settings.DownloadStation != nil, domain.DownloadClientKindDownloadStation},
		{"transmission", settings.Transmission != nil, domain.DownloadClientKindTransmission},
		{"deluge", settings.Deluge != nil, domain.DownloadClientKindDeluge},
		{"rtorrent", settings.RTorrent != nil, domain.DownloadClientKindRTorrent},
	}
	for _, m := range mismatches {
		if m.set && kind != m.kind {
			return fmt.Errorf("%w: %s settings given for kind %q", domain.ErrInvalid, m.name, kind)
		}
	}
	switch kind {
	case domain.DownloadClientKindQui:
		if settings.Qui == nil || settings.Qui.InstanceID <= 0 {
			return fmt.Errorf("%w: qui settings instanceId must be > 0", domain.ErrInvalid)
		}
	case domain.DownloadClientKindBlackhole:
		return validateBlackholeSettings(settings.Blackhole)
	}
	return nil
}

// validateBlackholeSettings requires at least one watch-folder dir and rejects
// a relative one. It deliberately does not check the dir exists — the folder
// may be a mount that comes and goes; Test is the probe for that.
func validateBlackholeSettings(s *domain.BlackholeSettings) error {
	if s == nil || (s.TorrentDir == "" && s.NZBDir == "") {
		return fmt.Errorf("%w: blackhole requires at least one of torrentDir/nzbDir", domain.ErrInvalid)
	}
	for _, dir := range []string{s.TorrentDir, s.NZBDir} {
		if dir != "" && !filepath.IsAbs(dir) {
			return fmt.Errorf("%w: blackhole directories must be absolute paths", domain.ErrInvalid)
		}
	}
	return nil
}
