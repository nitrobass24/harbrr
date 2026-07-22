package notify

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/connresource"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/secrets"
)

// secretURL is the AAD discriminator for a notification's single encrypted secret (its
// destination URL), bound alongside the notification id — mirroring appsync/announce.
const secretURL = "url"

// healthNotifyCooldown debounces repeated health-failure notifications for the same
// (indexer, kind): a persistently-broken indexer is polled every 15–60 min by each app,
// and recordHealth fires on every failure with no recovery edge to reset on, so without
// a gate one broken indexer spams hundreds of identical messages a day and trains the
// operator to mute the channel. One hour is long enough to collapse poll-spam yet short
// enough to re-alert (roughly daily-ish) while the indexer stays broken.
const healthNotifyCooldown = time.Hour

// healthKey identifies a health-notification stream for cooldown accounting. The indexer
// slug is stable and unique per indexer and Kind is one of the four classified health
// kinds, so (indexer, kind) is a sufficient dedup key.
type healthKey struct {
	indexer string
	kind    string
}

// Service persists notification targets (encrypting the destination URL) and dispatches
// operational events to the enabled, matching ones. It implements the registry's health
// sink: a recorded indexer health failure fans out to every enabled target whose
// on_health_failure flag is set, asynchronously and best-effort. Create/Update/Delete of
// the target row and its encrypted secret are sequenced by connresource.Lifecycle; notify
// mints nothing (unlike appsync/announce), so its specs simply leave Minter nil.
type Service struct {
	// dispatchWG tracks in-flight detached dispatch goroutines so Drain can join them
	// before the DB is torn down at shutdown (dispatch reads the DB).
	dispatchWG sync.WaitGroup
	db         dbinterface.Querier
	repo       database.Notifications
	keyring    *secrets.Keyring
	client     *http.Client
	clock      func() time.Time
	life       *connresource.Lifecycle[domain.Notification]
	// healthMu guards lastHealthNotify, the per-(indexer, kind) time of the last
	// dispatched health notification. It debounces poll-spam (see healthNotifyCooldown)
	// and must be a distinct lock from dispatchWG's accounting.
	healthMu         sync.Mutex
	lastHealthNotify map[healthKey]time.Time
	log              zerolog.Logger
}

// NewService wires the notify service. client is shared by all senders (nil installs a
// timeout-bounded default); clock is injectable for deterministic tests (assigning to the
// returned Service's clock field also retunes its Lifecycle, which reads clock through an
// indirection).
func NewService(db dbinterface.Querier, keyring *secrets.Keyring, client *http.Client, log zerolog.Logger) *Service {
	if client == nil {
		client = defaultHTTPClient()
	}
	s := &Service{
		db: db, keyring: keyring, client: client, clock: time.Now,
		lastHealthNotify: make(map[healthKey]time.Time), log: log,
	}
	s.life = connresource.New[domain.Notification](db, keyring, func() time.Time { return s.clock() })
	return s
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
	return s.life.Create(ctx, connresource.CreateSpec[domain.Notification]{
		Build: func(now time.Time, _ int64) domain.Notification {
			return domain.Notification{
				Name: p.Name, Type: p.Type, Enabled: true,
				OnHealthFailure: p.OnHealthFailure == nil || *p.OnHealthFailure,
				CreatedAt:       now, UpdatedAt: now,
			}
		},
		Insert: func(ctx context.Context, q dbinterface.Execer, n domain.Notification) (int64, error) {
			return s.repo.InsertNotification(ctx, q, n)
		},
		Secrets: func(_ domain.Notification, _ string) []connresource.Secret {
			return []connresource.Secret{{Discriminator: secretURL, Plaintext: p.URL}}
		},
		SetSecrets: func(ctx context.Context, q dbinterface.Execer, id int64, encrypted []string, keyID string) error {
			return s.repo.SetNotificationSecret(ctx, q, id, encrypted[0], keyID)
		},
		Finalize: func(n domain.Notification, id int64, encrypted []string, keyID string) domain.Notification {
			n.ID, n.URLEncrypted, n.KeyID = id, encrypted[0], keyID
			return n
		},
	})
}

// UpdateNotificationParams patches a target; nil fields are left unchanged. URL, when
// set, rotates the destination (re-encrypted in place).
type UpdateNotificationParams struct {
	Name            *string
	URL             *string
	OnHealthFailure *bool
}

// UpdateNotification applies a patch, re-encrypting the URL when rotated. The read and the
// full-row write run in one transaction so two overlapping PATCHes can't lose each other's
// write: under SetMaxOpenConns(1) the tx holds the only connection, serializing a concurrent
// UpdateNotification so the second reads the first's committed row (mirrors appsync
// UpdateConnection).
func (s *Service) UpdateNotification(ctx context.Context, id int64, p UpdateNotificationParams) error {
	return s.life.Update(ctx, id, connresource.UpdateSpec[domain.Notification]{
		Get: func(ctx context.Context, q dbinterface.Execer, id int64) (domain.Notification, error) {
			return s.repo.GetNotification(ctx, q, id)
		},
		Patch: func(n *domain.Notification) error {
			if p.Name != nil {
				name := strings.TrimSpace(*p.Name)
				if name == "" {
					return fmt.Errorf("%w: name must not be blank", domain.ErrInvalid)
				}
				n.Name = name
			}
			if p.OnHealthFailure != nil {
				n.OnHealthFailure = *p.OnHealthFailure
			}
			return nil
		},
		Rotate: func(_ *domain.Notification) (connresource.Secret, bool, error) {
			if p.URL == nil {
				return connresource.Secret{}, false, nil
			}
			raw := strings.TrimSpace(*p.URL)
			if err := validateURL(raw); err != nil {
				return connresource.Secret{}, false, err
			}
			return connresource.Secret{Discriminator: secretURL, Plaintext: raw}, true, nil
		},
		Apply: func(n *domain.Notification, encrypted, keyID string) { n.URLEncrypted, n.KeyID = encrypted, keyID },
		Touch: func(n *domain.Notification, now time.Time) { n.UpdatedAt = now },
		Write: func(ctx context.Context, q dbinterface.Execer, n domain.Notification) error {
			return s.repo.UpdateNotification(ctx, q, n)
		},
	})
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

// DeleteNotification removes a target by id. Unlike appsync/announce, notify mints
// nothing, so this is a plain get-then-delete with no revoke step.
func (s *Service) DeleteNotification(ctx context.Context, id int64) error {
	return s.life.Delete(ctx, id, connresource.DeleteSpec[domain.Notification]{
		Get: func(ctx context.Context, q dbinterface.Execer, id int64) (domain.Notification, error) {
			return s.repo.GetNotification(ctx, q, id)
		},
		Delete: func(ctx context.Context, q dbinterface.Execer, id int64) error {
			return s.repo.DeleteNotification(ctx, q, id)
		},
	})
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
	now := s.clock()
	// Debounce: suppress a repeated failure of the same kind for the same indexer within
	// the cooldown window — no dispatch, no goroutine, no send — so a persistently-broken
	// indexer polled every cycle doesn't spam identical messages. The check-and-set is
	// atomic under healthMu so concurrent calls for one key can't both pass the gate.
	if s.healthSuppressed(indexer, kind, now) {
		return
	}
	ev := Event{
		Event:     EventIndexerHealth,
		Indexer:   indexer,
		Kind:      kind,
		Detail:    detail,
		Timestamp: now,
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

// healthSuppressed reports whether a health notification for (indexer, kind) falls inside
// the cooldown window at now. When it does not (the first event, or one past the window),
// it records now as the new last-notified time and returns false so the caller proceeds.
// The check-and-set runs under a single lock so concurrent callers for the same key can't
// both pass.
func (s *Service) healthSuppressed(indexer, kind string, now time.Time) bool {
	key := healthKey{indexer: indexer, kind: kind}
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	if last, ok := s.lastHealthNotify[key]; ok && now.Sub(last) < healthNotifyCooldown {
		return true
	}
	s.lastHealthNotify[key] = now
	return false
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
		return fmt.Errorf("%w: name is required", domain.ErrInvalid)
	}
	if err := validateType(p.Type); err != nil {
		return err
	}
	return validateURL(p.URL)
}

// validateType rejects an unknown sender type up front — a key-existence check against
// senders, the same map newSender builds from.
func validateType(typ string) error {
	if _, ok := senders[typ]; ok {
		return nil
	}
	return fmt.Errorf("%w: type must be webhook or discord (got %q)", domain.ErrInvalid, typ)
}

// validateURL requires an absolute http(s) URL with a host, so a malformed/relative
// destination can't be persisted and later fail every send. The trimmed return is
// discarded: notify persists a separately-trimmed value at each call site.
func validateURL(raw string) error {
	_, err := domain.ValidateAbsURL("url", raw)
	return err
}
