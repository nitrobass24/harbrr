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

// UpdateConnectionParams patches a connection; nil fields are left unchanged. APIKey,
// when set, rotates the tool's key (re-encrypted in place). Kind is immutable (a driver
// swap is a delete + recreate), matching appsync.
type UpdateConnectionParams struct {
	Name      *string
	BaseURL   *string
	HarbrrURL *string
	APIKey    *string
}

// UpdateConnection applies a patch, re-encrypting the tool key when rotated. Only the
// mutable fields move — the minted harbrr key and the enabled flag are untouched (they
// have their own paths). The read → write runs in one transaction so a concurrent key
// rotation can't be lost by this full-row write reading a stale api_key (the appsync
// UpdateConnection precedent).
func (s *Service) UpdateConnection(ctx context.Context, id int64, p UpdateConnectionParams) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("announce: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	conn, err := s.repo.GetAnnounceConnection(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("announce: get connection: %w", err)
	}
	if err := applyAnnounceUpdate(&conn, p); err != nil {
		return err
	}
	if p.APIKey != nil {
		if strings.TrimSpace(*p.APIKey) == "" {
			return fmt.Errorf("%w: api key must not be blank", domain.ErrInvalid)
		}
		// The read view redacts the tool key to the <redacted> sentinel; a client that
		// echoes it back means "keep the stored key" and must OMIT the field. Storing the
		// sentinel literally would silently replace the real key, so reject it explicitly.
		if secrets.IsRedacted(strings.TrimSpace(*p.APIKey)) {
			return fmt.Errorf("%w: api key must not be the redacted placeholder (omit it to keep the stored key)", domain.ErrInvalid)
		}
		enc, err := s.keyring.Encrypt(conn.ID, secretApp, *p.APIKey)
		if err != nil {
			return fmt.Errorf("announce: encrypt tool key: %w", err)
		}
		conn.APIKeyEncrypted, conn.KeyID = enc, s.keyring.KeyID()
	}
	conn.UpdatedAt = s.clock()
	if err := s.repo.UpdateAnnounceConnection(ctx, tx, conn); err != nil {
		if database.IsUniqueViolation(err) {
			return fmt.Errorf("%w: %s at %s", domain.ErrConflict, conn.Kind, apphttp.RedactURL(conn.BaseURL))
		}
		return fmt.Errorf("announce: update connection: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("announce: commit: %w", err)
	}
	return nil
}

// TestConnection probes a connection's reachability (and, for qui, its API key) WITHOUT
// injecting anything. It reuses the same per-kind driver factory Push does; the returned
// error is already scrubbed by the driver.
func (s *Service) TestConnection(ctx context.Context, id int64) error {
	conn, err := s.repo.GetAnnounceConnection(ctx, s.db, id)
	if err != nil {
		return fmt.Errorf("announce: get connection: %w", err)
	}
	toolKey, err := s.keyring.Decrypt(conn.ID, secretApp, conn.APIKeyEncrypted)
	if err != nil {
		return fmt.Errorf("announce: decrypt tool key: %w", err)
	}
	target, err := s.factory(conn, toolKey)
	if err != nil {
		return err
	}
	if err := target.Probe(ctx); err != nil {
		return fmt.Errorf("announce: test connection: %w", err)
	}
	return nil
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
		rels := build(conn)
		// Per-connection budget: Push repeats delivery per connection, so a batch
		// deadline scaled only by release count starves the SECOND connection's tail
		// behind a slow first one. Each connection gets its own release-scaled budget;
		// the caller's ctx stays the overall hard cap.
		connCtx, cancel := context.WithTimeout(ctx, connPushBudget(len(rels)))
		matched += s.pushOne(connCtx, conn, rels)
		cancel()
	}
	return matched
}

// pushBudgetBase is the floor of one connection's push budget — enough for a
// handful of releases against a live target even with PerReleaseTimeout applied
// per release inside pushOne.
const pushBudgetBase = 30 * time.Second

// connPushBudget scales one connection's push deadline with its release count:
// pushOne announces sequentially at up to PerReleaseTimeout each, so a fixed
// budget that fits a small batch fails a large one's tail. The caller's context
// carries the overall hard cap, so no cap is applied here.
func connPushBudget(releases int) time.Duration {
	return pushBudgetBase + time.Duration(releases)*PerReleaseTimeout
}

// PerReleaseTimeout bounds a single release's announce POST. A batch shares one caller-
// supplied context, but pushOne announces releases sequentially — without a per-release
// deadline, one slow/stuck release stalls the shared context until every release after it
// in the batch fails "context deadline exceeded" too (#232). newAnnounceSink sizes its batch
// context off this constant so a big batch gets a proportionally bigger budget.
const PerReleaseTimeout = 10 * time.Second

// pushOne builds the connection's driver and announces each release (each capped at
// PerReleaseTimeout), returning the match count. Per-release failures are not logged
// individually — a large batch would otherwise emit one WRN per failure (#232) — they're
// folded into one batch-summary log after the loop: WRN with the first (redacted) failure
// when any release failed, DBG otherwise.
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

	start := time.Now()
	matched, failed := 0, 0
	var firstFailGUID, firstFailErr string
	for _, rel := range rels {
		relCtx, cancel := context.WithTimeout(ctx, PerReleaseTimeout)
		res, err := target.Announce(relCtx, rel)
		cancel()
		if err != nil {
			failed++
			if firstFailErr == "" {
				// The guid is scrubbed: for passkey-in-GUID trackers (FileList-style)
				// it IS the credential-bearing download URL (#230).
				firstFailGUID, firstFailErr = apphttp.RedactURL(rel.GUID), apphttp.RedactError(err)
			}
			continue
		}
		if res.Matched {
			matched++
		}
	}

	msg := "announce: push batch complete"
	ev := s.log.Debug()
	if failed > 0 {
		msg = fmt.Sprintf("announce: push failed for %d/%d releases in batch", failed, len(rels))
		ev = s.log.Warn().Str("guid", firstFailGUID).Str("error", firstFailErr)
	}
	ev.Int64("connection_id", conn.ID).Int("pushed", len(rels)-failed).Int("failed", failed).
		Dur("duration", time.Since(start)).Msg(msg)
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

// applyAnnounceUpdate overlays the non-nil patch fields onto conn, trimming and validating
// each (the same rules as create) so a partial edit can't persist a blank name or a
// host-less URL. The tool key is handled by the caller (it re-encrypts); kind is immutable.
func applyAnnounceUpdate(conn *domain.AnnounceConnection, p UpdateConnectionParams) error {
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if name == "" {
			return fmt.Errorf("%w: name is required", domain.ErrInvalid)
		}
		conn.Name = name
	}
	if p.BaseURL != nil {
		base, err := domain.ValidateAbsURL("base url", *p.BaseURL)
		if err != nil {
			return err
		}
		conn.BaseURL = base
	}
	if p.HarbrrURL != nil {
		harbrr, err := domain.ValidateAbsURL("harbrr url", *p.HarbrrURL)
		if err != nil {
			return err
		}
		conn.HarbrrURL = harbrr
	}
	return nil
}

func validateKind(kind string) error {
	switch kind {
	case domain.AnnounceKindQui, domain.AnnounceKindCrossSeedV6:
		return nil
	default:
		return fmt.Errorf("%w: kind must be qui or crossseed-v6 (got %q)", domain.ErrInvalid, kind)
	}
}
