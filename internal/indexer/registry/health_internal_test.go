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

	recent := []domain.IndexerHealthEvent{{ID: 2, OccurredAt: now.Add(-1 * time.Minute)}}
	old := []domain.IndexerHealthEvent{{ID: 1, OccurredAt: now.Add(-2 * time.Hour)}}
	recovered := database.HealthRecovery{ThroughEventID: 2, OccurredAt: now}
	later := []domain.IndexerHealthEvent{{ID: 3, OccurredAt: now}}
	tests := []struct {
		name     string
		events   []domain.IndexerHealthEvent
		recovery database.HealthRecovery
		want     string
	}{
		{name: "no events", want: "healthy"},
		{name: "recent failure", events: recent, want: "unhealthy"},
		{name: "old failure", events: old, want: "healthy"},
		{name: "recovered failure", events: recent, recovery: recovered, want: "healthy"},
		{name: "failure after recovery", events: later, recovery: recovered, want: "unhealthy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := r.deriveStatus(tt.events, tt.recovery); got != tt.want {
				t.Errorf("deriveStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}
