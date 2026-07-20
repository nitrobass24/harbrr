// Package apps owns the first-class App registry (ADR 0004): a (kind, base_url)
// external service harbrr connects to, stored once with a single sealed credential
// and its harbrr vantage, and referenced by the three surface tables (app-sync,
// announce, download) via app_id. The three surface services inject a *Service and
// resolve/decrypt through it, so an app's identity and credential live in one place
// and a rotation propagates to every surface that uses it.
package apps

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/connresource"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/secrets"
)

// httpClientTimeout bounds a single qui-instance proxy call.
const httpClientTimeout = 30 * time.Second

// Service persists Apps (encrypting the app's one credential under the app's own id)
// and hands decrypted identity to the surface services. Create/Update of the row and
// its sealed credential are sequenced by connresource.Lifecycle with Minter nil — an
// App mints no harbrr key of its own.
type Service struct {
	db      dbinterface.Querier
	repo    database.Apps
	keyring *secrets.Keyring
	client  *http.Client
	clock   func() time.Time
	life    *connresource.Lifecycle[domain.App]
	log     zerolog.Logger
}

// NewService wires the apps service. client is used only by the qui-instance proxy
// (nil installs a timeout-bounded default); clock is injectable for deterministic
// tests (assigning to the returned Service's clock field also retunes its Lifecycle).
func NewService(db dbinterface.Querier, keyring *secrets.Keyring, client *http.Client, log zerolog.Logger) *Service {
	if client == nil {
		client = &http.Client{Timeout: httpClientTimeout}
	}
	s := &Service{db: db, keyring: keyring, client: client, clock: time.Now, log: log}
	s.life = connresource.New[domain.App](db, keyring, func() time.Time { return s.clock() })
	return s
}

// Ref is a create-time reference to an App: either an existing id to reuse, or the
// inline identity to get-or-create by (Kind, BaseURL). APIKey is the app's credential
// (API key or password); a non-empty value on an existing app is authoritative and
// rotates the stored credential (ADR §3).
type Ref struct {
	AppID     *int64
	Kind      string
	Name      string
	BaseURL   string
	Username  string
	APIKey    string
	HarbrrURL string
}

// Resolve returns the App a surface create references: it loads an existing app by id
// (asserting the kind matches), or get-or-creates one by (Kind, BaseURL). On an
// existing app a typed (non-empty) credential rotates the stored one and a provided
// harbrr_url backfills an empty one; an empty credential reuses the app untouched. Its
// own transaction — an orphan App from a later-failing surface create is allowed
// (ADR §6), so the surface services keep their single insert-then-seal Hook.
func (s *Service) Resolve(ctx context.Context, ref Ref) (domain.App, error) {
	if ref.AppID != nil {
		app, err := s.repo.GetApp(ctx, s.db, *ref.AppID)
		if err != nil {
			return domain.App{}, fmt.Errorf("apps: resolve by id: %w", err)
		}
		if app.Kind != ref.Kind {
			return domain.App{}, fmt.Errorf("%w: app %d is a %s app, not %s", domain.ErrInvalid, app.ID, app.Kind, ref.Kind)
		}
		return app, nil
	}
	ref.BaseURL = strings.TrimSpace(ref.BaseURL)
	// Trim the credential once here so every downstream seal/copy (create's Secrets,
	// reconcile's rotate) stores the same value the blank/redacted checks validated —
	// a whitespace-padded paste must not be sealed verbatim.
	ref.APIKey = strings.TrimSpace(ref.APIKey)
	app, err := s.repo.GetAppByIdentity(ctx, s.db, ref.Kind, ref.BaseURL)
	switch {
	case err == nil:
		return s.reconcile(ctx, app, ref)
	case errors.Is(err, database.ErrNotFound):
		return s.createOrAdopt(ctx, ref)
	default:
		return domain.App{}, fmt.Errorf("apps: resolve by identity: %w", err)
	}
}

// createOrAdopt creates the app, or — on a concurrent create losing the unique race —
// re-looks-up the winner and reconciles the caller's inline fields into it.
func (s *Service) createOrAdopt(ctx context.Context, ref Ref) (domain.App, error) {
	app, err := s.create(ctx, ref)
	if err == nil {
		return app, nil
	}
	if !errors.Is(err, domain.ErrConflict) {
		return domain.App{}, err
	}
	existing, lookupErr := s.repo.GetAppByIdentity(ctx, s.db, ref.Kind, ref.BaseURL)
	if lookupErr != nil {
		return domain.App{}, fmt.Errorf("apps: re-lookup after concurrent create: %w", lookupErr)
	}
	return s.reconcile(ctx, existing, ref)
}

// create inserts a new App and seals its credential under the App's own id.
func (s *Service) create(ctx context.Context, ref Ref) (domain.App, error) {
	name := strings.TrimSpace(ref.Name)
	if name == "" {
		name = ref.Kind
	}
	return s.life.Create(ctx, connresource.CreateSpec[domain.App]{
		Build: func(now time.Time, _ int64) domain.App {
			return domain.App{
				Kind: ref.Kind, Name: name, BaseURL: ref.BaseURL, Username: strings.TrimSpace(ref.Username),
				HarbrrURL: strings.TrimSpace(ref.HarbrrURL), Enabled: true, CreatedAt: now, UpdatedAt: now,
			}
		},
		Insert: func(ctx context.Context, q dbinterface.Execer, a domain.App) (int64, error) {
			return s.repo.InsertApp(ctx, q, a)
		},
		Secrets: func(_ domain.App, _ string) []connresource.Secret {
			return []connresource.Secret{{Discriminator: domain.AppSecret, Plaintext: ref.APIKey}}
		},
		SetSecrets: func(ctx context.Context, q dbinterface.Execer, id int64, encrypted []string, keyID string) error {
			return s.repo.SetAppSecret(ctx, q, id, encrypted[0], keyID)
		},
		Finalize: func(a domain.App, id int64, encrypted []string, keyID string) domain.App {
			a.ID, a.APIKeyEncrypted, a.KeyID = id, encrypted[0], keyID
			return a
		},
		Conflict: func(a domain.App) error {
			return fmt.Errorf("%w: %s at %s", domain.ErrConflict, a.Kind, apphttp.RedactURL(a.BaseURL))
		},
	})
}

// reconcile folds a create's inline fields into an already-existing app: a typed
// (non-empty) credential rotates the stored one, and a provided harbrr_url backfills
// an empty one. An empty credential + already-set harbrr_url is a pure reuse.
func (s *Service) reconcile(ctx context.Context, app domain.App, ref Ref) (domain.App, error) {
	var p UpdateParams
	changed := false
	if strings.TrimSpace(ref.APIKey) != "" && !secrets.IsRedacted(strings.TrimSpace(ref.APIKey)) {
		p.APIKey, changed = &ref.APIKey, true
	}
	if app.HarbrrURL == "" && strings.TrimSpace(ref.HarbrrURL) != "" {
		h := strings.TrimSpace(ref.HarbrrURL)
		p.HarbrrURL, changed = &h, true
	}
	if !changed {
		return app, nil
	}
	if err := s.UpdateCredential(ctx, app.ID, p); err != nil {
		return domain.App{}, err
	}
	return s.Get(ctx, app.ID)
}

// Get returns one app by id (the surface services and the handler both read through
// it; the credential stays encrypted, the handler redacts).
func (s *Service) Get(ctx context.Context, id int64) (domain.App, error) {
	app, err := s.repo.GetApp(ctx, s.db, id)
	if err != nil {
		return domain.App{}, fmt.Errorf("apps: get: %w", err)
	}
	return app, nil
}

// List returns all apps ordered by id.
func (s *Service) List(ctx context.Context) ([]domain.App, error) {
	list, err := s.repo.ListApps(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("apps: list: %w", err)
	}
	return list, nil
}

// Index returns all apps keyed by id — the surface List paths enrich display fields
// (base URL, harbrr URL, username) from one lookup rather than N per-row Gets.
func (s *Service) Index(ctx context.Context) (map[int64]domain.App, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]domain.App, len(list))
	for _, a := range list {
		m[a.ID] = a
	}
	return m, nil
}

// References returns the surface reference counts for an app (for the delete 409 and
// the "used by N surfaces" list view).
func (s *Service) References(ctx context.Context, id int64) (database.AppReferences, error) {
	refs, err := s.repo.CountAppReferences(ctx, s.db, id)
	if err != nil {
		return database.AppReferences{}, fmt.Errorf("apps: count references: %w", err)
	}
	return refs, nil
}

// DecryptKey returns an app's plaintext credential (API key or password), decrypted
// under the app's own id as AAD.
func (s *Service) DecryptKey(app domain.App) (string, error) {
	key, err := s.keyring.Decrypt(app.ID, domain.AppSecret, app.APIKeyEncrypted)
	if err != nil {
		return "", fmt.Errorf("apps: decrypt credential: %w", err)
	}
	return key, nil
}

// UpdateParams patches an app; nil fields are left unchanged. APIKey, when a non-nil
// non-blank value, rotates the credential (re-encrypted in place) — every referencing
// surface decrypts through the app on its next call, so a rotation propagates.
type UpdateParams struct {
	Name      *string
	BaseURL   *string
	Username  *string
	HarbrrURL *string
	Enabled   *bool
	APIKey    *string
}

// UpdateCredential applies a patch, re-encrypting the credential when rotated. The
// read and full-row write run in one transaction so two overlapping updates can't lose
// each other's write (the connresource.Update precedent).
func (s *Service) UpdateCredential(ctx context.Context, id int64, p UpdateParams) error {
	return s.life.Update(ctx, id, connresource.UpdateSpec[domain.App]{
		Get: func(ctx context.Context, q dbinterface.Execer, id int64) (domain.App, error) {
			return s.repo.GetApp(ctx, q, id)
		},
		Patch: func(a *domain.App) error { return applyAppPatch(a, p) },
		Rotate: func(_ *domain.App) (connresource.Secret, bool, error) {
			if p.APIKey == nil {
				return connresource.Secret{}, false, nil
			}
			key := strings.TrimSpace(*p.APIKey)
			if key == "" {
				return connresource.Secret{}, false, fmt.Errorf("%w: credential must not be blank", domain.ErrInvalid)
			}
			// The read view redacts the credential to the sentinel; a client echoing it
			// back means "keep the stored one" and must OMIT the field. Storing the
			// sentinel literally would silently replace the real credential.
			if secrets.IsRedacted(key) {
				return connresource.Secret{}, false, fmt.Errorf("%w: credential must not be the redacted placeholder (omit it to keep the stored one)", domain.ErrInvalid)
			}
			return connresource.Secret{Discriminator: domain.AppSecret, Plaintext: key}, true, nil
		},
		Apply: func(a *domain.App, encrypted, keyID string) { a.APIKeyEncrypted, a.KeyID = encrypted, keyID },
		Touch: func(a *domain.App, now time.Time) { a.UpdatedAt = now },
		Write: func(ctx context.Context, q dbinterface.Execer, a domain.App) error {
			return s.repo.UpdateApp(ctx, q, a)
		},
		Conflict: func(a domain.App) error {
			return fmt.Errorf("%w: %s at %s", domain.ErrConflict, a.Kind, apphttp.RedactURL(a.BaseURL))
		},
	})
}

// applyAppPatch overlays the non-nil, non-credential patch fields onto the app,
// validating each (a present-but-blank required field is rejected, not silently
// stored).
func applyAppPatch(a *domain.App, p UpdateParams) error {
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if name == "" {
			return fmt.Errorf("%w: name must not be blank", domain.ErrInvalid)
		}
		a.Name = name
	}
	if p.BaseURL != nil {
		base, err := domain.ValidateAbsURL("base url", *p.BaseURL)
		if err != nil {
			return err
		}
		a.BaseURL = base
	}
	if p.Username != nil {
		a.Username = strings.TrimSpace(*p.Username)
	}
	if p.HarbrrURL != nil {
		harbrr, err := domain.ValidateAbsURL("harbrr url", *p.HarbrrURL)
		if err != nil {
			return err
		}
		a.HarbrrURL = harbrr
	}
	if p.Enabled != nil {
		a.Enabled = *p.Enabled
	}
	return nil
}

// Delete removes an app, blocking with a 409 (domain.ErrConflict) naming the
// referencing surfaces when it is still in use — a config convenience must never
// silently delete a working download client (ADR §6). Deleting an unreferenced or
// missing app flows through to the repo (ErrNotFound → 404).
func (s *Service) Delete(ctx context.Context, id int64) error {
	refs, err := s.References(ctx, id)
	if err != nil {
		return err
	}
	if refs.Any() {
		return fmt.Errorf("%w: app is in use by %s", domain.ErrConflict, describeRefs(refs))
	}
	if err := s.repo.DeleteApp(ctx, s.db, id); err != nil {
		return fmt.Errorf("apps: delete: %w", err)
	}
	return nil
}

// describeRefs names the referencing surfaces for the delete-blocked 409.
func describeRefs(r database.AppReferences) string {
	var parts []string
	for _, p := range []struct {
		n     int
		label string
	}{
		{r.AppConnections, "app-sync connection"},
		{r.Announce, "announce connection"},
		{r.Download, "download client"},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s(s)", p.n, p.label))
		}
	}
	return strings.Join(parts, ", ")
}
