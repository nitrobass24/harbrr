package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

func TestClassifyHealth(t *testing.T) {
	t.Parallel()
	// timeoutErr satisfies net.Error (Timeout()==true), the client-timeout shape.
	timeoutErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}
	dnsErr := &net.DNSError{Err: "no such host", Name: "example.invalid", IsNotFound: true}
	connRefused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	tests := []struct {
		name string
		err  error
		want string
		ok   bool
	}{
		{"auth", login.ErrLoginFailed, domain.HealthAuthFailure, true},
		{"anti-bot", login.ErrSolverRequired, domain.HealthAntiBot, true},
		{"rate-limited", search.ErrRateLimited, domain.HealthRateLimited, true},
		{"parse", search.ErrParseError, domain.HealthParseError, true},
		{"wrapped auth", fmt.Errorf("cardigann: login for x: %w", login.ErrLoginFailed), domain.HealthAuthFailure, true},
		{"unclassified", errors.New("boom"), "", false},
		{"net.Error timeout", timeoutErr, domain.HealthTransport, true},
		{"connection refused", connRefused, domain.HealthTransport, true},
		{"dns failure", dnsErr, domain.HealthTransport, true},
		{"context deadline exceeded", context.DeadlineExceeded, domain.HealthTransport, true},
		{"url.Error chain", &url.Error{Op: "Get", URL: "https://tracker.example/x", Err: errors.New("connection reset by peer")}, domain.HealthTransport, true},
		{"wrapped net error", fmt.Errorf("GET https://tracker.example: %w", timeoutErr), domain.HealthTransport, true},
		{"unexpected EOF read", fmt.Errorf("reading response from https://tracker.example: %w", io.ErrUnexpectedEOF), domain.HealthTransport, true},
		{"plain EOF read", fmt.Errorf("reading response from https://tracker.example: %w", io.EOF), domain.HealthTransport, true},
		// Gateway statuses (#247, widened in #457): the request-path builders wrap
		// search.ErrGatewayStatus for the origin-down family only, so these classify as
		// transport — a reachable-but-unhappy tracker answering with a plain 404/500 stays
		// unclassified (the tracker itself answered; that's not a gateway outage).
		{"gateway 502", fmt.Errorf("GET https://tracker.example: tracker returned HTTP 502: %w", search.ErrGatewayStatus), domain.HealthTransport, true},
		{"gateway 504", fmt.Errorf("GET https://tracker.example: tracker returned HTTP 504: %w", search.ErrGatewayStatus), domain.HealthTransport, true},
		{"gateway 521", fmt.Errorf("GET https://tracker.example: tracker returned HTTP 521: %w", search.ErrGatewayStatus), domain.HealthTransport, true},
		{"gateway 522", fmt.Errorf("GET https://tracker.example: tracker returned HTTP 522: %w", search.ErrGatewayStatus), domain.HealthTransport, true},
		{"gateway 523", fmt.Errorf("GET https://tracker.example: tracker returned HTTP 523: %w", search.ErrGatewayStatus), domain.HealthTransport, true},
		{"gateway 524", fmt.Errorf("GET https://tracker.example: tracker returned HTTP 524: %w", search.ErrGatewayStatus), domain.HealthTransport, true},
		{"gateway 530", fmt.Errorf("GET https://tracker.example: tracker returned HTTP 530: %w", search.ErrGatewayStatus), domain.HealthTransport, true},
		{"not-found status stays unclassified", errors.New("GET https://tracker.example: tracker returned HTTP 404"), "", false},
		{"server error status stays unclassified", errors.New("GET https://tracker.example: tracker returned HTTP 500"), "", false},
		// A mid-body read failure carries the native ErrBodyRead marker: transport,
		// not parse (#234) — even when the underlying cause is a bespoke error shape
		// that isn't itself an EOF or net.Error.
		{"body-read marker", fmt.Errorf("newznab: %w: %w", native.ErrBodyRead, errors.New("bespoke stream error")), domain.HealthTransport, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := classifyHealth(tt.err)
			if ok != tt.ok || got != tt.want {
				t.Errorf("classifyHealth = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestReachedTracker pins which failures count as evidence about a tracker. Everything
// that never left the process must answer false — under the sticky derivation a query
// stamp with no success after it reads failing forever — while a request the tracker
// simply did not answer must stay true.
func TestReachedTracker(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// callerGone cancels the caller's context before the call, the shape a
		// disconnected consumer leaves behind.
		callerGone bool
		err        error
		want       bool
	}{
		{name: "tracker answered badly", err: errors.New("tracker returned HTTP 500"), want: true},
		{name: "client timeout, caller still live", err: &url.Error{Op: "GET", Err: context.DeadlineExceeded}, want: true},
		{name: "caller cancelled", callerGone: true, err: &url.Error{Op: "GET", Err: context.Canceled}},
		{name: "caller deadline elapsed", callerGone: true, err: errors.New("whatever the engine surfaced")},
		{name: "inherited cancellation, caller live", err: fmt.Errorf("registry: request aborted: %w", context.Canceled)},
		{name: "pacing budget refused the request", err: fmt.Errorf("%w: %w", errPacingBudget, context.DeadlineExceeded)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.callerGone {
				cancel()
			}
			if got := reachedTracker(ctx, tt.err); got != tt.want {
				t.Errorf("reachedTracker = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeriveStatus(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.June, 14, 12, 0, 0, 0, time.UTC)
	// deriveStatus lives on StatsReporter now; construct it directly (it needs only clock).
	r := &StatsReporter{clock: func() time.Time { return now }}

	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	recent := []domain.IndexerHealthEvent{{ID: 2, OccurredAt: ago(1 * time.Minute)}}
	old := []domain.IndexerHealthEvent{{ID: 1, OccurredAt: ago(2 * time.Hour)}}
	ancient := []domain.IndexerHealthEvent{{ID: 1, OccurredAt: ago(30 * time.Hour)}}
	recovered := database.HealthRecovery{ThroughEventID: 2, OccurredAt: now}
	later := []domain.IndexerHealthEvent{{ID: 3, OccurredAt: now}}
	tests := []struct {
		name string
		s    healthSignals
		want string
	}{
		// #389/#484: no evidence of anything is "unknown" — now meaning never tested,
		// never the old asserted "healthy".
		{name: "nothing ever observed", want: StatusUnknown},
		{name: "recent failure", s: healthSignals{events: recent}, want: StatusFailing},
		// #484: health is sticky, so age alone changes nothing. A failure nobody has
		// succeeded past still stands hours later, and a day later.
		{name: "old failure still stands", s: healthSignals{events: old}, want: StatusFailing},
		{name: "ancient failure still stands", s: healthSignals{events: ancient}, want: StatusFailing},
		// A passing explicit Test is a success (#116) and clears the failure it covers.
		{name: "recovered failure", s: healthSignals{events: recent, recovery: recovered}, want: StatusHealthy},
		{name: "failure after recovery", s: healthSignals{events: later, recovery: recovered}, want: StatusFailing},
		// ...and that success does not expire either: an idle indexer keeps the last
		// answer it gave rather than aging back to unknown (#484).
		{
			name: "old recovery, no traffic since",
			s:    healthSignals{recovery: database.HealthRecovery{ThroughEventID: 1, OccurredAt: ago(6 * time.Hour)}},
			want: StatusHealthy,
		},
		// #253: an open circuit reads failing even with no recorded triggering event.
		{name: "circuit open, old failure", s: healthSignals{events: old, disabled: true}, want: StatusFailing},
		// A search that actually SUCCEEDED after the newest failure is the newest evidence
		// there is, so the failure no longer stands.
		{name: "success after a recent failure", s: healthSignals{events: recent, lastSuccess: now}, want: StatusHealthy},
		// A success in the same instant as the failure is not evidence the failure is over.
		{name: "success tied with the failure", s: healthSignals{events: recent, lastSuccess: ago(1 * time.Minute)}, want: StatusFailing},
		{name: "recent success, no failures", s: healthSignals{lastSuccess: ago(10 * time.Minute)}, want: StatusHealthy},
		// Idle-but-previously-healthy stays healthy indefinitely (#484): the success is
		// three hours old and nothing has been observed since.
		{name: "old success, no traffic since", s: healthSignals{lastSuccess: ago(3 * time.Hour)}, want: StatusHealthy},
		// #457: attempts are not successes — a stale success cannot be refreshed by the
		// failing traffic itself, so classified failures still read failing.
		{name: "stale success, classified failures", s: healthSignals{events: recent, lastSuccess: ago(3 * time.Hour)}, want: StatusFailing},
		// #484 rule 3, the parameter-free catch-all: queries since the last success read
		// failing even though NOTHING was classified, so no health event exists at all.
		{
			name: "queries since the last success, nothing classified",
			s:    healthSignals{lastSuccess: ago(3 * time.Hour), lastQuery: ago(1 * time.Minute)},
			want: StatusFailing,
		},
		// Never succeeded, only queried: the same rule, from a cold start.
		{name: "queried, never succeeded", s: healthSignals{lastQuery: ago(1 * time.Minute)}, want: StatusFailing},
		// ...and it self-heals on the next success, with no window to wait out.
		{
			name: "success after the failing queries",
			s:    healthSignals{lastSuccess: ago(1 * time.Minute), lastQuery: ago(2 * time.Minute)},
			want: StatusHealthy,
		},
		// A search that succeeded stamps the query first and the success microseconds
		// later; both are truncated to the second, so the pair is a tie, not a query
		// nothing succeeded on.
		{
			name: "query and success in the same second",
			s:    healthSignals{lastSuccess: now, lastQuery: now},
			want: StatusHealthy,
		},
		// A passing Test counts as the success the queries are measured against, even
		// though it is not itself a query.
		{
			name: "test passed after the failing queries",
			s:    healthSignals{recovery: recovered, lastQuery: ago(1 * time.Minute)},
			want: StatusHealthy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := r.deriveStatus(tt.s); got != tt.want {
				t.Errorf("deriveStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSuccessSecondGranularityTiesToTheFailure pins the precision contract behind the
// #457 derivation: health events persist as RFC3339 SECONDS, so a millisecond-precise
// success stamp could otherwise order itself after a failure recorded later in the same
// second and declare healthy off the very failure it lost the race with. RecordSuccess
// truncates to the second, which makes any same-second pair a tie — resolved in the
// failure's favour — while a success in a later second still recovers. Each case is also
// re-derived after a flush/rehydrate round trip, so a restart cannot change the answer.
func TestSuccessSecondGranularityTiesToTheFailure(t *testing.T) {
	t.Parallel()
	// The failure event as the store reads it back: whole seconds, sub-second part lost.
	failAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	events := []domain.IndexerHealthEvent{{ID: 1, OccurredAt: failAt}}
	r := &StatsReporter{clock: func() time.Time { return failAt.Add(time.Minute) }}

	tests := []struct {
		name      string
		successAt time.Time
		want      string
	}{
		// The dangerous direction: the success genuinely came first, and the failure that
		// followed it 600ms later stored as failAt. Without truncation this read healthy.
		{name: "success before a same-second failure", successAt: failAt.Add(200 * time.Millisecond), want: StatusFailing},
		// The benign direction, conservative by construction: at second granularity there
		// is no evidence the success came after, so the failure keeps standing.
		{name: "success after a same-second failure", successAt: failAt.Add(800 * time.Millisecond), want: StatusFailing},
		{name: "success in a later second", successAt: failAt.Add(time.Second), want: StatusHealthy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := openCacheDB(t, filepath.Join(t.TempDir(), "harbrr.db"))
			id := insertInstanceSlug(t, db, "tt")
			stats := newIndexerStats(db, func() time.Time { return tt.successAt }, zerolog.Nop())
			stats.RecordSuccess(id)

			recorded := stats.snapshot(id).lastSuccess
			if got := r.deriveStatus(healthSignals{events: events, lastSuccess: recorded}); got != tt.want {
				t.Errorf("deriveStatus() = %q, want %q (success %s vs failure %s)", got, tt.want, recorded, failAt)
			}

			// Restart: the flushed instant rehydrates unchanged, so the derivation is
			// identical on the other side of a reboot.
			stats.FlushCounters(ctx)
			restarted := newIndexerStats(db, func() time.Time { return tt.successAt }, zerolog.Nop())
			if err := restarted.RehydrateCounters(ctx); err != nil {
				t.Fatalf("RehydrateCounters: %v", err)
			}
			rehydrated := restarted.snapshot(id).lastSuccess
			if !rehydrated.Equal(recorded) {
				t.Fatalf("rehydrated lastSuccess = %s, want %s", rehydrated, recorded)
			}
			if got := r.deriveStatus(healthSignals{events: events, lastSuccess: rehydrated}); got != tt.want {
				t.Errorf("deriveStatus() after restart = %q, want %q", got, tt.want)
			}
		})
	}
}
