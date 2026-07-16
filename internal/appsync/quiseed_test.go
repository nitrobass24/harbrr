package appsync

import (
	"context"
	"errors"
	"testing"

	"github.com/autobrr/harbrr/internal/domain"
)

// TestQuiSeedReturnsDecryptedCredentials covers the happy path: a qui app-connection's
// base URL, decrypted API key, and harbrr URL are returned as-is for reuse by the
// announce-connection seeding endpoint (#72).
func TestQuiSeedReturnsDecryptedCredentials(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	qui, err := f.svc.CreateConnection(ctx, CreateConnectionParams{
		Name: "qui", Kind: domain.AppKindQui, BaseURL: "http://qui:7476",
		APIKey: "qui-secret", HarbrrURL: "http://harbrr:7478",
	})
	if err != nil {
		t.Fatalf("CreateConnection qui: %v", err)
	}

	seed, err := f.svc.QuiSeed(ctx, qui.ID)
	if err != nil {
		t.Fatalf("QuiSeed: %v", err)
	}
	want := QuiSeedResult{Name: "qui", BaseURL: "http://qui:7476", APIKey: "qui-secret", HarbrrURL: "http://harbrr:7478"}
	if seed != want {
		t.Errorf("QuiSeed = %+v, want %+v", seed, want)
	}
}

// TestQuiSeedRejectsNonQuiKind covers the guard: seeding from a non-qui app-connection
// (Sonarr, in the shared fixture) is refused rather than silently reusing its key as a
// qui credential.
func TestQuiSeedRejectsNonQuiKind(t *testing.T) {
	t.Parallel()
	f := newSyncFixture(t)
	ctx := context.Background()

	if _, err := f.svc.QuiSeed(ctx, f.conn.ID); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("QuiSeed on sonarr connection = %v, want domain.ErrInvalid", err)
	}
}
