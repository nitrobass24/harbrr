package announce_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/announce"
	"github.com/autobrr/harbrr/internal/apps"
	"github.com/autobrr/harbrr/internal/auth"
	"github.com/autobrr/harbrr/internal/database/dbtest"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/secrets"
)

// deadlineTarget records how much of a window pushOne left on each per-release context.
// It never sleeps: reading the deadline is enough to prove which timeout was applied.
type deadlineTarget struct {
	timeout   time.Duration
	remaining []time.Duration
}

func (d *deadlineTarget) Announce(ctx context.Context, _ announce.Release) (announce.Result, error) {
	dl, ok := ctx.Deadline()
	if !ok {
		return announce.Result{}, errors.New("pushOne left the per-release context unbounded")
	}
	d.remaining = append(d.remaining, time.Until(dl))
	return announce.Result{}, nil
}

func (d *deadlineTarget) Probe(context.Context) error    { return nil }
func (d *deadlineTarget) AnnounceTimeout() time.Duration { return d.timeout }

// TestServicePushOneUsesTargetAnnounceTimeout pins #527's mechanism: the per-release window
// comes from the TARGET, not from one global constant, so a target declaring a long ceiling
// actually gets it while one declaring a short ceiling is unaffected.
func TestServicePushOneUsesTargetAnnounceTimeout(t *testing.T) {
	t.Parallel()
	// Named by magnitude, not by tool: the injected factory below ignores the connection's
	// Kind and returns the fake regardless, so nothing here exercises a real driver. Which
	// kind declares which ceiling is pinned separately by TestTargetAnnounceTimeouts, which
	// builds the actual drivers.
	tests := []struct {
		name    string
		timeout time.Duration
	}{
		{"a long ceiling is granted in full", 120 * time.Second},
		{"a short ceiling is left alone", 10 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			tgt := &deadlineTarget{timeout: tt.timeout}
			svc, _, _ := newService(t, func(domain.AnnounceConnection, string) (announce.Target, error) { return tgt, nil })
			if _, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
				Name: "t", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
			}); err != nil {
				t.Fatalf("create: %v", err)
			}
			rels := []announce.Release{{Name: "a", GUID: "g0"}, {Name: "b", GUID: "g1"}, {Name: "c", GUID: "g2"}}
			svc.Push(ctx, func(domain.AnnounceConnection) []announce.Release { return rels })

			if len(tgt.remaining) != len(rels) {
				t.Fatalf("announced %d releases, want %d", len(tgt.remaining), len(rels))
			}
			// Generous slack: this asserts WHICH timeout was applied, not scheduler
			// precision. The only values in play are 10s and 120s, so 5s of slop still
			// tells them apart decisively while leaving room for a contended runner to
			// deschedule us between WithTimeout and the deadline read.
			const slack = 5 * time.Second
			for i, remaining := range tgt.remaining {
				if remaining > tt.timeout || remaining < tt.timeout-slack {
					t.Errorf("release %d got a %v window, want ~%v (the target's own ceiling)", i, remaining, tt.timeout)
				}
			}
		})
	}
}

// slowQuiTarget stands in for a qui apply that takes longer than the 10s harbrr used to
// allow but finishes well inside qui's own 120s ceiling. It never sleeps: it reads the
// window pushOne granted and only "succeeds" if that window is long enough for the work.
type slowQuiTarget struct {
	timeout time.Duration // qui's declared ceiling
	needs   time.Duration // how long the simulated apply takes
}

func (s *slowQuiTarget) Announce(ctx context.Context, _ announce.Release) (announce.Result, error) {
	dl, ok := ctx.Deadline()
	if !ok {
		return announce.Result{}, errors.New("pushOne left the per-release context unbounded")
	}
	if time.Until(dl) < s.needs {
		// The window closes mid-apply — exactly what harbrr saw before #527. qui itself
		// still completes the injection (context.WithoutCancel), so this "failure" is a
		// lie about work that in fact happened.
		return announce.Result{}, context.DeadlineExceeded
	}
	return announce.Result{Matched: true}, nil
}

func (s *slowQuiTarget) Probe(context.Context) error    { return nil }
func (s *slowQuiTarget) AnnounceTimeout() time.Duration { return s.timeout }

// TestServicePushSlowQuiCountsAsSuccess is the regression guard for #527: a qui push that
// takes longer than the old shared 10s cap, but less than qui's own ceiling, must be
// reported as a match — not folded into the batch's failed count and a WRN. Under the old
// behaviour the per-release window was a flat 10s (and a 1-release batch budget only 40s),
// so a 45s apply was cut off and miscounted as a failure that never happened.
func TestServicePushSlowQuiCountsAsSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var buf bytes.Buffer
	db := dbtest.OpenMigrated(t)
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: testKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	appsSvc := apps.NewService(db, kr, http.DefaultClient, zerolog.Nop())
	// needs is comfortably above the old 10s cap and below qui's 120s ceiling.
	tgt := &slowQuiTarget{timeout: 120 * time.Second, needs: 45 * time.Second}
	svc := announce.NewService(db, appsSvc, auth.NewService(db), kr,
		func(domain.AnnounceConnection, string) (announce.Target, error) { return tgt, nil }, zerolog.New(&buf))
	if _, err := svc.CreateConnection(ctx, announce.CreateConnectionParams{
		Name: "qui", Kind: domain.AnnounceKindQui, BaseURL: "http://qui:7476", APIKey: "k", HarbrrURL: "http://h:8787",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// One release: batch pacing alone would not cover a single 45s apply, so this also pins
	// that the connection budget accommodates one worst-case release.
	rels := []announce.Release{{Name: "slow.apply", GUID: "g0"}}
	if matched := svc.Push(ctx, func(domain.AnnounceConnection) []announce.Release { return rels }); matched != 1 {
		t.Errorf("matched = %d, want 1 — a slow-but-successful qui apply must not be reported as a failure", matched)
	}
	if logged := buf.String(); strings.Contains(logged, "push failed") {
		t.Errorf("a slow-but-successful push logged a failure: %q", logged)
	}
}
