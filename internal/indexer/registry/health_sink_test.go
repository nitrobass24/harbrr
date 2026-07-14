package registry_test

import (
	"context"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
)

// healthArgs is one captured OnHealthEvent call.
type healthArgs struct {
	indexer, kind, detail string
}

// recordingSink captures the health events the registry hands its sink. The registry
// calls OnHealthEvent synchronously from recordHealth, so a mutex-guarded slice is
// enough (no async race).
type recordingSink struct {
	mu   sync.Mutex
	seen []healthArgs
}

func (s *recordingSink) OnHealthEvent(_ context.Context, indexer, kind, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, healthArgs{indexer: indexer, kind: kind, detail: detail})
}

func (s *recordingSink) events() []healthArgs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]healthArgs(nil), s.seen...)
}

// newRegistryWithSink builds a registry with the given doer and health sink over an
// in-memory DB and the testtracker drop-in def (mirrors newRegistry).
func newRegistryWithSink(t *testing.T, doer search.Doer, sink registry.HealthSink) *registry.Registry {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dropin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dropin, "testtracker.yml"), []byte(defYAML), 0o600); err != nil {
		t.Fatalf("write def: %v", err)
	}
	keyring, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testHexKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("open keyring: %v", err)
	}
	return registry.New(
		db, loader.New(dropin), keyring, nil,
		registry.WithClock(fixedClock),
		registry.WithDoerFactory(func(registry.ClientParams) (search.Doer, error) { return doer, nil }),
		registry.WithHealthSink(sink),
	)
}

// TestHealthSinkNotifiedOnClassifiedFailure proves a classified search failure both
// records a health event and notifies the sink with the slug, kind, and scrubbed detail.
func TestHealthSinkNotifiedOnClassifiedFailure(t *testing.T) {
	t.Parallel()
	sink := &recordingSink{}
	reg := newRegistryWithSink(t, statusDoer{status: stdhttp.StatusServiceUnavailable}, sink)
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); err == nil {
		t.Fatal("Search returned nil error, want a classified failure")
	}

	got := sink.events()
	if len(got) != 1 {
		t.Fatalf("sink saw %d events, want 1", len(got))
	}
	if got[0].indexer != "tt" || got[0].kind != domain.HealthRateLimited {
		t.Errorf("sink event = %+v, want indexer=tt kind=rate_limited", got[0])
	}
	if got[0].detail == "" {
		t.Error("sink event detail is empty, want the scrubbed failure detail")
	}
}
