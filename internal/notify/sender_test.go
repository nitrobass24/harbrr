package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/domain"
)

// capturedRequest records what a sender POSTed so a test can assert the payload/headers.
type capturedRequest struct {
	method      string
	contentType string
	body        []byte
}

// captureServer starts an httptest server that records the one request it receives and
// answers with status. It returns the server (Close via t.Cleanup) and the capture.
func captureServer(t *testing.T, status int) (*httptest.Server, *capturedRequest) {
	t.Helper()
	rec := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.contentType = r.Header.Get("Content-Type")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		rec.body = buf
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// sampleEvent is the fixed event the sender tests dispatch.
func sampleEvent() Event {
	return Event{
		Event:     EventIndexerHealth,
		Indexer:   "mytracker",
		Kind:      domain.HealthAuthFailure,
		Detail:    "login failed: 403",
		Timestamp: time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
	}
}

func TestWebhookSendPayload(t *testing.T) {
	t.Parallel()
	srv, rec := captureServer(t, http.StatusOK)

	w := newWebhook(srv.URL, srv.Client())
	if err := w.Send(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if rec.method != http.MethodPost {
		t.Errorf("method = %q, want POST", rec.method)
	}
	if !strings.HasPrefix(rec.contentType, "application/json") {
		t.Errorf("content-type = %q, want application/json", rec.contentType)
	}

	var got webhookPayload
	if err := json.Unmarshal(rec.body, &got); err != nil {
		t.Fatalf("unmarshal body %q: %v", rec.body, err)
	}
	want := webhookPayload{
		Event: EventIndexerHealth, Indexer: "mytracker", Kind: domain.HealthAuthFailure,
		Detail: "login failed: 403", Timestamp: time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
	}
	if got != want {
		t.Errorf("payload = %+v, want %+v", got, want)
	}
}

func TestDiscordSendPayload(t *testing.T) {
	t.Parallel()
	srv, rec := captureServer(t, http.StatusNoContent) // Discord answers 204

	d := newDiscord(srv.URL, srv.Client())
	if err := d.Send(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var got discordPayload
	if err := json.Unmarshal(rec.body, &got); err != nil {
		t.Fatalf("unmarshal body %q: %v", rec.body, err)
	}
	if len(got.Embeds) != 1 {
		t.Fatalf("embeds = %d, want 1", len(got.Embeds))
	}
	e := got.Embeds[0]
	if !strings.Contains(e.Title, "mytracker") || !strings.Contains(e.Title, "auth failure") {
		t.Errorf("title = %q, want it to mention the indexer and humanized kind", e.Title)
	}
	if e.Description != "login failed: 403" {
		t.Errorf("description = %q, want the scrubbed detail", e.Description)
	}
	if e.Color != discordColorFailure {
		t.Errorf("color = %d, want %d", e.Color, discordColorFailure)
	}
	if e.Timestamp != "2026-06-30T12:00:00Z" {
		t.Errorf("timestamp = %q, want RFC3339 UTC", e.Timestamp)
	}
	// The three fields carry indexer/kind/event so a channel reader sees them at a glance.
	if len(e.Fields) != 3 {
		t.Fatalf("fields = %d, want 3", len(e.Fields))
	}
}

func TestSenderNon2xxIsError(t *testing.T) {
	t.Parallel()
	srv, _ := captureServer(t, http.StatusInternalServerError)

	for _, typ := range []string{domain.NotifyTypeWebhook, domain.NotifyTypeDiscord} {
		s, err := newSender(typ, srv.URL, srv.Client())
		if err != nil {
			t.Fatalf("newSender(%q): %v", typ, err)
		}
		if err := s.Send(context.Background(), sampleEvent()); err == nil {
			t.Errorf("%s: Send to a 500 endpoint returned nil, want error", typ)
		}
	}
}

func TestSenderErrorDoesNotLeakURL(t *testing.T) {
	t.Parallel()
	// A URL that carries a secret token, pointed at a dead port so the transport fails.
	const secretURL = "http://127.0.0.1:0/hook?token=SUPERSECRET"

	s, err := newSender(domain.NotifyTypeWebhook, secretURL, defaultHTTPClient())
	if err != nil {
		t.Fatalf("newSender: %v", err)
	}
	err = s.Send(context.Background(), sampleEvent())
	if err == nil {
		t.Fatal("Send to a dead endpoint returned nil, want error")
	}
	if strings.Contains(err.Error(), "SUPERSECRET") {
		t.Errorf("error leaks the secret URL token: %q", err)
	}
}

func TestNewSenderUnknownType(t *testing.T) {
	t.Parallel()
	if _, err := newSender("carrier-pigeon", "http://x.invalid", nil); err == nil {
		t.Fatal("newSender with an unknown type returned nil error")
	}
}
