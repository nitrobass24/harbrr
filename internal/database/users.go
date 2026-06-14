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

// Users is the SQLite repository for the admin account. Stateless; every method
// takes an Execer.
type Users struct{}

// Count returns the number of users (first-run setup is gated on this being 0).
func (Users) Count(ctx context.Context, q dbinterface.Execer) (int, error) {
	var n int
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil {
		return 0, fmt.Errorf("database: count users: %w", err)
	}
	return n, nil
}

// Create inserts a user and returns its id.
func (Users) Create(ctx context.Context, q dbinterface.Execer, u domain.User) (int64, error) {
	res, err := q.ExecContext(
		ctx,
		q.Rebind(`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?)`),
		u.Username, u.PasswordHash,
		u.CreatedAt.UTC().Format(timeLayout), u.UpdatedAt.UTC().Format(timeLayout),
	)
	if err != nil {
		return 0, fmt.Errorf("database: insert user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("database: user last insert id: %w", err)
	}
	return id, nil
}

// GetByUsername returns the user with the given username, or ErrNotFound.
func (Users) GetByUsername(ctx context.Context, q dbinterface.Execer, username string) (domain.User, error) {
	var (
		u                    domain.User
		createdAt, updatedAt string
	)
	err := q.QueryRowContext(ctx,
		q.Rebind(`SELECT id, username, password_hash, created_at, updated_at FROM users WHERE username = ?`), username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, fmt.Errorf("user %q: %w", username, ErrNotFound)
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("database: get user: %w", err)
	}
	u.CreatedAt = parseTime(createdAt)
	u.UpdatedAt = parseTime(updatedAt)
	return u, nil
}

// UpdatePassword sets a user's password hash by id.
func (Users) UpdatePassword(ctx context.Context, q dbinterface.Execer, id int64, hash string, updatedAt time.Time) error {
	_, err := q.ExecContext(ctx,
		q.Rebind(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`),
		hash, updatedAt.UTC().Format(timeLayout), id)
	if err != nil {
		return fmt.Errorf("database: update password: %w", err)
	}
	return nil
}
