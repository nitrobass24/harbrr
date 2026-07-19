package auth_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbinterface"
	"github.com/autobrr/harbrr/internal/domain"
)

type fastPasswordHasher struct{}

func (fastPasswordHasher) HashPassword(password string) (string, error) {
	return fastPasswordHash(password), nil
}

func (fastPasswordHasher) VerifyPassword(password, encoded string) (bool, error) {
	return encoded == fastPasswordHash(password), nil
}

func fastPasswordHash(password string) string {
	return fmt.Sprintf("test-sha256:%x", sha256.Sum256([]byte(password)))
}

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

	// Construct over a dbinterface.Querier-typed variable (not the concrete
	// *database.DB) so this seam can't silently regress to requiring the
	// concrete storage type, matching the other services (notify, proxy,
	// appsync, announce) that already depend on the Querier interface.
	var q dbinterface.Querier = db
	return auth.NewServiceWithPasswordHasher(q, fastPasswordHasher{})
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

func TestAPIKeyTouchFlush(t *testing.T) {
	t.Parallel()
	s := newService(t)
	ctx := context.Background()

	plaintext, k, err := s.MintAPIKey(ctx, "smoke")
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}

	// Validation buffers the touch in memory — nothing lands until a flush.
	if _, err := s.ValidateAPIKey(ctx, plaintext); err != nil {
		t.Fatalf("ValidateAPIKey: %v", err)
	}
	if got := listOneKey(t, s); got.LastUsedAt != nil {
		t.Errorf("LastUsedAt before flush = %v, want nil", got.LastUsedAt)
	}

	if err := s.FlushAPIKeyTouches(ctx); err != nil {
		t.Fatalf("FlushAPIKeyTouches: %v", err)
	}
	if got := listOneKey(t, s); got.LastUsedAt == nil {
		t.Error("LastUsedAt after flush = nil, want set")
	}

	// A failed validation buffers nothing; a drained buffer flushes clean.
	if _, err := s.ValidateAPIKey(ctx, "bogus"); !errors.Is(err, auth.ErrInvalidAPIKey) {
		t.Fatalf("ValidateAPIKey(bogus) err = %v, want ErrInvalidAPIKey", err)
	}
	if err := s.FlushAPIKeyTouches(ctx); err != nil {
		t.Errorf("FlushAPIKeyTouches on empty buffer: %v", err)
	}

	// A key revoked between use and flush is not an error (the UPDATE misses).
	if _, err := s.ValidateAPIKey(ctx, plaintext); err != nil {
		t.Fatalf("ValidateAPIKey: %v", err)
	}
	if err := s.RevokeAPIKey(ctx, k.ID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	if err := s.FlushAPIKeyTouches(ctx); err != nil {
		t.Errorf("FlushAPIKeyTouches after revoke: %v", err)
	}
}

func listOneKey(t *testing.T, s *auth.Service) domain.APIKey {
	t.Helper()
	keys, err := s.ListAPIKeys(context.Background())
	if err != nil || len(keys) != 1 {
		t.Fatalf("ListAPIKeys = (%d,%v), want 1", len(keys), err)
	}
	return keys[0]
}

func TestChangePassword(t *testing.T) {
	t.Parallel()
	s := newService(t)
	ctx := context.Background()

	// No admin yet -> a wrong-credentials result (nothing to verify against).
	if err := s.ChangePassword(ctx, "whatever", "longenoughpw"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("ChangePassword before setup err = %v, want ErrInvalidCredentials", err)
	}

	if _, err := s.Setup(ctx, "admin", "correct-horse"); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Wrong current password -> ErrInvalidCredentials.
	if err := s.ChangePassword(ctx, "wrong", "brand-new-passphrase"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("ChangePassword wrong current err = %v, want ErrInvalidCredentials", err)
	}
	// Weak new password -> ErrWeakPassword (checked only after the current verifies).
	if err := s.ChangePassword(ctx, "correct-horse", "short"); !errors.Is(err, auth.ErrWeakPassword) {
		t.Errorf("ChangePassword weak new err = %v, want ErrWeakPassword", err)
	}

	// Valid change rotates the password: the old one stops working, the new one logs in.
	if err := s.ChangePassword(ctx, "correct-horse", "brand-new-passphrase"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := s.Login(ctx, "admin", "correct-horse"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("old password still logs in err = %v, want ErrInvalidCredentials", err)
	}
	if _, err := s.Login(ctx, "admin", "brand-new-passphrase"); err != nil {
		t.Errorf("new password login: %v", err)
	}
}
