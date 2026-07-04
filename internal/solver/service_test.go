package solver

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

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
	return NewService(db, kr), kr
}

func TestCreateDefaultsTypeAndEncrypts(t *testing.T) {
	t.Parallel()
	svc, kr := newService(t)
	ctx := context.Background()

	const rawURL = "http://flaresolverr:8191"
	// Empty type defaults to flaresolverr.
	s, err := svc.Create(ctx, CreateParams{Name: "fs", URL: rawURL, MaxTimeout: 90})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if s.Type != domain.SolverTypeFlaresolverr || s.MaxTimeout != 90 {
		t.Fatalf("Create = type %q maxTimeout %d", s.Type, s.MaxTimeout)
	}
	got, err := kr.Decrypt(s.ID, domain.SolverSecretURL, s.URLEncrypted)
	if err != nil || got != rawURL {
		t.Fatalf("decrypt = %q, %v; want %q", got, err, rawURL)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	ctx := context.Background()

	cases := []CreateParams{
		{Name: "", URL: "http://h"},
		{Name: "x", Type: "captcha-solver", URL: "http://h"},
		{Name: "x", URL: ""},
		{Name: "x", URL: "http://h", MaxTimeout: -1},
	}
	for _, c := range cases {
		if _, err := svc.Create(ctx, c); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%+v) err = %v, want ErrInvalid", c, err)
		}
	}
}
