package appsync

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
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/secrets"
)

// httpClientTimeout bounds a single app call so an unresponsive Sonarr/Radarr/qui
// cannot hang the sync worker.
const httpClientTimeout = 30 * time.Second

// defaultHTTPClient is the fallback client the drivers use when none is injected.
func defaultHTTPClient() *http.Client { return &http.Client{Timeout: httpClientTimeout} }

// AAD discriminators distinguishing a connection's two encrypted secrets (the app's
// own key vs the harbrr key minted for it), plus service defaults.
const (
	secretApp       = "app"
	secretHarbrr    = "harbrr"
	defaultPriority = 25
	// StatusSkipped is the sync status for a disabled connection (no remote calls).
	StatusSkipped = "skipped"
)

// IndexerSource is the slice of the registry app-sync needs: the configured indexers,
// each one's Newznab categories, and its Torznab capability tokens. Implemented by a
// registry adapter (serve.go).
type IndexerSource interface {
	List(ctx context.Context) ([]domain.IndexerInstance, error)
	Categories(ctx context.Context, slug string) ([]Category, error)
	// Capabilities returns the flat Torznab capability tokens (tv-search,
	// movie-search-imdbid, ...) the indexer advertises, for targets (qui) that store
	// caps per indexer instead of fetching them from the feed.
	Capabilities(ctx context.Context, slug string) ([]string, error)
}

// Service orchestrates app-sync connections: it persists them (encrypting both the
// app's key and the harbrr key minted for the connection), and reconciles harbrr's
// indexers into each app on demand. Create/Update/Delete of the connection row and
// its encrypted secrets are sequenced by connresource.Lifecycle; this service
// supplies the connection-specific data and repo calls.
type Service struct {
	db       dbinterface.Querier
	repo     database.AppConnections
	profiles database.SyncProfiles
	source   IndexerSource
	minter   connresource.KeyMinter
	keyring  *secrets.Keyring
	client   *http.Client
	clock    func() time.Time
	life     *connresource.Lifecycle[domain.AppConnection]
	log      zerolog.Logger
}

// NewService wires the app-sync service. client is shared by all drivers; clock is
// injectable for deterministic tests (assigning to the returned Service's clock
// field also retunes its Lifecycle, which reads clock through an indirection).
func NewService(db dbinterface.Querier, source IndexerSource, minter connresource.KeyMinter, keyring *secrets.Keyring, client *http.Client, log zerolog.Logger) *Service {
	if client == nil {
		client = defaultHTTPClient()
	}
	s := &Service{
		db: db, source: source, minter: minter, keyring: keyring,
		client: client, clock: time.Now, log: log,
	}
	s.life = connresource.New[domain.AppConnection](db, keyring, func() time.Time { return s.clock() })
	return s
}

// CreateConnectionParams is the input to CreateConnection. APIKey is the app's own
// API key (so harbrr can call it); HarbrrURL is the base URL this app uses to reach
// harbrr's feed. SyncLevel/IndexScope/Priority default when empty.
type CreateConnectionParams struct {
	Name          string
	Kind          string
	BaseURL       string
	APIKey        string
	HarbrrURL     string
	SyncLevel     string
	IndexScope    string
	FreeleechMode string
	Priority      int
	// SyncProfileID references a sync profile, or nil for none. Validated by
	// validateProfileRef (must exist, kind != qui, category overlap).
	SyncProfileID *int64
}

// CreateConnection mints a dedicated harbrr key for the connection, then persists the
// connection with both secrets encrypted. If persistence fails, the orphaned key is
// revoked so a failed create leaves nothing behind.
func (s *Service) CreateConnection(ctx context.Context, p CreateConnectionParams) (domain.AppConnection, error) {
	p = p.withDefaults()
	if err := validateCreate(&p); err != nil {
		return domain.AppConnection{}, err
	}
	// Advisory pre-check so an ordinary invalid profile ref fails before the key
	// mint has side effects; the authoritative, race-proof check runs again inside
	// Lifecycle.Create's transaction (the Hook below).
	if err := s.validateProfileRef(ctx, s.db, p.Kind, p.SyncProfileID); err != nil {
		return domain.AppConnection{}, err
	}
	return s.life.Create(ctx, connresource.CreateSpec[domain.AppConnection]{
		Minter:   s.minter,
		MintName: "app-sync: " + p.Name,
		Build: func(now time.Time, mintedKeyID int64) domain.AppConnection {
			return domain.AppConnection{
				Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL, HarbrrURL: p.HarbrrURL,
				HarbrrAPIKeyID: mintedKeyID, Enabled: true, SyncLevel: p.SyncLevel,
				IndexScope: p.IndexScope, FreeleechMode: p.FreeleechMode, Priority: p.Priority,
				SyncProfileID: p.SyncProfileID, CreatedAt: now, UpdatedAt: now,
			}
		},
		Hook: func(ctx context.Context, q dbinterface.Execer, conn *domain.AppConnection) error {
			// Re-validated against this same transaction (not the bare s.db handle
			// used by the advisory pre-check above), so a concurrent profile delete
			// or category-narrow can't slip between the check and the insert below.
			return s.validateProfileRef(ctx, q, conn.Kind, conn.SyncProfileID)
		},
		Insert: func(ctx context.Context, q dbinterface.Execer, conn domain.AppConnection) (int64, error) {
			return s.repo.InsertConnection(ctx, q, conn)
		},
		Secrets: func(_ domain.AppConnection, mintedPlain string) []connresource.Secret {
			return []connresource.Secret{
				{Discriminator: secretApp, Plaintext: p.APIKey},
				{Discriminator: secretHarbrr, Plaintext: mintedPlain},
			}
		},
		SetSecrets: func(ctx context.Context, q dbinterface.Execer, id int64, encrypted []string, keyID string) error {
			return s.repo.SetConnectionSecrets(ctx, q, id, encrypted[0], encrypted[1], keyID)
		},
		Finalize: func(conn domain.AppConnection, id int64, encrypted []string, keyID string) domain.AppConnection {
			conn.ID, conn.APIKeyEncrypted, conn.HarbrrAPIKeyEncrypted, conn.KeyID = id, encrypted[0], encrypted[1], keyID
			return conn
		},
		Conflict: func(conn domain.AppConnection) error {
			return fmt.Errorf("%w: %s at %s", domain.ErrConflict, conn.Kind, apphttp.RedactURL(conn.BaseURL))
		},
	})
}

// RefUpdate is a tri-state PATCH field for a nullable resource reference: Present false
// leaves the stored reference unchanged; Present true with a nil Value clears it; Present
// true with a value sets it. It mirrors registry.RefUpdate (the same tri-state the
// indexer PATCH uses for proxy/solver), redeclared here so appsync does not import
// registry — the web layer maps its optionalRef into this.
type RefUpdate struct {
	Present bool
	Value   *int64
}

// UpdateConnectionParams patches a connection; nil fields are left unchanged. APIKey,
// when set, rotates the app's key (re-encrypted in place). SyncProfileID is tri-state
// (RefUpdate): only an explicitly-present field changes the reference.
type UpdateConnectionParams struct {
	Name          *string
	BaseURL       *string
	HarbrrURL     *string
	APIKey        *string
	SyncLevel     *string
	IndexScope    *string
	FreeleechMode *string
	Priority      *int
	SyncProfileID RefUpdate
}

// UpdateConnection applies a patch, re-encrypting the app key when rotated. The
// read, profile-ref-validate, and write run in one Lifecycle.Update transaction,
// so a concurrent mutation can't slip between the check and the write (the
// UpdateProfile / proxy Update precedent). Two guarantees ride on this: a
// concurrent key rotation can't be lost by this full-row write reading a stale
// api_key, and a concurrent UpdateProfile can't narrow the referenced profile's
// categories between validateProfileRef and the ref write — which would leave a
// full-sync connection pointing at an empty gate that deletes every indexer it
// manages.
func (s *Service) UpdateConnection(ctx context.Context, id int64, p UpdateConnectionParams) error {
	return s.life.Update(ctx, id, connresource.UpdateSpec[domain.AppConnection]{
		Get: func(ctx context.Context, q dbinterface.Execer, id int64) (domain.AppConnection, error) {
			return s.repo.GetConnection(ctx, q, id)
		},
		Hook: func(ctx context.Context, q dbinterface.Execer, conn *domain.AppConnection) error {
			// A new profile ref is validated against the connection's kind before it
			// is applied (existence, non-qui, category overlap), so a bad ref is a
			// 400, not a stored orphan.
			if !p.SyncProfileID.Present {
				return nil
			}
			return s.validateProfileRef(ctx, q, conn.Kind, p.SyncProfileID.Value)
		},
		Patch: func(conn *domain.AppConnection) error {
			return applyUpdate(conn, p)
		},
		Rotate: func(_ *domain.AppConnection) (connresource.Secret, bool, error) {
			if p.APIKey == nil {
				return connresource.Secret{}, false, nil
			}
			if strings.TrimSpace(*p.APIKey) == "" {
				return connresource.Secret{}, false, fmt.Errorf("%w: api key must not be blank", domain.ErrInvalid)
			}
			return connresource.Secret{Discriminator: secretApp, Plaintext: *p.APIKey}, true, nil
		},
		Apply: func(conn *domain.AppConnection, encrypted, keyID string) {
			conn.APIKeyEncrypted, conn.KeyID = encrypted, keyID
		},
		Touch: func(conn *domain.AppConnection, now time.Time) { conn.UpdatedAt = now },
		Write: func(ctx context.Context, q dbinterface.Execer, conn domain.AppConnection) error {
			return s.repo.UpdateConnection(ctx, q, conn)
		},
		Conflict: func(conn domain.AppConnection) error {
			return fmt.Errorf("%w: %s at %s", domain.ErrConflict, conn.Kind, apphttp.RedactURL(conn.BaseURL))
		},
	})
}

// SetSelectedIndexers replaces a connection's selected-indexer set (the scope
// "selected" subset): the given instances become selected, every other currently
// selected one is cleared. Applied in one transaction.
func (s *Service) SetSelectedIndexers(ctx context.Context, id int64, instanceIDs []int64) error {
	if err := s.validateInstanceIDs(ctx, instanceIDs); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("appsync: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Read the connection inside the writing transaction (the UpdateConnection
	// precedent), so a concurrent delete can't slip between the existence check and
	// the selection writes and surface as an FK fault instead of a clean not-found.
	// The instance-ids check above stays advisory — the indexer source isn't
	// tx-scoped — with the selection FKs as the authoritative guard.
	if _, err := s.repo.GetConnection(ctx, tx, id); err != nil {
		return fmt.Errorf("appsync: get connection: %w", err)
	}

	want := make(map[int64]bool, len(instanceIDs))
	for _, instID := range instanceIDs {
		want[instID] = true
		if err := s.repo.SetIndexerSelection(ctx, tx, id, instID, true); err != nil {
			return fmt.Errorf("appsync: select indexer: %w", err)
		}
	}
	ledger, err := s.repo.ListConnectionIndexers(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("appsync: list ledger: %w", err)
	}
	for _, l := range ledger {
		if l.Selected && !want[l.InstanceID] {
			if err := s.repo.SetIndexerSelection(ctx, tx, id, l.InstanceID, false); err != nil {
				return fmt.Errorf("appsync: deselect indexer: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("appsync: commit selection: %w", err)
	}
	return nil
}

// validateInstanceIDs rejects a selection that names an indexer that does not exist,
// turning a client mistake into a 400 rather than a repository FK error.
func (s *Service) validateInstanceIDs(ctx context.Context, instanceIDs []int64) error {
	if len(instanceIDs) == 0 {
		return nil
	}
	instances, err := s.source.List(ctx)
	if err != nil {
		return fmt.Errorf("appsync: list indexers: %w", err)
	}
	known := make(map[int64]bool, len(instances))
	for _, inst := range instances {
		known[inst.ID] = true
	}
	for _, instID := range instanceIDs {
		if !known[instID] {
			return fmt.Errorf("%w: unknown indexer instance id %d", domain.ErrInvalid, instID)
		}
	}
	return nil
}

// DeleteConnection removes the connection (ledger cascades) and revokes its minted key.
func (s *Service) DeleteConnection(ctx context.Context, id int64) error {
	return s.life.Delete(ctx, id, connresource.DeleteSpec[domain.AppConnection]{
		Get: func(ctx context.Context, q dbinterface.Execer, id int64) (domain.AppConnection, error) {
			return s.repo.GetConnection(ctx, q, id)
		},
		Delete: func(ctx context.Context, q dbinterface.Execer, id int64) error {
			return s.repo.DeleteConnection(ctx, q, id)
		},
		Minter:      s.minter,
		MintedKeyID: func(conn domain.AppConnection) int64 { return conn.HarbrrAPIKeyID },
		// Fail closed (parity with internal/announce): the row is gone, but a
		// still-valid minted key would keep authorizing the feed, so surface a
		// revoke failure instead of swallowing it.
		RevokeFailMsg: func(_ domain.AppConnection, keyID int64, revokeErr error) error {
			return fmt.Errorf("appsync: connection deleted but its harbrr key (%d) could not be revoked — revoke it manually: %w",
				keyID, revokeErr)
		},
	})
}

// SetEnabled toggles a connection's enabled flag.
func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	if err := s.repo.SetConnectionEnabled(ctx, s.db, id, enabled, s.clock()); err != nil {
		return fmt.Errorf("appsync: set enabled: %w", err)
	}
	return nil
}

// ListConnections / GetConnection / ConnectionIndexers expose the persisted state for
// the API layer (secrets stay encrypted; the handler redacts).
func (s *Service) ListConnections(ctx context.Context) ([]domain.AppConnection, error) {
	list, err := s.repo.ListConnections(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("appsync: list connections: %w", err)
	}
	return list, nil
}

func (s *Service) GetConnection(ctx context.Context, id int64) (domain.AppConnection, error) {
	conn, err := s.repo.GetConnection(ctx, s.db, id)
	if err != nil {
		return domain.AppConnection{}, fmt.Errorf("appsync: get connection: %w", err)
	}
	return conn, nil
}

func (s *Service) ConnectionIndexers(ctx context.Context, id int64) ([]domain.AppConnectionIndexer, error) {
	ledger, err := s.repo.ListConnectionIndexers(ctx, s.db, id)
	if err != nil {
		return nil, fmt.Errorf("appsync: list connection indexers: %w", err)
	}
	return ledger, nil
}

// TestConnection probes the app's reachability and credentials by listing its
// indexers. The returned error is already scrubbed by the driver.
func (s *Service) TestConnection(ctx context.Context, id int64) error {
	conn, err := s.repo.GetConnection(ctx, s.db, id)
	if err != nil {
		return fmt.Errorf("appsync: get connection: %w", err)
	}
	driver, _, err := s.driver(conn)
	if err != nil {
		return err
	}
	if _, err := driver.List(ctx); err != nil {
		return fmt.Errorf("appsync: test connection: %w", err)
	}
	return nil
}

// driver decrypts a connection's keys and builds its Target, returning the harbrr feed
// key separately (it is pushed into each indexer body, not used to call the app).
func (s *Service) driver(conn domain.AppConnection) (Target, string, error) {
	appKey, err := s.keyring.Decrypt(conn.ID, secretApp, conn.APIKeyEncrypted)
	if err != nil {
		return nil, "", fmt.Errorf("appsync: decrypt app key: %w", err)
	}
	harbrrKey, err := s.keyring.Decrypt(conn.ID, secretHarbrr, conn.HarbrrAPIKeyEncrypted)
	if err != nil {
		return nil, "", fmt.Errorf("appsync: decrypt harbrr key: %w", err)
	}
	t, err := newDriver(conn.Kind, conn.BaseURL, appKey, s.client)
	return t, harbrrKey, err
}

// newDriver builds the per-kind Target.
func newDriver(kind, baseURL, apiKey string, client *http.Client) (Target, error) {
	switch kind {
	case domain.AppKindSonarr:
		return NewSonarr(baseURL, apiKey, client), nil
	case domain.AppKindRadarr:
		return NewRadarr(baseURL, apiKey, client), nil
	case domain.AppKindLidarr:
		return NewLidarr(baseURL, apiKey, client), nil
	case domain.AppKindReadarr:
		return NewReadarr(baseURL, apiKey, client), nil
	case domain.AppKindWhisparr:
		return NewWhisparr(baseURL, apiKey, client), nil
	case domain.AppKindQui:
		return NewQui(baseURL, apiKey, client), nil
	default:
		return nil, fmt.Errorf("%w: unknown kind %q", domain.ErrInvalid, kind)
	}
}
