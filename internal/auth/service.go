// Package auth is harbrr's authentication service: first-run admin setup, password
// login (argon2id), and API-key minting/validation (SHA-256). It follows the qui
// model — a single admin, password never recoverable, API keys shown once and
// stored only as hashes.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// Sentinel errors the API maps to status codes (409/401/400).
var (
	// ErrAlreadySetup is returned when setup runs after the admin exists.
	ErrAlreadySetup = errors.New("auth: setup already complete")
	// ErrInvalidCredentials is returned for a bad username/password (no enumeration
	// of which was wrong).
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	// ErrWeakPassword is returned when a password is shorter than the minimum.
	ErrWeakPassword = errors.New("auth: password too short")
	// ErrInvalidInput is returned for malformed setup input (e.g. empty username).
	ErrInvalidInput = errors.New("auth: invalid input")
	// ErrInvalidAPIKey is returned for an unrecognized API key.
	ErrInvalidAPIKey = errors.New("auth: invalid api key")
)

// minPasswordLen is the enforced minimum admin password length (qui's bar).
const minPasswordLen = 8

// Service performs authentication against the SQLite store.
type Service struct {
	db      *database.DB
	users   database.Users
	apiKeys database.APIKeys
	clock   func() time.Time
}

// Option configures the Service.
type Option func(*Service)

// WithClock injects the reference clock (timestamps).
func WithClock(fn func() time.Time) Option {
	return func(s *Service) {
		if fn != nil {
			s.clock = fn
		}
	}
}

// NewService builds the auth service over the database.
func NewService(db *database.DB, opts ...Option) *Service {
	s := &Service{db: db, clock: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// SetupComplete reports whether the admin account exists.
func (s *Service) SetupComplete(ctx context.Context) (bool, error) {
	n, err := s.users.Count(ctx, s.db)
	if err != nil {
		return false, fmt.Errorf("auth: setup status: %w", err)
	}
	return n > 0, nil
}

// Setup creates the single admin. It fails if an admin already exists, the
// username is empty, or the password is too short.
func (s *Service) Setup(ctx context.Context, username, password string) (domain.User, error) {
	n, err := s.users.Count(ctx, s.db)
	if err != nil {
		return domain.User{}, fmt.Errorf("auth: setup count: %w", err)
	}
	if n > 0 {
		return domain.User{}, ErrAlreadySetup
	}
	if username == "" {
		return domain.User{}, fmt.Errorf("%w: username is required", ErrInvalidInput)
	}
	if len(password) < minPasswordLen {
		return domain.User{}, fmt.Errorf("%w: minimum %d characters", ErrWeakPassword, minPasswordLen)
	}

	hash, err := secrets.HashPassword(password)
	if err != nil {
		return domain.User{}, fmt.Errorf("auth: hash password: %w", err)
	}
	now := s.clock()
	u := domain.User{Username: username, PasswordHash: hash, CreatedAt: now, UpdatedAt: now}
	id, err := s.users.Create(ctx, s.db, u)
	if err != nil {
		return domain.User{}, fmt.Errorf("auth: create user: %w", err)
	}
	u.ID = id
	return u, nil
}

// Login verifies a username/password, returning the user or ErrInvalidCredentials.
func (s *Service) Login(ctx context.Context, username, password string) (domain.User, error) {
	u, err := s.users.GetByUsername(ctx, s.db, username)
	if errors.Is(err, database.ErrNotFound) {
		return domain.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("auth: lookup user: %w", err)
	}
	ok, err := secrets.VerifyPassword(password, u.PasswordHash)
	if err != nil {
		return domain.User{}, fmt.Errorf("auth: verify password: %w", err)
	}
	if !ok {
		return domain.User{}, ErrInvalidCredentials
	}
	return u, nil
}

// MintAPIKey creates a new API key and returns the plaintext (shown once) plus the
// stored record (hash only).
func (s *Service) MintAPIKey(ctx context.Context, name string) (string, domain.APIKey, error) {
	plaintext, err := secrets.GenerateAPIKey()
	if err != nil {
		return "", domain.APIKey{}, fmt.Errorf("auth: generate api key: %w", err)
	}
	k := domain.APIKey{Name: name, KeyHash: secrets.HashToken(plaintext), CreatedAt: s.clock()}
	id, err := s.apiKeys.Create(ctx, s.db, k)
	if err != nil {
		return "", domain.APIKey{}, fmt.Errorf("auth: store api key: %w", err)
	}
	k.ID = id
	return plaintext, k, nil
}

// ValidateAPIKey returns the API key matching a presented plaintext, or
// ErrInvalidAPIKey. It is a pure read (no write on the request path).
func (s *Service) ValidateAPIKey(ctx context.Context, plaintext string) (domain.APIKey, error) {
	if plaintext == "" {
		return domain.APIKey{}, ErrInvalidAPIKey
	}
	k, err := s.apiKeys.GetByHash(ctx, s.db, secrets.HashToken(plaintext))
	if errors.Is(err, database.ErrNotFound) {
		return domain.APIKey{}, ErrInvalidAPIKey
	}
	if err != nil {
		return domain.APIKey{}, fmt.Errorf("auth: lookup api key: %w", err)
	}
	return k, nil
}

// ListAPIKeys returns all API keys (hashes only).
func (s *Service) ListAPIKeys(ctx context.Context) ([]domain.APIKey, error) {
	keys, err := s.apiKeys.List(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("auth: list api keys: %w", err)
	}
	return keys, nil
}

// RevokeAPIKey deletes an API key by id (ErrNotFound when absent).
func (s *Service) RevokeAPIKey(ctx context.Context, id int64) error {
	if err := s.apiKeys.Delete(ctx, s.db, id); err != nil {
		return fmt.Errorf("auth: revoke api key: %w", err)
	}
	return nil
}
