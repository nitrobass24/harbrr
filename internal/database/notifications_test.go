package database_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
)

func sampleNotification(typ string, now time.Time) domain.Notification {
	return domain.Notification{
		Name: typ + "-target", Type: typ, URLEncrypted: "enc(url)", KeyID: "key-1",
		Enabled: true, OnHealthFailure: true, CreatedAt: now, UpdatedAt: now,
	}
}

func TestNotificationRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.Notifications{}
	now := time.Now().UTC().Truncate(time.Second)

	for _, typ := range []string{domain.NotifyTypeWebhook, domain.NotifyTypeDiscord} {
		t.Run(typ, func(t *testing.T) {
			id, err := repo.InsertNotification(ctx, db, sampleNotification(typ, now))
			if err != nil {
				t.Fatalf("InsertNotification(%s): %v", typ, err)
			}
			got, err := repo.GetNotification(ctx, db, id)
			if err != nil {
				t.Fatalf("GetNotification(%s): %v", typ, err)
			}
			if got.Type != typ || got.URLEncrypted != "enc(url)" || !got.Enabled || !got.OnHealthFailure {
				t.Errorf("round-trip = %+v, want type=%s enc url enabled+onHealthFailure", got, typ)
			}
		})
	}
}

func TestNotificationSetSecretUpdateEnableDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.Notifications{}
	now := time.Now().UTC().Truncate(time.Second)

	id, err := repo.InsertNotification(ctx, db, sampleNotification(domain.NotifyTypeWebhook, now))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := repo.SetNotificationSecret(ctx, db, id, "enc(rotated)", "key-2"); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	upd := sampleNotification(domain.NotifyTypeWebhook, now)
	upd.ID = id
	upd.Name = "renamed"
	upd.URLEncrypted = "enc(updated)"
	upd.KeyID = "key-3"
	upd.OnHealthFailure = false
	upd.UpdatedAt = now.Add(time.Minute)
	if err := repo.UpdateNotification(ctx, db, upd); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.GetNotification(ctx, db, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "renamed" || got.URLEncrypted != "enc(updated)" || got.KeyID != "key-3" || got.OnHealthFailure {
		t.Errorf("update not applied: %+v", got)
	}

	if err := repo.SetNotificationEnabled(ctx, db, id, false, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	got, _ = repo.GetNotification(ctx, db, id)
	if got.Enabled {
		t.Error("still enabled after disable")
	}

	list, err := repo.ListNotifications(ctx, db)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %d (err %v), want 1", len(list), err)
	}

	if err := repo.DeleteNotification(ctx, db, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetNotification(ctx, db, id); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("get after delete err = %v, want ErrNotFound", err)
	}
}

func TestNotificationNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openMigrated(t, ":memory:")
	repo := database.Notifications{}
	if err := repo.DeleteNotification(ctx, db, 404); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("delete missing err = %v, want ErrNotFound", err)
	}
	if err := repo.SetNotificationEnabled(ctx, db, 404, true, time.Now()); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("enable missing err = %v, want ErrNotFound", err)
	}
}
