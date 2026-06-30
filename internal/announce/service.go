package announce

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/secrets"
)

// AAD discriminators for a connection's two encrypted secrets (the tool's own key vs the
// minted harbrr key), mirroring appsync.
const (
	secretApp    = "app"
	secretHarbrr = "harbrr"
)

// ErrInvalid / ErrConflict are the service's input-mapping sentinels (the handler turns
// them into 400 / 409). Not-found flows through database.ErrNotFound.
var (
	ErrInvalid  = errors.New("announce: invalid input")
	ErrConflict = errors.New("announce: connection already exists")
)

// KeyMinter mints/revokes the dedicated harbrr key whose plaintext signs the /dl link the
// cross-seed tool fetches back.
type KeyMinter interface {
	MintAPIKey(ctx context.Context, name string) (string, domain.APIKey, error)
	RevokeAPIKey(ctx context.Context, id int64) error
}

// TargetFactory builds the per-kind announce driver for a connection, given the decrypted
// tool API key. It is injected so Push is testable with a fake driver and so the live wiring
// (the qui torrent fetcher) lives in cmd/harbrr, not here.
type TargetFactory func(conn domain.AnnounceConnection, toolKey string) (Target, error)

// Service persists cross-seed announce connections (encrypting both secrets) and pushes
// newly-seen releases to the enabled ones.
type Service struct {
	db      dbinterface.Querier
	repo    database.AnnounceConnections
	minter  KeyMinter
	keyring *secrets.Keyring
	factory TargetFactory
	clock   func() time.Time
	log     zerolog.Logger
}

// NewService wires the announce service. factory builds the per-kind driver (see
// DefaultTargetFactory for the production wiring).
func NewService(db dbinterface.Querier, minter KeyMinter, keyring *secrets.Keyring, factory TargetFactory, log zerolog.Logger) *Service {
	return &Service{
		db: db, minter: minter, keyring: keyring, factory: factory,
		clock: time.Now, log: log,
	}
}

// CreateConnectionParams is the input to CreateConnection. APIKey is the tool's own API
// key; HarbrrURL is the base URL the tool uses to reach harbrr's /dl link.
type CreateConnectionParams struct {
	Name      string
	Kind      string
	BaseURL   string
	APIKey    string
	HarbrrURL string
}

// CreateConnection mints a dedicated harbrr key, then persists the connection with both
// secrets encrypted. A failed persist revokes the orphan key.
func (s *Service) CreateConnection(ctx context.Context, p CreateConnectionParams) (domain.AnnounceConnection, error) {
	if err := validateCreate(p); err != nil {
		return domain.AnnounceConnection{}, err
	}
	plaintext, key, err := s.minter.MintAPIKey(ctx, "announce: "+p.Name)
	if err != nil {
		return domain.AnnounceConnection{}, fmt.Errorf("announce: mint connection key: %w", err)
	}
	conn, err := s.insertConnection(ctx, p, key.ID, plaintext)
	if err != nil {
		// Fail closed: if the orphan key can't be revoked it remains a valid feed
		// credential, so surface that alongside the create failure rather than hiding it.
		if revErr := s.minter.RevokeAPIKey(ctx, key.ID); revErr != nil {
			return domain.AnnounceConnection{}, fmt.Errorf("%w (and its orphan key %d could not be revoked — revoke it manually: %w)",
				err, key.ID, revErr)
		}
		return domain.AnnounceConnection{}, err
	}
	return conn, nil
}

// insertConnection writes the row then its encrypted secrets in one transaction (the row
// first, so its id can bind each secret's AAD).
func (s *Service) insertConnection(ctx context.Context, p CreateConnectionParams, harbrrKeyID int64, harbrrKeyPlain string) (domain.AnnounceConnection, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AnnounceConnection{}, fmt.Errorf("announce: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.clock()
	conn := domain.AnnounceConnection{
		Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL, HarbrrURL: p.HarbrrURL,
		HarbrrAPIKeyID: harbrrKeyID, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	id, err := s.repo.InsertAnnounceConnection(ctx, tx, conn)
	if err != nil {
		if database.IsUniqueViolation(err) {
			return domain.AnnounceConnection{}, fmt.Errorf("%w: %s at %s", ErrConflict, p.Kind, apphttp.RedactURL(p.BaseURL))
		}
		return domain.AnnounceConnection{}, fmt.Errorf("announce: insert connection: %w", err)
	}
	conn.ID = id

	appEnc, err := s.keyring.Encrypt(id, secretApp, p.APIKey)
	if err != nil {
		return domain.AnnounceConnection{}, fmt.Errorf("announce: encrypt tool key: %w", err)
	}
	harbrrEnc, err := s.keyring.Encrypt(id, secretHarbrr, harbrrKeyPlain)
	if err != nil {
		return domain.AnnounceConnection{}, fmt.Errorf("announce: encrypt harbrr key: %w", err)
	}
	conn.APIKeyEncrypted, conn.HarbrrAPIKeyEncrypted, conn.KeyID = appEnc, harbrrEnc, s.keyring.KeyID()
	// The row was inserted with empty secret columns (so its id could bind the AAD); now
	// write the sealed secrets back.
	if err := s.setSecrets(ctx, tx, conn); err != nil {
		return domain.AnnounceConnection{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.AnnounceConnection{}, fmt.Errorf("announce: commit: %w", err)
	}
	return conn, nil
}

// setSecrets writes both encrypted secret columns + key_id for a connection.
func (s *Service) setSecrets(ctx context.Context, q dbinterface.Execer, c domain.AnnounceConnection) error {
	_, err := q.ExecContext(ctx, q.Rebind(
		`UPDATE announce_connections SET api_key_encrypted = ?, harbrr_api_key_encrypted = ?, key_id = ? WHERE id = ?`),
		c.APIKeyEncrypted, c.HarbrrAPIKeyEncrypted, c.KeyID, c.ID)
	if err != nil {
		return fmt.Errorf("announce: set secrets: %w", err)
	}
	return nil
}

// ListConnections / GetConnection expose persisted state (secrets stay encrypted).
func (s *Service) ListConnections(ctx context.Context) ([]domain.AnnounceConnection, error) {
	list, err := s.repo.ListAnnounceConnections(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("announce: list connections: %w", err)
	}
	return list, nil
}

func (s *Service) GetConnection(ctx context.Context, id int64) (domain.AnnounceConnection, error) {
	conn, err := s.repo.GetAnnounceConnection(ctx, s.db, id)
	if err != nil {
		return domain.AnnounceConnection{}, fmt.Errorf("announce: get connection: %w", err)
	}
	return conn, nil
}

// SetEnabled toggles a connection.
func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	if err := s.repo.SetAnnounceConnectionEnabled(ctx, s.db, id, enabled, s.clock()); err != nil {
		return fmt.Errorf("announce: set enabled: %w", err)
	}
	return nil
}

// DeleteConnection removes a connection and revokes its minted key.
func (s *Service) DeleteConnection(ctx context.Context, id int64) error {
	conn, err := s.repo.GetAnnounceConnection(ctx, s.db, id)
	if err != nil {
		return fmt.Errorf("announce: get connection: %w", err)
	}
	if err := s.repo.DeleteAnnounceConnection(ctx, s.db, id); err != nil {
		return fmt.Errorf("announce: delete connection: %w", err)
	}
	if conn.HarbrrAPIKeyID != 0 {
		// Fail closed: the row is gone, but a still-valid minted key would keep signing /dl
		// links and authorizing the feed, so surface a revoke failure instead of swallowing it.
		if err := s.minter.RevokeAPIKey(ctx, conn.HarbrrAPIKeyID); err != nil {
			return fmt.Errorf("announce: connection deleted but its harbrr key (%d) could not be revoked — revoke it manually: %w",
				conn.HarbrrAPIKeyID, err)
		}
	}
	return nil
}

// Push fans the releases out to every enabled connection's driver, best-effort: a per-
// connection or per-release failure is logged (scrubbed) and never blocks the rest. It
// returns the number of confirmed cross-seed matches. Build is injected, so the caller
// supplies the per-connection announce.Release (with its DownloadURL already formed).
func (s *Service) Push(ctx context.Context, build func(conn domain.AnnounceConnection) []Release) int {
	conns, err := s.repo.ListAnnounceConnections(ctx, s.db)
	if err != nil {
		s.log.Warn().Str("error", apphttp.RedactError(err)).Msg("announce: list connections for push failed")
		return 0
	}
	matched := 0
	for _, conn := range conns {
		if !conn.Enabled {
			continue
		}
		matched += s.pushOne(ctx, conn, build(conn))
	}
	return matched
}

// pushOne builds the connection's driver and announces each release, returning the match
// count. All failures are logged (scrubbed), never propagated.
func (s *Service) pushOne(ctx context.Context, conn domain.AnnounceConnection, rels []Release) int {
	if len(rels) == 0 {
		return 0
	}
	toolKey, err := s.keyring.Decrypt(conn.ID, secretApp, conn.APIKeyEncrypted)
	if err != nil {
		s.log.Warn().Int64("connection_id", conn.ID).Msg("announce: decrypt tool key failed")
		return 0
	}
	target, err := s.factory(conn, toolKey)
	if err != nil {
		s.log.Warn().Int64("connection_id", conn.ID).Str("error", apphttp.RedactError(err)).Msg("announce: build target failed")
		return 0
	}
	matched := 0
	for _, rel := range rels {
		res, err := target.Announce(ctx, rel)
		if err != nil {
			s.log.Warn().Int64("connection_id", conn.ID).Str("guid", rel.GUID).
				Str("error", apphttp.RedactError(err)).Msg("announce: push failed")
			continue
		}
		if res.Matched {
			matched++
		}
	}
	return matched
}

// HarbrrKey decrypts the minted harbrr key for a connection (the value that signs the /dl
// link the tool fetches). Used by the source wiring to build a connection's Release links.
// A connection whose key was revoked out of band (FK SET NULL → HarbrrAPIKeyID 0) is
// refused: pushing a /dl link signed with a dead key would just hand the tool a credential
// harbrr no longer recognizes (mirrors appsync's revoked-key guard).
func (s *Service) HarbrrKey(conn domain.AnnounceConnection) (string, error) {
	if conn.HarbrrAPIKeyID == 0 {
		return "", fmt.Errorf("%w: harbrr key revoked; recreate the connection to re-mint it", ErrInvalid)
	}
	key, err := s.keyring.Decrypt(conn.ID, secretHarbrr, conn.HarbrrAPIKeyEncrypted)
	if err != nil {
		return "", fmt.Errorf("announce: decrypt harbrr key: %w", err)
	}
	return key, nil
}

func validateCreate(p CreateConnectionParams) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if err := validateKind(p.Kind); err != nil {
		return err
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return fmt.Errorf("%w: base url is required", ErrInvalid)
	}
	if strings.TrimSpace(p.APIKey) == "" {
		return fmt.Errorf("%w: api key is required", ErrInvalid)
	}
	if err := validateAbsURL("base url", p.BaseURL); err != nil {
		return err
	}
	// Both kinds need an absolute harbrr URL to form a fetchable /dl link: cross-seed v6
	// fetches it itself, and qui fetches it server-side (HTTPTorrentFetcher). Without it the
	// /dl URL would be host-less and every non-magnet release would silently fail to push.
	if strings.TrimSpace(p.HarbrrURL) == "" {
		return fmt.Errorf("%w: harbrr url is required (the tool fetches harbrr's /dl link)", ErrInvalid)
	}
	return validateAbsURL("harbrr url", p.HarbrrURL)
}

// validateAbsURL requires an absolute http(s) URL with a host, so a malformed/relative URL
// can't be persisted and later yield a host-less /dl link or a failing tool call.
func validateAbsURL(field, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%w: %s must be an absolute http(s) URL", ErrInvalid, field)
	}
	return nil
}

func validateKind(kind string) error {
	switch kind {
	case domain.AnnounceKindQui, domain.AnnounceKindCrossSeedV6:
		return nil
	default:
		return fmt.Errorf("%w: kind must be qui or crossseed-v6 (got %q)", ErrInvalid, kind)
	}
}
