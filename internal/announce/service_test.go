package announce_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/announce"
	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// fakeTarget records the releases it was asked to announce and a fixed match verdict.
type fakeTarget struct {
	got     []announce.Release
	matched bool
	err     error
}

func (f *fakeTarget) Announce(_ context.Context, rel announce.Release) (announce.Result, error) {
	f.got = append(f.got, rel)
	if f.err != nil {
		return announce.Result{}, f.err
	}
	return announce.Result{Matched: f.matched}, nil
}

func newService(t *testing.T, factory announce.TargetFactory) (*announce.Service, *database.DB) {
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
	return announce.NewService(db, auth.NewService(db), kr, factory, zerolog.Nop()), db
}

func TestServiceCreateGetDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) {
		return &fakeTarget{}, nil
	})

	conn, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "qui_secret", HarbrrURL: "http://h:8787",
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	if conn.ID == 0 || conn.HarbrrAPIKeyID == 0 {
		t.Fatalf("connection not fully persisted: %+v", conn)
	}

	got, err := svc.GetConnection(ctx, conn.ID)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if got.APIKeyEncrypted == "" || got.APIKeyEncrypted == "qui_secret" {
		t.Errorf("tool key not encrypted at rest: %q", got.APIKeyEncrypted)
	}

	// the decrypted harbrr key (for the /dl link) round-trips and is not the ciphertext.
	hk, err := svc.HarbrrKey(got)
	if err != nil || hk == "" || hk == got.HarbrrAPIKeyEncrypted {
		t.Errorf("HarbrrKey = %q, err %v", hk, err)
	}

	if err := svc.DeleteConnection(ctx, conn.ID); err != nil {
		t.Fatalf("DeleteConnection: %v", err)
	}
	if _, err := svc.GetConnection(ctx, conn.ID); err == nil {
		t.Error("connection still present after delete")
	}
}

func TestServiceCreateValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) { return &fakeTarget{}, nil })

	// cross-seed v6 requires a harbrr URL (it fetches the /dl link).
	_, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "cs", Kind: domain.AnnounceKindCrossSeedV6, BaseURL: "http://cs:2468", APIKey: "k",
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("missing harbrrUrl err = %v, want ErrInvalid", err)
	}

	// unknown kind is rejected.
	_, err = svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "x", Kind: "sabnzbd", BaseURL: "http://x", APIKey: "k",
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("bad kind err = %v, want ErrInvalid", err)
	}

	// a non-absolute / non-http base URL is rejected (would yield a host-less /dl link).
	_, err = svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "q", Kind: domain.AnnounceKindQui, BaseURL: "qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("relative base url err = %v, want ErrInvalid", err)
	}
}

// TestServiceCreateTrimsURLs proves whitespace-padded URLs are normalized before storage,
// so they can't bypass the (kind, base_url) uniqueness contract or leave a padded /dl URL.
func TestServiceCreateTrimsURLs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) { return &fakeTarget{}, nil })

	conn, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui,
		BaseURL: "  http://qui:7476  ", APIKey: "k", HarbrrURL: " http://h:8787 ",
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	// Re-read through the service so the assertion proves AT-REST normalization, not just
	// the returned struct.
	got, err := svc.GetConnection(ctx, conn.ID)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	if got.BaseURL != "http://qui:7476" || got.HarbrrURL != "http://h:8787" {
		t.Errorf("stored URLs not trimmed: baseURL=%q harbrrURL=%q", got.BaseURL, got.HarbrrURL)
	}

	// A second create with the UNPADDED same base URL must conflict — proof the padded one
	// was stored under its trimmed, canonical value (else the (kind, base_url) uniqueness
	// contract would not catch it).
	_, err = svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui2", Kind: domain.AnnounceKindQui,
		BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate (unpadded) base url err = %v, want ErrConflict", err)
	}
}

// TestServiceHarbrrKeyRejectsRevoked proves HarbrrKey refuses a connection whose minted key
// was revoked out of band (FK SET NULL → id 0), so a dead /dl signing key is never used.
func TestServiceHarbrrKeyRejectsRevoked(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) { return &fakeTarget{}, nil })
	_, err := svc.HarbrrKey(domain.AnnounceConnection{ID: 1, HarbrrAPIKeyID: 0})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("HarbrrKey(revoked) err = %v, want ErrInvalid", err)
	}
}

func TestServicePushFansOutToEnabledOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	targets := map[int64]*fakeTarget{}
	svc, _ := newService(t, func(conn domain.AnnounceConnection, _ string) (announce.Target, error) {
		tgt := &fakeTarget{matched: true}
		targets[conn.ID] = tgt
		return tgt, nil
	})

	enabled, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	})
	if err != nil {
		t.Fatalf("create enabled: %v", err)
	}
	disabled, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "cs", Kind: domain.AnnounceKindCrossSeedV6, BaseURL: "http://cs:2468", APIKey: "k", HarbrrURL: "http://h",
	})
	if err != nil {
		t.Fatalf("create disabled: %v", err)
	}
	if err := svc.SetEnabled(ctx, disabled.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	rel := announce.Release{Name: "X", GUID: "g1"}
	matched := svc.Push(ctx, func(domain.AnnounceConnection) []announce.Release {
		return []announce.Release{rel}
	})

	if matched != 1 {
		t.Errorf("matched = %d, want 1 (only the enabled connection)", matched)
	}
	if got := targets[enabled.ID]; got == nil || len(got.got) != 1 {
		t.Errorf("enabled connection not pushed to: %+v", targets[enabled.ID])
	}
	if got := targets[disabled.ID]; got != nil {
		t.Error("disabled connection should not have a built target")
	}
}

// TestServicePushSwallowsErrors proves a per-connection announce failure is logged, not
// propagated, and never blocks the rest of the fan-out.
func TestServicePushSwallowsErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) {
		return &fakeTarget{err: errors.New("boom")}, nil
	})
	if _, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	matched := svc.Push(ctx, func(domain.AnnounceConnection) []announce.Release {
		return []announce.Release{{Name: "X", GUID: "g1"}}
	})
	if matched != 0 {
		t.Errorf("matched = %d, want 0 (the announce errored)", matched)
	}
}
