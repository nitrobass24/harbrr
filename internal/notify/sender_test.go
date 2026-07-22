package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
		rec.body, _ = io.ReadAll(r.Body) // ReadAll, not one Read: a large embed body needs several reads
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

// discordEmbedFrom dispatches e through a Discord sender against a 204 stub and returns
// the single posted embed, so a test can assert what actually went on the wire.
func discordEmbedFrom(t *testing.T, e Event) discordEmbed {
	t.Helper()
	srv, rec := captureServer(t, http.StatusNoContent)
	d := newDiscord(srv.URL, srv.Client())
	if err := d.Send(context.Background(), e); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var got discordPayload
	if err := json.Unmarshal(rec.body, &got); err != nil {
		t.Fatalf("unmarshal body %q: %v", rec.body, err)
	}
	if len(got.Embeds) != 1 {
		t.Fatalf("embeds = %d, want 1", len(got.Embeds))
	}
	return got.Embeds[0]
}

// TestDiscordTruncatesOversizeDescription proves the fail-before/pass-after: an oversize
// Detail (a broad login-error selector matching a whole element) is capped to the
// description limit instead of going out full-length and 400ing at Discord.
func TestDiscordTruncatesOversizeDescription(t *testing.T) {
	t.Parallel()
	e := sampleEvent()
	e.Detail = strings.Repeat("x", discordDescriptionMax+500)

	embed := discordEmbedFrom(t, e)

	got := utf8.RuneCountInString(embed.Description)
	if got > discordDescriptionMax {
		t.Errorf("description rune count = %d, want <= %d", got, discordDescriptionMax)
	}
	if !strings.HasSuffix(embed.Description, "…") {
		t.Errorf("truncated description = %q, want a trailing ellipsis", embed.Description)
	}
}

// TestDiscordNormalDescriptionUnchanged proves a normal-length detail is byte-identical
// (no truncation, no ellipsis).
func TestDiscordNormalDescriptionUnchanged(t *testing.T) {
	t.Parallel()
	e := sampleEvent() // Detail is "login failed: 403"

	embed := discordEmbedFrom(t, e)

	if embed.Description != e.Detail {
		t.Errorf("description = %q, want unchanged %q", embed.Description, e.Detail)
	}
	if strings.Contains(embed.Description, "…") {
		t.Errorf("description = %q, want no ellipsis for a short detail", embed.Description)
	}
}

// TestDiscordTruncatesOnRuneBoundary feeds multibyte runes exceeding the cap and asserts
// the result is valid UTF-8 cut on a rune boundary (never mid-rune) with the right count.
func TestDiscordTruncatesOnRuneBoundary(t *testing.T) {
	t.Parallel()
	e := sampleEvent()
	e.Detail = strings.Repeat("世", discordDescriptionMax+50) // 3-byte runes

	embed := discordEmbedFrom(t, e)

	if !utf8.ValidString(embed.Description) {
		t.Errorf("description is not valid UTF-8: %q", embed.Description)
	}
	if got := utf8.RuneCountInString(embed.Description); got != discordDescriptionMax {
		t.Errorf("description rune count = %d, want exactly %d", got, discordDescriptionMax)
	}
	if !strings.HasSuffix(embed.Description, "…") {
		t.Errorf("truncated description = %q, want a trailing ellipsis", embed.Description)
	}
}

// TestDiscordTruncatesOversizeFieldValue proves an oversize field value (here the indexer
// slug) is capped to the field-value limit.
func TestDiscordTruncatesOversizeFieldValue(t *testing.T) {
	t.Parallel()
	e := sampleEvent()
	e.Indexer = strings.Repeat("z", discordFieldValueMax+200)

	embed := discordEmbedFrom(t, e)

	if len(embed.Fields) == 0 {
		t.Fatal("no fields on embed")
	}
	got := utf8.RuneCountInString(embed.Fields[0].Value) // Indexer field
	if got > discordFieldValueMax {
		t.Errorf("field value rune count = %d, want <= %d", got, discordFieldValueMax)
	}
	if !strings.HasSuffix(embed.Fields[0].Value, "…") {
		t.Errorf("truncated field value = %q, want a trailing ellipsis", embed.Fields[0].Value)
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

// TestSendersMessageSync asserts validateType's human-facing rejection message names
// every senders key, so adding a type to the map without updating the message fails
// the build instead of silently drifting.
func TestSendersMessageSync(t *testing.T) {
	t.Parallel()
	err := validateType("carrier-pigeon")
	if err == nil {
		t.Fatal("validateType with an unknown type returned nil error")
	}
	for typ := range senders {
		if !strings.Contains(err.Error(), typ) {
			t.Errorf("rejection message %q does not name registered type %q", err.Error(), typ)
		}
	}
}

// TestSendersRegisteredTypesValidateAndConstruct proves every senders key both passes
// validateType and constructs a non-nil Sender via newSender.
func TestSendersRegisteredTypesValidateAndConstruct(t *testing.T) {
	t.Parallel()
	for typ := range senders {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			if err := validateType(typ); err != nil {
				t.Errorf("validateType(%q) = %v, want nil", typ, err)
			}
			s, err := newSender(typ, "http://x.invalid", nil)
			if err != nil {
				t.Fatalf("newSender(%q): %v", typ, err)
			}
			if s == nil {
				t.Errorf("newSender(%q) returned a nil Sender", typ)
			}
		})
	}
}
