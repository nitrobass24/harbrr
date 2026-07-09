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
		// Over the cap: the solve-time reset would silently drop these to the 60s
		// default, so reject them at save instead of saving a lie.
		{Name: "x", URL: "http://h", MaxTimeout: 300},
		{Name: "x", URL: "http://h", MaxTimeout: domain.FlareMaxTimeoutCapSeconds + 1},
	}
	for _, c := range cases {
		if _, err := svc.Create(ctx, c); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%+v) err = %v, want ErrInvalid", c, err)
		}
	}
}

// TestMaxTimeoutBounds pins the accepted/rejected boundary of the per-solve
// budget on both the create and update paths (validate is the shared gate).
func TestMaxTimeoutBounds(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	ctx := context.Background()

	const rawURL = "http://flaresolverr:8191"
	cases := []struct {
		name       string
		maxTimeout int
		wantErr    bool
	}{
		{"at cap accepted", domain.FlareMaxTimeoutCapSeconds, false},
		{"just over cap rejected", domain.FlareMaxTimeoutCapSeconds + 1, true},
		{"zero uses default", 0, false},
		{"negative rejected", -1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			// Create path.
			_, err := svc.Create(ctx, CreateParams{Name: "fs", URL: rawURL, MaxTimeout: c.maxTimeout})
			if c.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("Create(maxTimeout=%d) err = %v, want ErrInvalid", c.maxTimeout, err)
				}
			} else if err != nil {
				t.Fatalf("Create(maxTimeout=%d) err = %v, want nil", c.maxTimeout, err)
			}

			// Update path: patch an existing valid solver to the same value.
			base, err := svc.Create(ctx, CreateParams{Name: "base", URL: rawURL, MaxTimeout: 90})
			if err != nil {
				t.Fatalf("Create base: %v", err)
			}
			mt := c.maxTimeout
			err = svc.Update(ctx, base.ID, UpdateParams{MaxTimeout: &mt})
			if c.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("Update(maxTimeout=%d) err = %v, want ErrInvalid", c.maxTimeout, err)
				}
			} else if err != nil {
				t.Fatalf("Update(maxTimeout=%d) err = %v, want nil", c.maxTimeout, err)
			}
		})
	}
}

// TestUpdateKeepsURLWhenOmitted covers the U11-F5 silent-revert class: a name-only
// PATCH (URL omitted) must leave the stored encrypted URL untouched. It exercises the
// placeholder-URL path in Update (validateURL = "unchanged://ok"), where p.URL == nil
// skips both the cross-check and the re-encrypt, so a rename can't wipe the endpoint.
func TestUpdateKeepsURLWhenOmitted(t *testing.T) {
	t.Parallel()
	svc, kr := newService(t)
	ctx := context.Background()

	const rawURL = "http://flaresolverr:8191"
	s, err := svc.Create(ctx, CreateParams{Name: "fs", URL: rawURL, MaxTimeout: 90})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	name := "renamed"
	if err := svc.Update(ctx, s.ID, UpdateParams{Name: &name}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after, err := svc.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	dec, err := kr.Decrypt(after.ID, domain.SolverSecretURL, after.URLEncrypted)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	// Compare as a bool so the endpoint value never lands in test output/logs.
	if after.Name != "renamed" || dec != rawURL {
		t.Fatalf("after name-only update: name %q urlUnchanged=%v (url must be unchanged)", after.Name, dec == rawURL)
	}
}

// TestUpdateRotatesURL round-trips a URL rotation: PATCHing the URL re-encrypts the
// new value at rest under the solver's own id (the AAD), and the stored ciphertext is
// not the plaintext.
func TestUpdateRotatesURL(t *testing.T) {
	t.Parallel()
	svc, kr := newService(t)
	ctx := context.Background()

	s, err := svc.Create(ctx, CreateParams{Name: "fs", URL: "http://flaresolverr:8191", MaxTimeout: 90})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newURL := "http://flaresolverr-2:8191"
	if err := svc.Update(ctx, s.ID, UpdateParams{URL: &newURL}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after, err := svc.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.URLEncrypted == newURL || after.URLEncrypted == "" {
		t.Fatalf("rotated URL not encrypted at rest")
	}
	dec, err := kr.Decrypt(after.ID, domain.SolverSecretURL, after.URLEncrypted)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	// Compare as a bool so the endpoint value never lands in test output/logs.
	if dec != newURL {
		t.Fatalf("after rotation: stored url matches new value = %v; want true", dec == newURL)
	}
}

// TestValidateSkipsOmittedMaxTimeout proves the bound check is gated on the field
// being present: a nil maxTimeout (an update patch that omits it) leaves the stored
// value untouched — so an unrelated edit, or an over-cap value imported by
// resourcemigrate (which bypasses validate), doesn't block the update — while a
// supplied over-cap value is still rejected.
func TestValidateSkipsOmittedMaxTimeout(t *testing.T) {
	t.Parallel()
	const url = "http://flaresolverr:8191"
	if err := validate("fs", domain.SolverTypeFlaresolverr, url, nil); err != nil {
		t.Fatalf("nil maxTimeout should skip the bound check, got %v", err)
	}
	over := domain.FlareMaxTimeoutCapSeconds + 1
	if err := validate("fs", domain.SolverTypeFlaresolverr, url, &over); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over-cap maxTimeout should be rejected, got %v", err)
	}
	ok := domain.FlareMaxTimeoutCapSeconds
	if err := validate("fs", domain.SolverTypeFlaresolverr, url, &ok); err != nil {
		t.Fatalf("boundary maxTimeout (%d) should be accepted, got %v", ok, err)
	}
}
