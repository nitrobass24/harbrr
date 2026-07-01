// Package notify fires harbrr's operational events (an indexer health failure today)
// at configured notification targets so an operator learns an indexer broke without
// tailing logs. Two senders ship in the MVP: a generic webhook (JSON POST) and a
// Discord webhook (embed). The destination URL is a secret — it routinely embeds a
// bearer token — so it is encrypted at rest, redacted in logs, and never echoed in an
// error. Dispatch is best-effort and asynchronous: a failed send is logged (scrubbed)
// and never blocks or breaks the search path that triggered it.
package notify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/autobrr/harbrr/internal/domain"
)

// httpClientTimeout bounds a single send so an unresponsive webhook endpoint cannot
// hang the dispatcher goroutine.
const httpClientTimeout = 15 * time.Second

// defaultHTTPClient is the fallback client the senders use when none is injected.
func defaultHTTPClient() *http.Client { return &http.Client{Timeout: httpClientTimeout} }

// ErrInvalid is the service's input-mapping sentinel (the handler turns it into 400).
// Not-found flows through database.ErrNotFound.
var ErrInvalid = errors.New("notify: invalid input")

// Event kinds — the operational triggers a notification fires on. Stored nowhere
// (they are dispatch-time labels), carried in the sent payload's `event` field.
const (
	// EventIndexerHealth is a classified indexer failure (auth/anti-bot/rate-limited/
	// parse) recorded by the registry — the MVP trigger.
	EventIndexerHealth = "indexer_health"
)

// Event is one operational occurrence dispatched to the matching targets. Detail is
// already credential-scrubbed by the caller (registry passes internal/http.RedactError
// output); this package never adds a secret to it.
type Event struct {
	// Event is the trigger label (e.g. EventIndexerHealth).
	Event string
	// Indexer is the human-facing indexer identifier (its slug).
	Indexer string
	// Kind is the health-event kind (domain.HealthAuthFailure, ...).
	Kind string
	// Detail is the scrubbed failure detail.
	Detail string
	// Timestamp is when the event was dispatched.
	Timestamp time.Time
}

// Sender delivers one Event to a concrete destination (a webhook endpoint, Discord,
// ...). Implementations must not log or echo their destination URL. A transport or
// non-2xx failure returns a scrubbed error; the dispatcher logs it and moves on.
type Sender interface {
	Send(ctx context.Context, e Event) error
}

// newSender builds the per-type Sender for a decrypted destination URL. It is the
// single factory both dispatch and the test action route through.
func newSender(typ, url string, client *http.Client) (Sender, error) {
	if client == nil {
		client = defaultHTTPClient()
	}
	switch typ {
	case domain.NotifyTypeWebhook:
		return newWebhook(url, client), nil
	case domain.NotifyTypeDiscord:
		return newDiscord(url, client), nil
	default:
		return nil, fmt.Errorf("%w: unknown notification type %q", ErrInvalid, typ)
	}
}
