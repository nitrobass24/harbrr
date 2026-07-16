package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/announce"
	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/secrets"
)

// TestAnnounceOrigin covers the /dl base origin choice for an announce push: the
// configured server.external_url wins when set (issue #10's drift-cutting note),
// otherwise the connection's own stored harbrr URL, trailing slash trimmed.
func TestAnnounceOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		externalOrigin string
		harbrrURL      string
		want           string
	}{
		{"external_url set wins over the connection's URL", "https://harbrr.example.com", "http://10.0.0.5:7478/", "https://harbrr.example.com"},
		{"external_url unset falls back to the connection's URL", "", "http://10.0.0.5:7478/", "http://10.0.0.5:7478"},
		{"neither set", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := announceOrigin(tt.externalOrigin, tt.harbrrURL); got != tt.want {
				t.Errorf("announceOrigin(%q, %q) = %q, want %q", tt.externalOrigin, tt.harbrrURL, got, tt.want)
			}
		})
	}
}

// countingTarget counts announces across goroutines for the sink test.
type countingTarget struct{ n *atomic.Int64 }

func (c countingTarget) Announce(context.Context, announce.Release) (announce.Result, error) {
	c.n.Add(1)
	return announce.Result{}, nil
}

// TestAnnounceSinkSkipsUsenet pins #231: every announce target today (qui cross-seed,
// cross-seed v6) is torrent-only, so a usenet instance's RSS fill must not fan out a
// push, while a torrent instance's still does.
func TestAnnounceSinkSkipsUsenet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}

	var announced atomic.Int64
	svc := announce.NewService(db, auth.NewService(db), kr, func(domain.AnnounceConnection, string) (announce.Target, error) {
		return countingTarget{n: &announced}, nil
	}, zerolog.Nop())
	if _, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	}); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	instances := database.Instances{}
	now := time.Now().UTC()
	mk := func(slug, protocol string) int64 {
		id, ierr := instances.Insert(ctx, db, domain.IndexerInstance{
			Slug: slug, DefinitionID: slug, Name: slug, Protocol: protocol,
			Enabled: true, CreatedAt: now, UpdatedAt: now,
		})
		if ierr != nil {
			t.Fatalf("insert %s: %v", slug, ierr)
		}
		return id
	}
	usenetID := mk("dog", "usenet")
	torrentID := mk("tl", "torrent")

	sink := newAnnounceSink(svc, db, kr, "", "", zerolog.Nop())
	rel := []*normalizer.Release{{Title: "X", Link: "https://t.example/dl?passkey=p"}}

	sink(ctx, usenetID, rel)
	sink(ctx, torrentID, rel)

	// Pushes are async (detached goroutine per fill): wait for the torrent push,
	// then give a wrong usenet push a beat to surface before asserting the total.
	deadline := time.Now().Add(5 * time.Second)
	for announced.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the torrent push")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if n := announced.Load(); n != 1 {
		t.Errorf("announced %d releases, want exactly 1 (torrent instance only)", n)
	}
}
