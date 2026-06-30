package appsync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

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

// ErrInvalid and ErrConflict are the service's input-mapping sentinels (the handler
// turns them into 400 / 409). Not-found flows through database.ErrNotFound.
var (
	ErrInvalid  = errors.New("appsync: invalid input")
	ErrConflict = errors.New("appsync: connection already exists")
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

// KeyMinter is the slice of auth.Service app-sync needs: mint a dedicated harbrr API
// key per connection and revoke it on delete.
type KeyMinter interface {
	MintAPIKey(ctx context.Context, name string) (string, domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, id int64) error
}

// Service orchestrates app-sync connections: it persists them (encrypting both the
// app's key and the harbrr key minted for the connection), and reconciles harbrr's
// indexers into each app on demand.
type Service struct {
	db      dbinterface.Querier
	repo    database.AppConnections
	source  IndexerSource
	minter  KeyMinter
	keyring *secrets.Keyring
	client  *http.Client
	clock   func() time.Time
	log     zerolog.Logger
}

// NewService wires the app-sync service. client is shared by all drivers; clock is
// injectable for deterministic tests.
func NewService(db dbinterface.Querier, source IndexerSource, minter KeyMinter, keyring *secrets.Keyring, client *http.Client, log zerolog.Logger) *Service {
	if client == nil {
		client = defaultHTTPClient()
	}
	return &Service{
		db: db, source: source, minter: minter, keyring: keyring,
		client: client, clock: time.Now, log: log,
	}
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
}

// CreateConnection mints a dedicated harbrr key for the connection, then persists the
// connection with both secrets encrypted. If persistence fails, the orphaned key is
// revoked so a failed create leaves nothing behind.
func (s *Service) CreateConnection(ctx context.Context, p CreateConnectionParams) (domain.AppConnection, error) {
	p = p.withDefaults()
	if err := validateCreate(p); err != nil {
		return domain.AppConnection{}, err
	}

	plaintext, key, err := s.minter.MintAPIKey(ctx, "app-sync: "+p.Name)
	if err != nil {
		return domain.AppConnection{}, fmt.Errorf("appsync: mint connection key: %w", err)
	}
	conn, err := s.insertConnection(ctx, p, key.ID, plaintext)
	if err != nil {
		// Fail closed (parity with internal/announce): an orphan key that cannot be
		// revoked remains a valid feed credential, so surface the revoke failure
		// alongside the create failure rather than swallowing it in a log line.
		if revErr := s.minter.RevokeAPIKey(ctx, key.ID); revErr != nil {
			return domain.AppConnection{}, fmt.Errorf("%w (and its orphan key %d could not be revoked — revoke it manually: %w)",
				err, key.ID, revErr)
		}
		return domain.AppConnection{}, err
	}
	return conn, nil
}

// insertConnection writes the connection in two phases inside one transaction: the row
// first (to mint the id the encryption AAD binds to), then its encrypted secrets.
func (s *Service) insertConnection(ctx context.Context, p CreateConnectionParams, harbrrKeyID int64, harbrrKeyPlain string) (domain.AppConnection, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AppConnection{}, fmt.Errorf("appsync: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.clock()
	conn := domain.AppConnection{
		Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL, HarbrrURL: p.HarbrrURL,
		HarbrrAPIKeyID: harbrrKeyID, Enabled: true, SyncLevel: p.SyncLevel,
		IndexScope: p.IndexScope, FreeleechMode: p.FreeleechMode, Priority: p.Priority,
		CreatedAt: now, UpdatedAt: now,
	}
	id, err := s.repo.InsertConnection(ctx, tx, conn)
	if err != nil {
		if database.IsUniqueViolation(err) {
			return domain.AppConnection{}, fmt.Errorf("%w: %s at %s", ErrConflict, p.Kind, apphttp.RedactURL(p.BaseURL))
		}
		return domain.AppConnection{}, fmt.Errorf("appsync: insert connection: %w", err)
	}
	conn.ID = id

	appEnc, harbrrEnc, err := s.encryptSecrets(id, p.APIKey, harbrrKeyPlain)
	if err != nil {
		return domain.AppConnection{}, err
	}
	if err := s.repo.SetConnectionSecrets(ctx, tx, id, appEnc, harbrrEnc, s.keyring.KeyID()); err != nil {
		return domain.AppConnection{}, fmt.Errorf("appsync: set connection secrets: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.AppConnection{}, fmt.Errorf("appsync: commit: %w", err)
	}
	conn.APIKeyEncrypted, conn.HarbrrAPIKeyEncrypted, conn.KeyID = appEnc, harbrrEnc, s.keyring.KeyID()
	return conn, nil
}

// encryptSecrets seals both connection secrets under the connection id.
func (s *Service) encryptSecrets(connID int64, appKey, harbrrKey string) (appEnc, harbrrEnc string, err error) {
	appEnc, err = s.keyring.Encrypt(connID, secretApp, appKey)
	if err != nil {
		return "", "", fmt.Errorf("appsync: encrypt app key: %w", err)
	}
	harbrrEnc, err = s.keyring.Encrypt(connID, secretHarbrr, harbrrKey)
	if err != nil {
		return "", "", fmt.Errorf("appsync: encrypt harbrr key: %w", err)
	}
	return appEnc, harbrrEnc, nil
}

// UpdateConnectionParams patches a connection; nil fields are left unchanged. APIKey,
// when set, rotates the app's key (re-encrypted in place).
type UpdateConnectionParams struct {
	Name          *string
	BaseURL       *string
	HarbrrURL     *string
	APIKey        *string
	SyncLevel     *string
	IndexScope    *string
	FreeleechMode *string
	Priority      *int
}

// UpdateConnection applies a patch, re-encrypting the app key when rotated.
func (s *Service) UpdateConnection(ctx context.Context, id int64, p UpdateConnectionParams) error {
	conn, err := s.repo.GetConnection(ctx, s.db, id)
	if err != nil {
		return fmt.Errorf("appsync: get connection: %w", err)
	}
	if err := applyUpdate(&conn, p); err != nil {
		return err
	}
	if p.APIKey != nil {
		if strings.TrimSpace(*p.APIKey) == "" {
			return fmt.Errorf("%w: api key must not be blank", ErrInvalid)
		}
		enc, err := s.keyring.Encrypt(conn.ID, secretApp, *p.APIKey)
		if err != nil {
			return fmt.Errorf("appsync: encrypt app key: %w", err)
		}
		conn.APIKeyEncrypted, conn.KeyID = enc, s.keyring.KeyID()
	}
	conn.UpdatedAt = s.clock()
	if err := s.repo.UpdateConnection(ctx, s.db, conn); err != nil {
		if database.IsUniqueViolation(err) {
			return fmt.Errorf("%w: %s at %s", ErrConflict, conn.Kind, apphttp.RedactURL(conn.BaseURL))
		}
		return fmt.Errorf("appsync: update connection: %w", err)
	}
	return nil
}

// SetSelectedIndexers replaces a connection's selected-indexer set (the scope
// "selected" subset): the given instances become selected, every other currently
// selected one is cleared. Applied in one transaction.
func (s *Service) SetSelectedIndexers(ctx context.Context, id int64, instanceIDs []int64) error {
	if _, err := s.repo.GetConnection(ctx, s.db, id); err != nil {
		return fmt.Errorf("appsync: get connection: %w", err)
	}
	if err := s.validateInstanceIDs(ctx, instanceIDs); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("appsync: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

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
			return fmt.Errorf("%w: unknown indexer instance id %d", ErrInvalid, instID)
		}
	}
	return nil
}

// DeleteConnection removes the connection (ledger cascades) and revokes its minted key.
func (s *Service) DeleteConnection(ctx context.Context, id int64) error {
	conn, err := s.repo.GetConnection(ctx, s.db, id)
	if err != nil {
		return fmt.Errorf("appsync: get connection: %w", err)
	}
	if err := s.repo.DeleteConnection(ctx, s.db, id); err != nil {
		return fmt.Errorf("appsync: delete connection: %w", err)
	}
	if conn.HarbrrAPIKeyID != 0 {
		// Fail closed (parity with internal/announce): the row is gone, but a
		// still-valid minted key would keep authorizing the feed, so surface a revoke
		// failure instead of swallowing it.
		if err := s.minter.RevokeAPIKey(ctx, conn.HarbrrAPIKeyID); err != nil {
			return fmt.Errorf("appsync: connection deleted but its harbrr key (%d) could not be revoked — revoke it manually: %w",
				conn.HarbrrAPIKeyID, err)
		}
	}
	return nil
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
		return nil, fmt.Errorf("%w: unknown kind %q", ErrInvalid, kind)
	}
}
