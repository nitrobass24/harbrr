package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"testing"
	"time"

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
		// Gateway statuses (#247): the request-path builders wrap search.ErrGatewayStatus
		// for 502/504/522 only, so these now classify as transport — a reachable-but-
		// unhappy tracker answering with a plain 404/500 stays unclassified (the tracker
		// itself answered; that's not a gateway outage).
		{"gateway 502", fmt.Errorf("GET https://tracker.example: tracker returned HTTP 502: %w", search.ErrGatewayStatus), domain.HealthTransport, true},
		{"gateway 504", fmt.Errorf("GET https://tracker.example: tracker returned HTTP 504: %w", search.ErrGatewayStatus), domain.HealthTransport, true},
		{"gateway 522", fmt.Errorf("GET https://tracker.example: tracker returned HTTP 522: %w", search.ErrGatewayStatus), domain.HealthTransport, true},
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
		// #389: no evidence of anything is "unknown", not the old asserted "healthy".
		{name: "nothing ever observed", want: statusUnknown},
		{name: "recent failure", s: healthSignals{events: recent}, want: statusFailing},
		// Level 0 keeps the historical 1h window, so a 2h-old failure has expired —
		// but with no success either, that expiry lands on unknown, not healthy.
		{name: "failure past the level-0 window", s: healthSignals{events: old}, want: statusUnknown},
		// The window doubles per escalation rung: the same 2h-old failure still stands
		// at level 2 (4h).
		{name: "failure inside the level-2 window", s: healthSignals{events: old, level: 2}, want: statusFailing},
		// ...and is capped at 24h, so a 30h-old failure has expired even at the top rung.
		{name: "failure past the 24h cap", s: healthSignals{events: ancient, level: maxCircuitLevel}, want: statusUnknown},
		// A passing explicit Test is a success (#116) and clears the failure it covers.
		{name: "recovered failure", s: healthSignals{events: recent, recovery: recovered}, want: statusHealthy},
		{name: "failure after recovery", s: healthSignals{events: later, recovery: recovered}, want: statusFailing},
		// ...but that success expires too: a test that passed yesterday proves nothing now.
		{
			name: "stale recovery, no traffic",
			s:    healthSignals{recovery: database.HealthRecovery{ThroughEventID: 1, OccurredAt: ago(6 * time.Hour)}},
			want: statusUnknown,
		},
		// #253: an open circuit reads failing even with no recent triggering event (a
		// high escalation rung can outlast the failing window).
		{name: "circuit open, old failure", s: healthSignals{events: old, disabled: true}, want: statusFailing},
		// A search that reached the tracker AFTER the newest failure is the newest
		// evidence there is: it succeeded, so the failure no longer stands.
		{name: "success after a recent failure", s: healthSignals{events: recent, lastQuery: now}, want: statusHealthy},
		// A failed search counts as a query too, timestamped just before its event — so a
		// query that merely ties the newest failure is that failure, not a success.
		{name: "query tied with the failure", s: healthSignals{events: recent, lastQuery: ago(1 * time.Minute)}, want: statusFailing},
		{name: "recent success, no failures", s: healthSignals{lastQuery: ago(10 * time.Minute)}, want: statusHealthy},
		{name: "success past the healthy window", s: healthSignals{lastQuery: ago(3 * time.Hour)}, want: statusUnknown},
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

// TestFailingWindow pins the level-scaled failing window: level 0 is the historical
// 1h recency window, each rung doubles it, and it never exceeds 24h. Out-of-range
// levels clamp rather than panic on a negative shift.
func TestFailingWindow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		level int
		want  time.Duration
	}{
		{level: -1, want: time.Hour},
		{level: 0, want: time.Hour},
		{level: 1, want: 2 * time.Hour},
		{level: 4, want: 16 * time.Hour},
		{level: 5, want: 24 * time.Hour},
		{level: maxCircuitLevel, want: 24 * time.Hour},
		{level: 999, want: 24 * time.Hour},
	}
	for _, tt := range tests {
		if got := failingWindow(tt.level); got != tt.want {
			t.Errorf("failingWindow(%d) = %s, want %s", tt.level, got, tt.want)
		}
	}
}
