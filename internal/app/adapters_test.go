package app

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/announce"
	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/registry"
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

// slowTarget simulates a live-but-slow announce target: each call sleeps before counting, so
// a big batch takes real (bounded) wall-clock time to push.
type slowTarget struct {
	n     *atomic.Int64
	sleep time.Duration
}

func (s slowTarget) Announce(context.Context, announce.Release) (announce.Result, error) {
	time.Sleep(s.sleep)
	s.n.Add(1)
	return announce.Result{}, nil
}

// syncBuffer is a mutex-guarded bytes.Buffer: the sink's worker pool logs from multiple
// goroutines concurrently, and bytes.Buffer alone isn't safe for that.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// sinkTestEnv wires a real newAnnounceSink against an in-memory db + one enabled qui
// connection + one torrent instance, for the queue/timeout tests below.
type sinkTestEnv struct {
	sink     registry.AnnounceSink
	instance int64
}

func newSinkTestEnv(t *testing.T, log zerolog.Logger, factory announce.TargetFactory) sinkTestEnv {
	t.Helper()
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
	svc := announce.NewService(db, auth.NewService(db), kr, factory, zerolog.Nop())
	if _, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	}); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	instances := database.Instances{}
	now := time.Now().UTC()
	id, err := instances.Insert(ctx, db, domain.IndexerInstance{
		Slug: "tl", DefinitionID: "tl", Name: "tl", Protocol: "torrent",
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	return sinkTestEnv{sink: newAnnounceSink(svc, db, kr, "", "", log), instance: id}
}

func manyReleases(n int) []*normalizer.Release {
	rels := make([]*normalizer.Release, n)
	for i := range rels {
		rels[i] = &normalizer.Release{Title: fmt.Sprintf("r%d", i), Link: "https://t.example/dl?p=1"}
	}
	return rels
}

// TestAnnounceSinkLargeBatchNoTailLoss pins #232 point 1: a large batch (the production
// evidence was 94 releases) against a slow-but-alive target must complete in full — no tail
// lost to a fixed, too-small batch deadline.
func TestAnnounceSinkLargeBatchNoTailLoss(t *testing.T) {
	t.Parallel()
	var counted atomic.Int64
	env := newSinkTestEnv(t, zerolog.Nop(), func(domain.AnnounceConnection, string) (announce.Target, error) {
		return slowTarget{n: &counted, sleep: 20 * time.Millisecond}, nil
	})

	const n = 100
	env.sink(context.Background(), env.instance, manyReleases(n))

	deadline := time.Now().Add(10 * time.Second)
	for counted.Load() != int64(n) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: pushed %d/%d releases (tail loss)", counted.Load(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAnnounceSinkQueueDeliversUnderBusyBurst pins #232 point 2: when every worker is busy,
// a normal-size burst of additional fills must queue and eventually deliver — not drop —
// once a worker frees up.
func TestAnnounceSinkQueueDeliversUnderBusyBurst(t *testing.T) {
	t.Parallel()
	var counted atomic.Int64
	gate := make(chan struct{})
	target := func(domain.AnnounceConnection, string) (announce.Target, error) {
		return gatedTarget{n: &counted, gate: gate}, nil
	}
	buf := &syncBuffer{}
	env := newSinkTestEnv(t, zerolog.New(buf), target)

	// Occupy all workers; each fill is one release so exactly maxConcurrentAnnouncePushes
	// workers are pinned on the gate.
	for range maxConcurrentAnnouncePushes {
		env.sink(context.Background(), env.instance, manyReleases(1))
	}
	// A modest extra burst, comfortably inside announcePushQueueCapacity: these must queue,
	// not drop.
	const burst = 4
	for range burst {
		env.sink(context.Background(), env.instance, manyReleases(1))
	}
	close(gate) // release every worker so the queued burst gets picked up

	want := int64(maxConcurrentAnnouncePushes + burst)
	deadline := time.Now().Add(5 * time.Second)
	for counted.Load() != want {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: pushed %d/%d (burst starved)", counted.Load(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if strings.Contains(buf.String(), "backpressure") {
		t.Errorf("a normal burst inside queue capacity must not log backpressure/drop: %q", buf.String())
	}
}

// TestAnnounceSinkQueueFullDrops pins #232 point 2's other half: dropping is still
// acceptable, but only once the queue itself is full — and the existing WRN wording is kept.
func TestAnnounceSinkQueueFullDrops(t *testing.T) {
	t.Parallel()
	var counted atomic.Int64
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	target := func(domain.AnnounceConnection, string) (announce.Target, error) {
		return gatedTarget{n: &counted, gate: gate}, nil
	}
	buf := &syncBuffer{}
	env := newSinkTestEnv(t, zerolog.New(buf), target)

	// Fill every worker plus the entire queue buffer — all of these must be accepted
	// (buffered channel sends succeed without waiting for a receiver).
	for range maxConcurrentAnnouncePushes + announcePushQueueCapacity {
		env.sink(context.Background(), env.instance, manyReleases(1))
	}
	if strings.Contains(buf.String(), "backpressure") {
		t.Fatalf("filling exactly worker+queue capacity must not drop yet: %q", buf.String())
	}

	// One more, with the queue completely full and every worker gated: this one must wait
	// out announceQueueEnqueueGrace and then drop with the existing WRN wording.
	env.sink(context.Background(), env.instance, manyReleases(1))
	if !strings.Contains(buf.String(), "too many in-flight pushes; dropping") {
		t.Errorf("expected the backpressure drop WRN, got %q", buf.String())
	}
}

// gatedTarget blocks until gate is closed (or ctx is done), letting tests deterministically
// occupy a fixed number of workers/queue slots.
type gatedTarget struct {
	n    *atomic.Int64
	gate chan struct{}
}

func (g gatedTarget) Announce(ctx context.Context, _ announce.Release) (announce.Result, error) {
	select {
	case <-g.gate:
	case <-ctx.Done():
		return announce.Result{}, ctx.Err()
	}
	g.n.Add(1)
	return announce.Result{}, nil
}
