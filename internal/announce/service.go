package announce

import (
	"context"
	"fmt"
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

// AAD discriminators for a connection's two encrypted secrets (the tool's own key vs the
// minted harbrr key), mirroring appsync.
const (
	secretApp    = "app"
	secretHarbrr = "harbrr"
)

// TargetFactory builds the per-kind announce driver for a connection, given the decrypted
// tool API key. It is injected so Push is testable with a fake driver and so the live wiring
// (the qui torrent fetcher) lives in cmd/harbrr, not here.
type TargetFactory func(conn domain.AnnounceConnection, toolKey string) (Target, error)

// Service persists cross-seed announce connections (encrypting both secrets) and pushes
// newly-seen releases to the enabled ones. Create/Delete of the connection row and its
// encrypted secrets are sequenced by connresource.Lifecycle; announce has no Update (its
// HTTP clients and per-connection fields have nothing a PATCH would rotate beyond what
// CreateConnection already sets, unlike appsync/notify).
type Service struct {
	db      dbinterface.Querier
	repo    database.AnnounceConnections
	minter  connresource.KeyMinter
	keyring *secrets.Keyring
	factory TargetFactory
	clock   func() time.Time
	life    *connresource.Lifecycle[domain.AnnounceConnection]
	log     zerolog.Logger
}

// NewService wires the announce service. factory builds the per-kind driver (see
// DefaultTargetFactory for the production wiring).
func NewService(db dbinterface.Querier, minter connresource.KeyMinter, keyring *secrets.Keyring, factory TargetFactory, log zerolog.Logger) *Service {
	s := &Service{
		db: db, minter: minter, keyring: keyring, factory: factory,
		clock: time.Now, log: log,
	}
	s.life = connresource.New[domain.AnnounceConnection](db, keyring, func() time.Time { return s.clock() })
	return s
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
	// Trim before validating AND persisting, so a whitespace-padded URL can't bypass the
	// (kind, base_url) uniqueness contract or leave a trailing space in a posted/dl URL.
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	p.HarbrrURL = strings.TrimSpace(p.HarbrrURL)
	if err := validateCreate(p); err != nil {
		return domain.AnnounceConnection{}, err
	}
	return s.life.Create(ctx, connresource.CreateSpec[domain.AnnounceConnection]{
		Minter:   s.minter,
		MintName: "announce: " + p.Name,
		Build: func(now time.Time, mintedKeyID int64) domain.AnnounceConnection {
			return domain.AnnounceConnection{
				Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL, HarbrrURL: p.HarbrrURL,
				HarbrrAPIKeyID: mintedKeyID, Enabled: true, CreatedAt: now, UpdatedAt: now,
			}
		},
		Insert: func(ctx context.Context, q dbinterface.Execer, conn domain.AnnounceConnection) (int64, error) {
			return s.repo.InsertAnnounceConnection(ctx, q, conn)
		},
		Secrets: func(_ domain.AnnounceConnection, mintedPlain string) []connresource.Secret {
			return []connresource.Secret{
				{Discriminator: secretApp, Plaintext: p.APIKey},
				{Discriminator: secretHarbrr, Plaintext: mintedPlain},
			}
		},
		// The row is inserted with empty secret columns (so its id could bind the
		// AAD); this writes the sealed secrets back via the same raw-SQL helper
		// setSecrets uses elsewhere (announce has no repo method for it).
		SetSecrets: func(ctx context.Context, q dbinterface.Execer, id int64, encrypted []string, keyID string) error {
			return s.setSecrets(ctx, q, domain.AnnounceConnection{ID: id, APIKeyEncrypted: encrypted[0], HarbrrAPIKeyEncrypted: encrypted[1], KeyID: keyID})
		},
		Finalize: func(conn domain.AnnounceConnection, id int64, encrypted []string, keyID string) domain.AnnounceConnection {
			conn.ID, conn.APIKeyEncrypted, conn.HarbrrAPIKeyEncrypted, conn.KeyID = id, encrypted[0], encrypted[1], keyID
			return conn
		},
		Conflict: func(conn domain.AnnounceConnection) error {
			return fmt.Errorf("%w: %s at %s", domain.ErrConflict, conn.Kind, apphttp.RedactURL(conn.BaseURL))
		},
	})
}

// setSecrets writes both encrypted secret columns + key_id for a connection.
func (s *Service) setSecrets(ctx context.Context, q dbinterface.Execer, c domain.AnnounceConnection) error {
	_, err := q.ExecContext(ctx, q.Rebind(
		`UPDATE announce_connections SET api_key_encrypted = ?, harbrr_api_key_encrypted = ?, key_id = ? WHERE id = ?`,
	),
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
	return s.life.Delete(ctx, id, connresource.DeleteSpec[domain.AnnounceConnection]{
		Get: func(ctx context.Context, q dbinterface.Execer, id int64) (domain.AnnounceConnection, error) {
			return s.repo.GetAnnounceConnection(ctx, q, id)
		},
		Delete: func(ctx context.Context, q dbinterface.Execer, id int64) error {
			return s.repo.DeleteAnnounceConnection(ctx, q, id)
		},
		Minter:      s.minter,
		MintedKeyID: func(conn domain.AnnounceConnection) int64 { return conn.HarbrrAPIKeyID },
		// Fail closed: the row is gone, but a still-valid minted key would keep
		// signing /dl links and authorizing the feed, so surface a revoke failure
		// instead of swallowing it.
		RevokeFailMsg: func(_ domain.AnnounceConnection, keyID int64, revokeErr error) error {
			return fmt.Errorf("announce: connection deleted but its harbrr key (%d) could not be revoked — revoke it manually: %w",
				keyID, revokeErr)
		},
	})
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
		return "", fmt.Errorf("%w: harbrr key revoked; recreate the connection to re-mint it", domain.ErrInvalid)
	}
	key, err := s.keyring.Decrypt(conn.ID, secretHarbrr, conn.HarbrrAPIKeyEncrypted)
	if err != nil {
		return "", fmt.Errorf("announce: decrypt harbrr key: %w", err)
	}
	return key, nil
}

func validateCreate(p CreateConnectionParams) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name is required", domain.ErrInvalid)
	}
	if err := validateKind(p.Kind); err != nil {
		return err
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return fmt.Errorf("%w: base url is required", domain.ErrInvalid)
	}
	if strings.TrimSpace(p.APIKey) == "" {
		return fmt.Errorf("%w: api key is required", domain.ErrInvalid)
	}
	// The trimmed return is discarded: CreateConnection already trimmed p.BaseURL
	// before validateCreate ran, so the raw and normalized forms are identical here.
	if _, err := domain.ValidateAbsURL("base url", p.BaseURL); err != nil {
		return err
	}
	// Both kinds need an absolute harbrr URL to form a fetchable /dl link: cross-seed v6
	// fetches it itself, and qui fetches it server-side (HTTPTorrentFetcher). Without it the
	// /dl URL would be host-less and every non-magnet release would silently fail to push.
	if strings.TrimSpace(p.HarbrrURL) == "" {
		return fmt.Errorf("%w: harbrr url is required (the tool fetches harbrr's /dl link)", domain.ErrInvalid)
	}
	_, err := domain.ValidateAbsURL("harbrr url", p.HarbrrURL)
	return err
}

func validateKind(kind string) error {
	switch kind {
	case domain.AnnounceKindQui, domain.AnnounceKindCrossSeedV6:
		return nil
	default:
		return fmt.Errorf("%w: kind must be qui or crossseed-v6 (got %q)", domain.ErrInvalid, kind)
	}
}
