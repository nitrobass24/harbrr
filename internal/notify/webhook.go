package notify

import (
	"context"
	"net/http"
	"time"
)

// webhookPayload is the JSON body posted to a generic webhook. It mirrors Event field
// for field (so Send can convert straight across) but adds the stable JSON tags a
// consumer keys on. It carries no secret — Detail is already scrubbed by the caller.
type webhookPayload struct {
	Event     string    `json:"event"`
	Indexer   string    `json:"indexer"`
	Kind      string    `json:"kind"`
	Detail    string    `json:"detail"`
	Timestamp time.Time `json:"timestamp"`
}

// webhook is the generic-webhook Sender: it POSTs the flat webhookPayload as JSON.
type webhook struct {
	p poster
}

// newWebhook builds a generic-webhook sender for a destination URL.
func newWebhook(url string, client *http.Client) *webhook {
	return &webhook{p: poster{kind: "webhook", url: url, client: client}}
}

// Send posts the event as a flat JSON object to the configured URL. webhookPayload is
// Event field for field, so the conversion just attaches the JSON tags.
func (w *webhook) Send(ctx context.Context, e Event) error {
	return w.p.post(ctx, webhookPayload(e))
}
