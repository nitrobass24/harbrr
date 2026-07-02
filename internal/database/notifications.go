package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

// Notifications is the SQLite repository for notification targets. Like the other
// connection repos it is stateless (every method takes an Execer, so it runs
// standalone or inside a transaction) and stores the opaque (already-encrypted)
// destination URL; encryption is the service's concern.
type Notifications struct{}

// notificationColumns is the full select list, in scan order.
const notificationColumns = `id, name, type, url_encrypted, key_id, enabled,
	on_health_failure, created_at, updated_at`

// InsertNotification writes a notification row and returns its new id. The row is
// inserted with an empty url_encrypted so its id can bind the encryption AAD; the
// service writes the sealed URL back via SetNotificationSecret in the same tx.
func (Notifications) InsertNotification(ctx context.Context, q dbinterface.Execer, n domain.Notification) (int64, error) {
	res, err := q.ExecContext(ctx,
		q.Rebind(`INSERT INTO notifications
			(name, type, url_encrypted, key_id, enabled, on_health_failure, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
		n.Name, n.Type, n.URLEncrypted, n.KeyID, boolToInt(n.Enabled), boolToInt(n.OnHealthFailure),
		n.CreatedAt.UTC().Format(timeLayout), n.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("database: insert notification: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: notification last insert id: %w", err)
	}
	return id, nil
}

// GetNotification returns the notification with the given id, or ErrNotFound.
func (Notifications) GetNotification(ctx context.Context, q dbinterface.Execer, id int64) (domain.Notification, error) {
	row := q.QueryRowContext(ctx,
		q.Rebind(`SELECT `+notificationColumns+` FROM notifications WHERE id = ?`), id)
	n, err := scanNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Notification{}, fmt.Errorf("notification %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return domain.Notification{}, err
	}
	return n, nil
}

// ListNotifications returns all notifications ordered by id.
func (Notifications) ListNotifications(ctx context.Context, q dbinterface.Execer) ([]domain.Notification, error) {
	rows, err := q.QueryContext(ctx, q.Rebind(`SELECT `+notificationColumns+` FROM notifications ORDER BY id`))
	if err != nil {
		return nil, fmt.Errorf("database: list notifications: %w", err)
	}
	defer rows.Close()

	var out []domain.Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("database: iterate notifications: %w", err)
	}
	return out, nil
}

// UpdateNotification writes a notification's mutable fields (name, the re-encrypted
// URL, key_id, on_health_failure) by id, returning ErrNotFound when no row matches.
// The type is immutable (a different sender is a different target) and enabled is
// owned by SetNotificationEnabled, so neither is touched here.
func (Notifications) UpdateNotification(ctx context.Context, q dbinterface.Execer, n domain.Notification) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE notifications SET
			name = ?, url_encrypted = ?, key_id = ?, on_health_failure = ?, updated_at = ?
			WHERE id = ?`),
		n.Name, n.URLEncrypted, n.KeyID, boolToInt(n.OnHealthFailure),
		n.UpdatedAt.UTC().Format(timeLayout), n.ID)
	if err != nil {
		return fmt.Errorf("database: update notification: %w", err)
	}
	return affectedOrNotFoundID(res, n.ID)
}

// SetNotificationSecret writes the encrypted URL column + key_id by id. Notifications
// are inserted in two phases inside one transaction — the row first (to mint the id
// the encryption AAD binds to), then its secret — so a credential is never bound to
// the wrong row.
func (Notifications) SetNotificationSecret(ctx context.Context, q dbinterface.Execer, id int64, urlEncrypted, keyID string) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE notifications SET url_encrypted = ?, key_id = ? WHERE id = ?`),
		urlEncrypted, keyID, id)
	if err != nil {
		return fmt.Errorf("database: set notification secret: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// SetNotificationEnabled toggles a notification's enabled flag by id.
func (Notifications) SetNotificationEnabled(ctx context.Context, q dbinterface.Execer, id int64, enabled bool, updatedAt time.Time) error {
	res, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE notifications SET enabled = ?, updated_at = ? WHERE id = ?`),
		boolToInt(enabled), updatedAt.UTC().Format(timeLayout), id)
	if err != nil {
		return fmt.Errorf("database: set notification enabled: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// DeleteNotification removes a notification by id, returning ErrNotFound when absent.
func (Notifications) DeleteNotification(ctx context.Context, q dbinterface.Execer, id int64) error {
	res, err := q.ExecContext(ctx, q.Rebind(`DELETE FROM notifications WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("database: delete notification: %w", err)
	}
	return affectedOrNotFoundID(res, id)
}

// scanNotification reads one notifications row from a *sql.Row or *sql.Rows.
func scanNotification(s interface{ Scan(...any) error }) (domain.Notification, error) {
	var (
		n                     domain.Notification
		enabled, onHealthFail int
		createdAt, updatedAt  string
	)
	if err := s.Scan(&n.ID, &n.Name, &n.Type, &n.URLEncrypted, &n.KeyID, &enabled,
		&onHealthFail, &createdAt, &updatedAt); err != nil {
		return domain.Notification{}, err //nolint:wrapcheck // sql.ErrNoRows matched by caller; others wrapped there.
	}
	n.Enabled = enabled != 0
	n.OnHealthFailure = onHealthFail != 0
	n.CreatedAt, n.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return n, nil
}
