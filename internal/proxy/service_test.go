package proxy

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database/dbtest"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func newService(t *testing.T) (*Service, *secrets.Keyring) {
	t.Helper()
	db := dbtest.OpenMigrated(t)
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return NewService(db, kr), kr
}

func TestCreateEncryptsPasswordAndRoundTrips(t *testing.T) {
	t.Parallel()
	svc, kr := newService(t)
	ctx := context.Background()

	p, err := svc.Create(ctx, CreateParams{
		Name: "home", Type: domain.ProxyTypeSOCKS5, Host: "10.0.0.9", Port: 1080, Username: "user", Password: "pass",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.Host != "10.0.0.9" || p.Port != 1080 || p.Username != "user" {
		t.Fatalf("structured fields not stored plainly: %+v", p)
	}

	// Stored ciphertext must not be the plaintext, and must decrypt back to it
	// under the proxy's own id (the AAD).
	if p.PasswordEncrypted == "pass" || p.PasswordEncrypted == "" {
		t.Fatalf("password not encrypted at rest: %q", p.PasswordEncrypted)
	}
	got, err := kr.Decrypt(p.ID, domain.ProxySecretPassword, p.PasswordEncrypted)
	if err != nil || got != "pass" {
		t.Fatalf("decrypt = %q, %v; want %q", got, err, "pass")
	}

	// Update rotates the password under the same id.
	newPass := "rotated"
	newType := domain.ProxyTypeHTTP
	if err := svc.Update(ctx, p.ID, UpdateParams{Type: &newType, Password: &newPass}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after, _ := svc.Get(ctx, p.ID)
	dec, _ := kr.Decrypt(after.ID, domain.ProxySecretPassword, after.PasswordEncrypted)
	if after.Type != newType || dec != newPass {
		t.Fatalf("after update: type %q password %q", after.Type, dec)
	}
}

func TestCreateAllowsCredentialFreeProxy(t *testing.T) {
	t.Parallel()
	svc, kr := newService(t)
	ctx := context.Background()

	p, err := svc.Create(ctx, CreateParams{Name: "open", Type: domain.ProxyTypeSOCKS5, Host: "10.0.0.9", Port: 1080})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dec, err := kr.Decrypt(p.ID, domain.ProxySecretPassword, p.PasswordEncrypted)
	if err != nil || dec != "" {
		t.Fatalf("decrypt = %q, %v; want empty password", dec, err)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	ctx := context.Background()

	cases := []CreateParams{
		{Name: "", Type: domain.ProxyTypeHTTP, Host: "h", Port: 8080},
		{Name: "x", Type: "ftp", Host: "h", Port: 8080},
		{Name: "x", Type: domain.ProxyTypeHTTP, Host: "", Port: 8080},
		{Name: "x", Type: domain.ProxyTypeHTTP, Host: "h", Port: 0},
		{Name: "x", Type: domain.ProxyTypeHTTP, Host: "h", Port: 70000},
	}
	for _, c := range cases {
		if _, err := svc.Create(ctx, c); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%+v) err = %v, want ErrInvalid", c, err)
		}
	}
}

func TestUpdateKeepsPasswordWhenOmitted(t *testing.T) {
	t.Parallel()
	svc, kr := newService(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, CreateParams{Name: "home", Type: domain.ProxyTypeHTTP, Host: "a", Port: 3128, Password: "orig"})

	name := "renamed"
	if err := svc.Update(ctx, p.ID, UpdateParams{Name: &name}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after, _ := svc.Get(ctx, p.ID)
	dec, _ := kr.Decrypt(after.ID, domain.ProxySecretPassword, after.PasswordEncrypted)
	if after.Name != "renamed" || dec != "orig" {
		t.Fatalf("after name-only update: name %q password %q (password should be unchanged)", after.Name, dec)
	}
}

// TestUpdateEmptyPasswordClearsCredential asserts Password is nil = keep,
// non-nil (even "") = rotate — an explicit empty string clears a stored
// password, distinct from omitting the field.
func TestUpdateEmptyPasswordClearsCredential(t *testing.T) {
	t.Parallel()
	svc, kr := newService(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, CreateParams{Name: "home", Type: domain.ProxyTypeHTTP, Host: "a", Port: 3128, Password: "orig"})

	empty := ""
	if err := svc.Update(ctx, p.ID, UpdateParams{Password: &empty}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after, _ := svc.Get(ctx, p.ID)
	dec, err := kr.Decrypt(after.ID, domain.ProxySecretPassword, after.PasswordEncrypted)
	if err != nil || dec != "" {
		t.Fatalf("after clearing password: %q, %v", dec, err)
	}
}

func TestUpdateStructuredFields(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, CreateParams{Name: "home", Type: domain.ProxyTypeHTTP, Host: "a", Port: 3128})

	host, username := "b", "carol"
	port := 8080
	if err := svc.Update(ctx, p.ID, UpdateParams{Host: &host, Port: &port, Username: &username}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after, _ := svc.Get(ctx, p.ID)
	if after.Host != "b" || after.Port != 8080 || after.Username != "carol" {
		t.Fatalf("after structured update: %+v", after)
	}
}
