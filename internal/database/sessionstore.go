package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database/dbinterface"
)

// SessionStore is a driver-agnostic alexedwards/scs/v2 store over the sessions
// table. The official scs/sqlite3store imports the cgo mattn driver, which would
// break the pure-Go cross-build; this store works on any database/sql backend.
// expiry is stored as Unix seconds (a REAL column) — our own convention.
type SessionStore struct {
	db dbinterface.Execer
}

// NewSessionStore builds a session store over the given handle.
func NewSessionStore(db dbinterface.Execer) *SessionStore { return &SessionStore{db: db} }

// FindCtx returns the data for an unexpired session token.
func (s *SessionStore) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT data FROM sessions WHERE token = ? AND expiry > ?`, token, nowUnix()).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("database: find session: %w", err)
	}
	return data, true, nil
}

// CommitCtx inserts or updates a session token's data and expiry.
func (s *SessionStore) CommitCtx(ctx context.Context, token string, b []byte, expiry time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token, data, expiry) VALUES (?, ?, ?)
		 ON CONFLICT(token) DO UPDATE SET data = excluded.data, expiry = excluded.expiry`,
		token, b, unixSeconds(expiry))
	if err != nil {
		return fmt.Errorf("database: commit session: %w", err)
	}
	return nil
}

// DeleteCtx removes a session token (a no-op if absent).
func (s *SessionStore) DeleteCtx(ctx context.Context, token string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token); err != nil {
		return fmt.Errorf("database: delete session: %w", err)
	}
	return nil
}

// Find implements scs.Store (delegates to the context variant).
func (s *SessionStore) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

// Commit implements scs.Store.
func (s *SessionStore) Commit(token string, b []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, b, expiry)
}

// Delete implements scs.Store.
func (s *SessionStore) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}

// DeleteExpired removes expired sessions; the server schedules it periodically so
// dead rows do not accumulate (Find already filters them out).
func (s *SessionStore) DeleteExpired(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expiry <= ?`, nowUnix()); err != nil {
		return fmt.Errorf("database: delete expired sessions: %w", err)
	}
	return nil
}

// unixSeconds renders a time as fractional Unix seconds for the expiry column.
func unixSeconds(t time.Time) float64 {
	return float64(t.UnixNano()) / float64(time.Second)
}

// nowUnix is the current time in fractional Unix seconds.
func nowUnix() float64 { return unixSeconds(time.Now()) }
