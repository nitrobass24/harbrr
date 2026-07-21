package announce

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/apps"
	"github.com/autobrr/harbrr/internal/connresource"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/secrets"
)

// secretHarbrr is the AAD discriminator for the minted harbrr key (the tool's own key
// lives on the App now, decrypted via s.apps, not on the row).
const secretHarbrr = "harbrr"

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
	apps    *apps.Service
	minter  connresource.KeyMinter
	keyring *secrets.Keyring
	factory TargetFactory
	clock   func() time.Time
	life    *connresource.Lifecycle[domain.AnnounceConnection]
	log     zerolog.Logger
}

// NewService wires the announce service. appsSvc owns the app identity/credential a
// connection references; factory builds the per-kind driver (see DefaultTargetFactory
// for the production wiring).
func NewService(db dbinterface.Querier, appsSvc *apps.Service, minter connresource.KeyMinter, keyring *secrets.Keyring, factory TargetFactory, log zerolog.Logger) *Service {
	s := &Service{
		db: db, apps: appsSvc, minter: minter, keyring: keyring, factory: factory,
		clock: time.Now, log: log,
	}
	s.life = connresource.New[domain.AnnounceConnection](db, keyring, func() time.Time { return s.clock() })
	return s
}

// CreateConnectionParams is the input to CreateConnection. It references the App holding
// the tool's identity + credential either by AppID (reuse) or inline
// (BaseURL/APIKey/Username get-or-create); HarbrrURL, when set, backfills the App's
// harbrr /dl URL.
type CreateConnectionParams struct {
	Name      string
	Kind      string
	AppID     *int64
	BaseURL   string
	APIKey    string
	Username  string
	HarbrrURL string
}

// CreateConnection resolves the App the connection references (get-or-create), enforces
// that it has a harbrr /dl URL, then mints a dedicated harbrr key and persists the
// connection. Only the minted harbrr key is sealed on the row — the tool's credential
// lives on the App. A failed persist revokes the orphan key.
func (s *Service) CreateConnection(ctx context.Context, p CreateConnectionParams) (domain.AnnounceConnection, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	p.HarbrrURL = strings.TrimSpace(p.HarbrrURL)
	if err := validateCreate(p); err != nil {
		return domain.AnnounceConnection{}, err
	}
	app, err := s.apps.Resolve(ctx, apps.Ref{
		AppID: p.AppID, Kind: p.Kind, Name: p.Name, BaseURL: p.BaseURL, Username: p.Username, APIKey: p.APIKey, HarbrrURL: p.HarbrrURL,
	})
	if err != nil {
		return domain.AnnounceConnection{}, fmt.Errorf("announce: resolve app: %w", err)
	}
	if app.HarbrrURL == "" {
		return domain.AnnounceConnection{}, fmt.Errorf("%w: harbrr url is required (the tool fetches harbrr's /dl link)", domain.ErrInvalid)
	}
	return s.life.Create(ctx, connresource.CreateSpec[domain.AnnounceConnection]{
		Minter:   s.minter,
		MintName: "announce: " + p.Name,
		Build: func(now time.Time, mintedKeyID int64) domain.AnnounceConnection {
			return domain.AnnounceConnection{
				Name: p.Name, Kind: p.Kind, AppID: &app.ID, BaseURL: app.BaseURL, HarbrrURL: app.HarbrrURL,
				HarbrrAPIKeyID: mintedKeyID, Enabled: true, CreatedAt: now, UpdatedAt: now,
			}
		},
		Insert: func(ctx context.Context, q dbinterface.Execer, conn domain.AnnounceConnection) (int64, error) {
			return s.repo.InsertAnnounceConnection(ctx, q, conn)
		},
		// Only the minted harbrr key is sealed on the connection; the tool credential
		// lives on the App (base_url is written for the (kind, base_url) unique index).
		Secrets: func(_ domain.AnnounceConnection, mintedPlain string) []connresource.Secret {
			return []connresource.Secret{{Discriminator: secretHarbrr, Plaintext: mintedPlain}}
		},
		SetSecrets: func(ctx context.Context, q dbinterface.Execer, id int64, encrypted []string, keyID string) error {
			return s.repo.SetAnnounceConnectionSecrets(ctx, q, id, encrypted[0], keyID)
		},
		Finalize: func(conn domain.AnnounceConnection, id int64, encrypted []string, keyID string) domain.AnnounceConnection {
			conn.ID, conn.HarbrrAPIKeyEncrypted, conn.KeyID = id, encrypted[0], keyID
			return conn
		},
		// The conflict IS about the App now (uniqueness moved to app_id): close over the
		// already-resolved app rather than relying on conn.BaseURL being copied correctly
		// by Build (it is, but this reads clearer).
		Conflict: func(_ domain.AnnounceConnection) error {
			return fmt.Errorf("%w: %s at %s", domain.ErrConflict, app.Kind, apphttp.RedactURL(app.BaseURL))
		},
	})
}

// annAppID and annApplyApp are the App-projection accessors apps.EnrichList/EnrichOne
// need: which field on the row holds the App reference, and which fields the App
// projects onto it.
func annAppID(c *domain.AnnounceConnection) *int64 { return c.AppID }

func annApplyApp(c *domain.AnnounceConnection, a domain.App) {
	c.BaseURL, c.HarbrrURL = a.BaseURL, a.HarbrrURL
}

// ListConnections / GetConnection expose persisted state, base URL + harbrr URL enriched
// from each connection's App (the single read path — the App is the sole store for
// these fields).
func (s *Service) ListConnections(ctx context.Context) ([]domain.AnnounceConnection, error) {
	list, err := s.repo.ListAnnounceConnections(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("announce: list connections: %w", err)
	}
	if err := apps.EnrichList(ctx, s.apps, list, annAppID, annApplyApp); err != nil {
		return nil, fmt.Errorf("announce: enrich connections: %w", err)
	}
	return list, nil
}

func (s *Service) GetConnection(ctx context.Context, id int64) (domain.AnnounceConnection, error) {
	conn, err := s.repo.GetAnnounceConnection(ctx, s.db, id)
	if err != nil {
		return domain.AnnounceConnection{}, fmt.Errorf("announce: get connection: %w", err)
	}
	if err := apps.EnrichOne(ctx, s.apps, &conn, annAppID, annApplyApp); err != nil {
		return domain.AnnounceConnection{}, fmt.Errorf("announce: enrich connection: %w", err)
	}
	return conn, nil
}

// UpdateConnectionParams patches a connection's surface fields; nil fields are left
// unchanged. Identity + credential (base URL, api key, harbrr URL) are App-level now —
// rotated via the App — so a PATCH is name-only. Kind is immutable.
type UpdateConnectionParams struct {
	Name *string
}

// UpdateConnection applies a name-only patch. The read → write runs in one transaction
// (the appsync UpdateConnection precedent).
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
	conn.UpdatedAt = s.clock()
	// No IsUniqueViolation check here: this patch is name-only (UpdateConnectionParams
	// never touches app_id), and app_id — not name — is what UNIQUE(app_id) guards, so
	// this write can never violate it.
	if err := s.repo.UpdateAnnounceConnection(ctx, tx, conn); err != nil {
		return fmt.Errorf("announce: update connection: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("announce: commit: %w", err)
	}
	return nil
}

// TestConnection probes a connection's reachability (and, for qui, its API key) WITHOUT
// injecting anything. It loads the connection's App for the base URL + decrypted tool
// key; the returned error is already scrubbed by the driver. AppID is never nil here:
// migration 0021 refuses to apply while any non-hostless row still has a NULL app_id, so
// every announce connection this service can read is folded.
func (s *Service) TestConnection(ctx context.Context, id int64) error {
	conn, err := s.repo.GetAnnounceConnection(ctx, s.db, id)
	if err != nil {
		return fmt.Errorf("announce: get connection: %w", err)
	}
	app, toolKey, err := s.apps.Bind(ctx, *conn.AppID)
	if err != nil {
		return fmt.Errorf("announce: bind app: %w", err)
	}
	conn.BaseURL = app.BaseURL
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
		// Enrich identity from the connection's App (the single read path) before
		// building /dl links (needs the App's harbrr URL) or the driver (needs its base
		// URL). A lookup failure skips the connection best-effort rather than failing
		// the whole batch.
		if err := apps.EnrichOne(ctx, s.apps, &conn, annAppID, annApplyApp); err != nil {
			s.log.Warn().Int64("connection_id", conn.ID).Str("error", apphttp.RedactError(err)).Msg("announce: skip connection for push")
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

// pushOne binds the connection's App for its decrypted tool key, builds the driver, and
// announces each release (each capped at PerReleaseTimeout), returning the match count.
// Bind runs after the empty-rels short-circuit, so a connection with nothing to push
// never decrypts its App's key at all (Push already Get-enriched it once, for the /dl
// links; Bind here is a second, independent App lookup for the credential). Per-release
// failures are not logged individually — a large batch would otherwise emit one WRN per
// failure (#232) — they're folded into one batch-summary log after the loop: WRN with the
// first (redacted) failure when any release failed, DBG otherwise.
func (s *Service) pushOne(ctx context.Context, conn domain.AnnounceConnection, rels []Release) int {
	if len(rels) == 0 {
		return 0
	}
	_, toolKey, err := s.apps.Bind(ctx, *conn.AppID)
	if err != nil {
		s.log.Warn().Int64("connection_id", conn.ID).Str("error", apphttp.RedactError(err)).Msg("announce: bind app failed")
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

// validateCreate checks the required fields of a create request. Identity (base URL,
// api key, harbrr URL) is the App's concern — validated by the apps service on
// get-or-create — so it is required here only for the inline path (no AppID); the
// AppID (reuse) path references an App that already owns validated identity.
func validateCreate(p CreateConnectionParams) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name is required", domain.ErrInvalid)
	}
	if err := validateKind(p.Kind); err != nil {
		return err
	}
	if p.AppID != nil {
		return nil
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return fmt.Errorf("%w: base url is required", domain.ErrInvalid)
	}
	if strings.TrimSpace(p.APIKey) == "" {
		return fmt.Errorf("%w: api key is required", domain.ErrInvalid)
	}
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

// applyAnnounceUpdate overlays the name patch onto conn (identity is App-level now, so
// a PATCH is name-only). Kind is immutable.
func applyAnnounceUpdate(conn *domain.AnnounceConnection, p UpdateConnectionParams) error {
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if name == "" {
			return fmt.Errorf("%w: name is required", domain.ErrInvalid)
		}
		conn.Name = name
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
