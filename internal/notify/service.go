package notify

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/secrets"
)

// secretURL is the AAD discriminator for a notification's single encrypted secret (its
// destination URL), bound alongside the notification id — mirroring appsync/announce.
const secretURL = "url"

// Service persists notification targets (encrypting the destination URL) and dispatches
// operational events to the enabled, matching ones. It implements the registry's health
// sink: a recorded indexer health failure fans out to every enabled target whose
// on_health_failure flag is set, asynchronously and best-effort.
type Service struct {
	// dispatchWG tracks in-flight detached dispatch goroutines so Drain can join them
	// before the DB is torn down at shutdown (dispatch reads the DB).
	dispatchWG sync.WaitGroup
	db         dbinterface.Querier
	repo       database.Notifications
	keyring    *secrets.Keyring
	client     *http.Client
	clock      func() time.Time
	log        zerolog.Logger
}

// NewService wires the notify service. client is shared by all senders (nil installs a
// timeout-bounded default); clock is injectable for deterministic tests.
func NewService(db dbinterface.Querier, keyring *secrets.Keyring, client *http.Client, log zerolog.Logger) *Service {
	if client == nil {
		client = defaultHTTPClient()
	}
	return &Service{db: db, keyring: keyring, client: client, clock: time.Now, log: log}
}

// CreateNotificationParams is the input to CreateNotification. OnHealthFailure is a
// pointer so an omitted flag defaults ON (a freshly-added target immediately surfaces
// indexer breakage) rather than silently off.
type CreateNotificationParams struct {
	Name            string
	Type            string
	URL             string
	OnHealthFailure *bool
}

// CreateNotification persists a target with its destination URL encrypted. The row is
// written first (to mint the id the encryption AAD binds to), then its sealed secret,
// in one transaction.
func (s *Service) CreateNotification(ctx context.Context, p CreateNotificationParams) (domain.Notification, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.URL = strings.TrimSpace(p.URL)
	if err := validateCreate(p); err != nil {
		return domain.Notification{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Notification{}, fmt.Errorf("notify: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.clock()
	n := domain.Notification{
		Name: p.Name, Type: p.Type, Enabled: true,
		OnHealthFailure: p.OnHealthFailure == nil || *p.OnHealthFailure,
		CreatedAt:       now, UpdatedAt: now,
	}
	id, err := s.repo.InsertNotification(ctx, tx, n)
	if err != nil {
		return domain.Notification{}, fmt.Errorf("notify: insert notification: %w", err)
	}
	n.ID = id

	enc, err := s.keyring.Encrypt(id, secretURL, p.URL)
	if err != nil {
		return domain.Notification{}, fmt.Errorf("notify: encrypt url: %w", err)
	}
	if err := s.repo.SetNotificationSecret(ctx, tx, id, enc, s.keyring.KeyID()); err != nil {
		return domain.Notification{}, fmt.Errorf("notify: set notification secret: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.Notification{}, fmt.Errorf("notify: commit: %w", err)
	}
	n.URLEncrypted, n.KeyID = enc, s.keyring.KeyID()
	return n, nil
}

// UpdateNotificationParams patches a target; nil fields are left unchanged. URL, when
// set, rotates the destination (re-encrypted in place).
type UpdateNotificationParams struct {
	Name            *string
	URL             *string
	OnHealthFailure *bool
}

// UpdateNotification applies a patch, re-encrypting the URL when rotated.
func (s *Service) UpdateNotification(ctx context.Context, id int64, p UpdateNotificationParams) error {
	n, err := s.repo.GetNotification(ctx, s.db, id)
	if err != nil {
		return fmt.Errorf("notify: get notification: %w", err)
	}
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if name == "" {
			return fmt.Errorf("%w: name must not be blank", ErrInvalid)
		}
		n.Name = name
	}
	if p.OnHealthFailure != nil {
		n.OnHealthFailure = *p.OnHealthFailure
	}
	if p.URL != nil {
		raw := strings.TrimSpace(*p.URL)
		if err := validateURL(raw); err != nil {
			return err
		}
		enc, err := s.keyring.Encrypt(n.ID, secretURL, raw)
		if err != nil {
			return fmt.Errorf("notify: encrypt url: %w", err)
		}
		n.URLEncrypted, n.KeyID = enc, s.keyring.KeyID()
	}
	n.UpdatedAt = s.clock()
	if err := s.repo.UpdateNotification(ctx, s.db, n); err != nil {
		return fmt.Errorf("notify: update notification: %w", err)
	}
	return nil
}

// ListNotifications / GetNotification expose persisted state (the URL stays encrypted;
// the handler redacts it).
func (s *Service) ListNotifications(ctx context.Context) ([]domain.Notification, error) {
	list, err := s.repo.ListNotifications(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("notify: list notifications: %w", err)
	}
	return list, nil
}

func (s *Service) GetNotification(ctx context.Context, id int64) (domain.Notification, error) {
	n, err := s.repo.GetNotification(ctx, s.db, id)
	if err != nil {
		return domain.Notification{}, fmt.Errorf("notify: get notification: %w", err)
	}
	return n, nil
}

// SetEnabled toggles a target's enabled flag.
func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	if err := s.repo.SetNotificationEnabled(ctx, s.db, id, enabled, s.clock()); err != nil {
		return fmt.Errorf("notify: set enabled: %w", err)
	}
	return nil
}

// DeleteNotification removes a target by id.
func (s *Service) DeleteNotification(ctx context.Context, id int64) error {
	if err := s.repo.DeleteNotification(ctx, s.db, id); err != nil {
		return fmt.Errorf("notify: delete notification: %w", err)
	}
	return nil
}

// TestNotification sends a synthetic event to one target so an operator can confirm the
// destination works. The returned error is already scrubbed by the sender.
func (s *Service) TestNotification(ctx context.Context, id int64) error {
	n, err := s.repo.GetNotification(ctx, s.db, id)
	if err != nil {
		return fmt.Errorf("notify: get notification: %w", err)
	}
	sender, err := s.sender(n)
	if err != nil {
		return err
	}
	ev := Event{
		Event:     EventIndexerHealth,
		Indexer:   "test-indexer",
		Kind:      domain.HealthAuthFailure,
		Detail:    "harbrr test notification",
		Timestamp: s.clock(),
	}
	if err := sender.Send(ctx, ev); err != nil {
		return fmt.Errorf("notify: test notification: %w", err)
	}
	return nil
}

// OnHealthEvent is the registry health sink: after a health failure is recorded the
// registry calls this best-effort. It never blocks the search path — dispatch runs on a
// detached context in its own goroutine, so a slow or failing webhook can't slow a
// search or propagate an error back into it.
func (s *Service) OnHealthEvent(ctx context.Context, indexer, kind, detail string) {
	ev := Event{
		Event:     EventIndexerHealth,
		Indexer:   indexer,
		Kind:      kind,
		Detail:    detail,
		Timestamp: s.clock(),
	}
	// Detach from the caller's request context (which is cancelled the moment the search
	// returns) so the send outlives it, but keep the process-wide cancellation absent —
	// the sender's own HTTP timeout bounds it. Tracked on dispatchWG so shutdown (Drain)
	// joins the goroutine before db.Close, since dispatch reads the DB.
	s.dispatchWG.Add(1)
	go func() {
		defer s.dispatchWG.Done()
		s.dispatch(context.WithoutCancel(ctx), ev, func(n domain.Notification) bool {
			return n.OnHealthFailure
		})
	}()
}

// Drain waits for in-flight dispatch goroutines to finish before returning, bounded by
// ctx (a hanging webhook must not stall shutdown indefinitely). Call it during shutdown
// after the server stops accepting requests and before the database is closed.
func (s *Service) Drain(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		s.dispatchWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		s.log.Warn().Msg("notify: drain deadline reached; abandoning in-flight sends")
	}
}

// dispatch fans an event out to every enabled target the match predicate selects,
// best-effort: a per-target build or send failure is logged (scrubbed) and never blocks
// the rest. It is synchronous (OnHealthEvent runs it in a goroutine); tests call it
// directly for determinism.
func (s *Service) dispatch(ctx context.Context, e Event, match func(domain.Notification) bool) {
	list, err := s.repo.ListNotifications(ctx, s.db)
	if err != nil {
		s.log.Warn().Str("error", apphttp.RedactError(err)).Msg("notify: list targets for dispatch failed")
		return
	}
	for _, n := range list {
		if !n.Enabled || !match(n) {
			continue
		}
		s.dispatchOne(ctx, n, e)
	}
}

// dispatchOne builds one target's sender and sends the event, logging (scrubbed) any
// failure. The target name is safe to log; the URL is never touched.
func (s *Service) dispatchOne(ctx context.Context, n domain.Notification, e Event) {
	sender, err := s.sender(n)
	if err != nil {
		s.log.Warn().Int64("notification_id", n.ID).Str("error", apphttp.RedactError(err)).
			Msg("notify: build sender failed")
		return
	}
	if err := sender.Send(ctx, e); err != nil {
		s.log.Warn().Int64("notification_id", n.ID).Str("type", n.Type).
			Str("error", apphttp.RedactError(err)).Msg("notify: send failed")
	}
}

// sender decrypts a target's destination URL and builds its Sender.
func (s *Service) sender(n domain.Notification) (Sender, error) {
	dest, err := s.keyring.Decrypt(n.ID, secretURL, n.URLEncrypted)
	if err != nil {
		return nil, fmt.Errorf("notify: decrypt url: %w", err)
	}
	return newSender(n.Type, dest, s.client)
}

// validateCreate checks a create request: name, a known type, and a well-formed URL.
func validateCreate(p CreateNotificationParams) error {
	if p.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if err := validateType(p.Type); err != nil {
		return err
	}
	return validateURL(p.URL)
}

// validateType rejects an unknown sender type up front (the same set newSender builds).
func validateType(typ string) error {
	switch typ {
	case domain.NotifyTypeWebhook, domain.NotifyTypeDiscord:
		return nil
	default:
		return fmt.Errorf("%w: type must be webhook or discord (got %q)", ErrInvalid, typ)
	}
}

// validateURL requires an absolute http(s) URL with a host, so a malformed/relative
// destination can't be persisted and later fail every send.
func validateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%w: url must be an absolute http(s) URL", ErrInvalid)
	}
	return nil
}
