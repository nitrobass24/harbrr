package notify

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// newService builds a notify.Service over an in-memory DB with a fixed clock. The
// concrete keyring is returned so a test can decrypt the stored URL to prove it round
// trips (and is not stored in the clear).
func newService(t *testing.T) (*Service, *secrets.Keyring) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	svc := NewService(db, kr, http.DefaultClient, zerolog.Nop())
	svc.clock = func() time.Time { return time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC) }
	return svc, kr
}

// countingServer answers every request 204 and counts them.
func countingServer(t *testing.T) (*httptest.Server, *int64) {
	t.Helper()
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&n, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func ptrBool(b bool) *bool { return &b }

func TestCreateEncryptsURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, kr := newService(t)

	const url = "https://hooks.example/services/T/B/XYZSECRET"
	n, err := svc.CreateNotification(ctx, CreateNotificationParams{
		Name: "ops", Type: domain.NotifyTypeWebhook, URL: url,
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if !n.Enabled || !n.OnHealthFailure {
		t.Errorf("defaults: enabled=%v onHealthFailure=%v, want both true", n.Enabled, n.OnHealthFailure)
	}
	if n.URLEncrypted == url || n.URLEncrypted == "" {
		t.Errorf("URL stored in the clear (or empty): %q", n.URLEncrypted)
	}
	got, err := kr.Decrypt(n.ID, secretURL, n.URLEncrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != url {
		t.Errorf("decrypted url = %q, want %q", got, url)
	}
}

func TestCreateDefaultsOnHealthFailureOff(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	n, err := svc.CreateNotification(context.Background(), CreateNotificationParams{
		Name: "ops", Type: domain.NotifyTypeDiscord, URL: "https://discord.example/api/webhooks/1/x",
		OnHealthFailure: ptrBool(false),
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if n.OnHealthFailure {
		t.Error("onHealthFailure = true, want the explicit false to stick")
	}
}

func TestCreateValidation(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	tests := []struct {
		name string
		p    CreateNotificationParams
	}{
		{"blank name", CreateNotificationParams{Name: "  ", Type: domain.NotifyTypeWebhook, URL: "http://x.invalid"}},
		{"unknown type", CreateNotificationParams{Name: "n", Type: "sms", URL: "http://x.invalid"}},
		{"relative url", CreateNotificationParams{Name: "n", Type: domain.NotifyTypeWebhook, URL: "/hook"}},
		{"non-http url", CreateNotificationParams{Name: "n", Type: domain.NotifyTypeWebhook, URL: "ftp://x.invalid"}},
		{"blank url", CreateNotificationParams{Name: "n", Type: domain.NotifyTypeWebhook, URL: ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := svc.CreateNotification(context.Background(), tt.p); !errors.Is(err, ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestUpdateRotatesURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, kr := newService(t)
	n, err := svc.CreateNotification(ctx, CreateNotificationParams{
		Name: "ops", Type: domain.NotifyTypeWebhook, URL: "https://old.example/hook",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newURL := "https://new.example/hook?token=ROTATED"
	if err := svc.UpdateNotification(ctx, n.ID, UpdateNotificationParams{
		Name: strPtr("renamed"), URL: &newURL, OnHealthFailure: ptrBool(false),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := svc.GetNotification(ctx, n.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "renamed" || got.OnHealthFailure {
		t.Errorf("patch not applied: name=%q onHealthFailure=%v", got.Name, got.OnHealthFailure)
	}
	dec, err := kr.Decrypt(got.ID, secretURL, got.URLEncrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if dec != newURL {
		t.Errorf("rotated url = %q, want %q", dec, newURL)
	}
}

func TestUpdateRejectsBadURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	n, _ := svc.CreateNotification(ctx, CreateNotificationParams{
		Name: "ops", Type: domain.NotifyTypeWebhook, URL: "https://old.example/hook",
	})
	bad := "not-a-url"
	if err := svc.UpdateNotification(ctx, n.ID, UpdateNotificationParams{URL: &bad}); !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
}

func TestSetEnabledAndDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	n, _ := svc.CreateNotification(ctx, CreateNotificationParams{
		Name: "ops", Type: domain.NotifyTypeWebhook, URL: "https://x.example/hook",
	})
	if err := svc.SetEnabled(ctx, n.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got, _ := svc.GetNotification(ctx, n.ID)
	if got.Enabled {
		t.Error("still enabled after disable")
	}
	if err := svc.DeleteNotification(ctx, n.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetNotification(ctx, n.ID); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("get after delete err = %v, want ErrNotFound", err)
	}
}

// TestDispatchFansOutToMatchingTargets asserts dispatch delivers only to enabled targets
// whose match predicate holds — a disabled target and a non-matching one are skipped.
func TestDispatchFansOutToMatchingTargets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	srv, hits := countingServer(t)

	// enabled + on_health_failure: delivered.
	if _, err := svc.CreateNotification(ctx, CreateNotificationParams{
		Name: "match", Type: domain.NotifyTypeWebhook, URL: srv.URL,
	}); err != nil {
		t.Fatalf("create match: %v", err)
	}
	// enabled but on_health_failure OFF: skipped by the predicate.
	if _, err := svc.CreateNotification(ctx, CreateNotificationParams{
		Name: "no-health", Type: domain.NotifyTypeWebhook, URL: srv.URL, OnHealthFailure: ptrBool(false),
	}); err != nil {
		t.Fatalf("create no-health: %v", err)
	}
	// disabled: skipped.
	disabled, err := svc.CreateNotification(ctx, CreateNotificationParams{
		Name: "off", Type: domain.NotifyTypeWebhook, URL: srv.URL,
	})
	if err != nil {
		t.Fatalf("create off: %v", err)
	}
	if err := svc.SetEnabled(ctx, disabled.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	svc.dispatch(ctx, Event{Event: EventIndexerHealth, Kind: domain.HealthAuthFailure},
		func(n domain.Notification) bool { return n.OnHealthFailure })

	if got := atomic.LoadInt64(hits); got != 1 {
		t.Errorf("delivered to %d targets, want 1 (only the enabled matching one)", got)
	}
}

// TestOnHealthEventDelivers proves the sink path (async) eventually delivers.
func TestOnHealthEventDelivers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	srv, hits := countingServer(t)
	if _, err := svc.CreateNotification(ctx, CreateNotificationParams{
		Name: "match", Type: domain.NotifyTypeWebhook, URL: srv.URL,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	svc.OnHealthEvent(ctx, "mytracker", domain.HealthAntiBot, "blocked")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(hits) == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("OnHealthEvent did not deliver within the deadline (hits=%d)", atomic.LoadInt64(hits))
}

func TestTestNotification(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t)
	srv, hits := countingServer(t)
	n, err := svc.CreateNotification(ctx, CreateNotificationParams{
		Name: "match", Type: domain.NotifyTypeWebhook, URL: srv.URL,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.TestNotification(ctx, n.ID); err != nil {
		t.Fatalf("TestNotification: %v", err)
	}
	if atomic.LoadInt64(hits) != 1 {
		t.Errorf("test delivered %d, want 1", atomic.LoadInt64(hits))
	}
	if err := svc.TestNotification(ctx, 9999); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("test unknown id err = %v, want ErrNotFound", err)
	}
}

func strPtr(s string) *string { return &s }
