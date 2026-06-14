package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database"
)

func newService(t *testing.T) *auth.Service {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return auth.NewService(db)
}

func TestSetupAndLogin(t *testing.T) {
	t.Parallel()
	s := newService(t)
	ctx := context.Background()

	if done, err := s.SetupComplete(ctx); err != nil || done {
		t.Fatalf("SetupComplete before = (%v,%v), want (false,nil)", done, err)
	}

	if _, err := s.Setup(ctx, "", "longenoughpw"); !errors.Is(err, auth.ErrInvalidInput) {
		t.Errorf("Setup empty username err = %v, want ErrInvalidInput", err)
	}
	if _, err := s.Setup(ctx, "admin", "short"); !errors.Is(err, auth.ErrWeakPassword) {
		t.Errorf("Setup weak password err = %v, want ErrWeakPassword", err)
	}

	if _, err := s.Setup(ctx, "admin", "correct-horse"); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if done, err := s.SetupComplete(ctx); err != nil || !done {
		t.Fatalf("SetupComplete after = (%v,%v), want (true,nil)", done, err)
	}
	// A second setup is rejected.
	if _, err := s.Setup(ctx, "other", "another-pass"); !errors.Is(err, auth.ErrAlreadySetup) {
		t.Errorf("second Setup err = %v, want ErrAlreadySetup", err)
	}

	// Login: wrong password and wrong user both map to ErrInvalidCredentials.
	if _, err := s.Login(ctx, "admin", "wrong"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("Login wrong password err = %v", err)
	}
	if _, err := s.Login(ctx, "ghost", "correct-horse"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("Login unknown user err = %v", err)
	}
	u, err := s.Login(ctx, "admin", "correct-horse")
	if err != nil || u.Username != "admin" {
		t.Errorf("Login = (%+v,%v), want admin", u, err)
	}
}

func TestAPIKeyLifecycle(t *testing.T) {
	t.Parallel()
	s := newService(t)
	ctx := context.Background()

	plaintext, k, err := s.MintAPIKey(ctx, "sonarr")
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	if plaintext == "" || k.KeyHash == "" || plaintext == k.KeyHash {
		t.Error("minted key should return a plaintext distinct from the stored hash")
	}

	got, err := s.ValidateAPIKey(ctx, plaintext)
	if err != nil || got.ID != k.ID {
		t.Errorf("ValidateAPIKey = (%+v,%v), want id %d", got, err, k.ID)
	}
	if _, err := s.ValidateAPIKey(ctx, "bogus"); !errors.Is(err, auth.ErrInvalidAPIKey) {
		t.Errorf("ValidateAPIKey(bogus) err = %v, want ErrInvalidAPIKey", err)
	}
	if _, err := s.ValidateAPIKey(ctx, ""); !errors.Is(err, auth.ErrInvalidAPIKey) {
		t.Errorf("ValidateAPIKey(empty) err = %v, want ErrInvalidAPIKey", err)
	}

	keys, err := s.ListAPIKeys(ctx)
	if err != nil || len(keys) != 1 {
		t.Fatalf("ListAPIKeys = (%d,%v), want 1", len(keys), err)
	}

	if err := s.RevokeAPIKey(ctx, k.ID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	if _, err := s.ValidateAPIKey(ctx, plaintext); !errors.Is(err, auth.ErrInvalidAPIKey) {
		t.Errorf("ValidateAPIKey after revoke err = %v, want ErrInvalidAPIKey", err)
	}
	if err := s.RevokeAPIKey(ctx, k.ID); !errors.Is(err, database.ErrNotFound) {
		t.Errorf("RevokeAPIKey missing err = %v, want ErrNotFound", err)
	}
}
