package proxy

import (
	"context"
	"errors"
	"strings"
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

func TestCreateEncryptsURLAndRoundTrips(t *testing.T) {
	t.Parallel()
	svc, kr := newService(t)
	ctx := context.Background()

	const rawURL = "socks5://user:pass@10.0.0.9:1080"
	p, err := svc.Create(ctx, CreateParams{Name: "home", Type: domain.ProxyTypeSOCKS5, URL: rawURL})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Stored ciphertext must not be the plaintext, and must decrypt back to it under
	// the proxy's own id (the AAD).
	if p.URLEncrypted == rawURL || p.URLEncrypted == "" {
		t.Fatalf("URL not encrypted at rest: %q", p.URLEncrypted)
	}
	got, err := kr.Decrypt(p.ID, domain.ProxySecretURL, p.URLEncrypted)
	if err != nil || got != rawURL {
		t.Fatalf("decrypt = %q, %v; want %q", got, err, rawURL)
	}

	// Update rotates the URL under the same id.
	newURL := "http://proxy.internal:3128"
	newType := domain.ProxyTypeHTTP
	if err := svc.Update(ctx, p.ID, UpdateParams{Type: &newType, URL: &newURL}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after, _ := svc.Get(ctx, p.ID)
	dec, _ := kr.Decrypt(after.ID, domain.ProxySecretURL, after.URLEncrypted)
	if after.Type != newType || dec != newURL {
		t.Fatalf("after update: type %q url %q", after.Type, dec)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	ctx := context.Background()

	cases := []CreateParams{
		{Name: "", Type: domain.ProxyTypeHTTP, URL: "http://h"},
		{Name: "x", Type: "ftp", URL: "http://h"},
		{Name: "x", Type: domain.ProxyTypeHTTP, URL: ""},
		{Name: "x", Type: domain.ProxyTypeHTTP, URL: "::::"},
	}
	for _, c := range cases {
		if _, err := svc.Create(ctx, c); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%+v) err = %v, want ErrInvalid", c, err)
		}
	}
}

// TestValidateRejectsSchemeMismatch covers U11-F1: a scheme-less URL and a
// type/scheme cross-family mismatch must be rejected at save (ErrInvalid → 400),
// not accepted and left to fail at search time on every referencing indexer.
func TestValidateRejectsSchemeMismatch(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	ctx := context.Background()

	cases := []struct {
		name   string
		params CreateParams
	}{
		{"socks5 type with http scheme", CreateParams{Name: "x", Type: domain.ProxyTypeSOCKS5, URL: "http://127.0.0.1:1080"}},
		{"http type with socks5 scheme", CreateParams{Name: "x", Type: domain.ProxyTypeHTTP, URL: "socks5://127.0.0.1:1080"}},
		{"scheme-less url", CreateParams{Name: "x", Type: domain.ProxyTypeHTTP, URL: "//127.0.0.1:3128"}},
	}
	for _, c := range cases {
		if _, err := svc.Create(ctx, c.params); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: Create err = %v, want ErrInvalid", c.name, err)
		}
	}
}

// TestValidateAcceptsSameFamilyScheme confirms schemes in the same transport
// family as the type are accepted: http↔https (both http.ProxyURL) and
// socks5↔socks5h (both proxy.FromURL) are interchangeable.
func TestValidateAcceptsSameFamilyScheme(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	ctx := context.Background()

	cases := []CreateParams{
		{Name: "a", Type: domain.ProxyTypeHTTP, URL: "https://proxy:8080"},        // http type, https scheme
		{Name: "b", Type: domain.ProxyTypeSOCKS5, URL: "socks5h://10.0.0.9:1080"}, // socks5 type, socks5h scheme
		{Name: "c", Type: domain.ProxyTypeSOCKS5, URL: "socks5://10.0.0.9:1080"},
		{Name: "d", Type: domain.ProxyTypeHTTP, URL: "http://proxy:3128"},
	}
	for _, c := range cases {
		if _, err := svc.Create(ctx, c); err != nil {
			t.Errorf("Create(%s) err = %v, want nil", c.Name, err)
		}
	}
}

// TestValidateErrorHidesURLSecret asserts the rejection error carries only the
// safe scheme token and type — never the URL's userinfo/host (it can embed
// user:pass or a passkey).
func TestValidateErrorHidesURLSecret(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	ctx := context.Background()

	const secret = "s3cr3t-passkey"
	const host = "10.9.8.7:1080"
	// Cross-family (socks5 type, http scheme) so validate rejects it.
	_, err := svc.Create(ctx, CreateParams{
		Name: "x", Type: domain.ProxyTypeSOCKS5,
		URL: "http://user:" + secret + "@" + host,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create err = %v, want ErrInvalid", err)
	}
	if msg := err.Error(); strings.Contains(msg, secret) || strings.Contains(msg, host) {
		t.Errorf("error message leaks URL credentials/host: %q", msg)
	}
}

func TestUpdateKeepsURLWhenOmitted(t *testing.T) {
	t.Parallel()
	svc, kr := newService(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, CreateParams{Name: "home", Type: domain.ProxyTypeHTTP, URL: "http://a:3128"})

	name := "renamed"
	if err := svc.Update(ctx, p.ID, UpdateParams{Name: &name}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	after, _ := svc.Get(ctx, p.ID)
	dec, _ := kr.Decrypt(after.ID, domain.ProxySecretURL, after.URLEncrypted)
	if after.Name != "renamed" || dec != "http://a:3128" {
		t.Fatalf("after name-only update: name %q url %q (url should be unchanged)", after.Name, dec)
	}
}

// TestUpdateTypeOnlyRevalidatesStoredURL: flipping the type without re-sending the
// URL must re-check the stored URL against the new type, so a cross-family flip
// (http url, socks5 type) is rejected at save rather than failing at search.
func TestUpdateTypeOnlyRevalidatesStoredURL(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, CreateParams{Name: "home", Type: domain.ProxyTypeHTTP, URL: "http://a:3128"})

	badType := domain.ProxyTypeSOCKS5
	if err := svc.Update(ctx, p.ID, UpdateParams{Type: &badType}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("type-only flip to socks5 over an http url: err = %v, want ErrInvalid", err)
	}
	// A same-family type change over the stored URL is still fine.
	okType := domain.ProxyTypeHTTPS
	if err := svc.Update(ctx, p.ID, UpdateParams{Type: &okType}); err != nil {
		t.Fatalf("type-only flip to https over an http url: err = %v, want nil (same family)", err)
	}
}
