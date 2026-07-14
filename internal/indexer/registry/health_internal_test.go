package registry

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

func TestClassifyHealth(t *testing.T) {
	t.Parallel()
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

	if s := r.deriveStatus(nil, database.HealthRecovery{}); s != "healthy" {
		t.Errorf("no events => %q, want healthy", s)
	}
	recent := []domain.IndexerHealthEvent{{ID: 2, OccurredAt: now.Add(-1 * time.Minute)}}
	if s := r.deriveStatus(recent, database.HealthRecovery{}); s != "unhealthy" {
		t.Errorf("recent failure => %q, want unhealthy", s)
	}
	old := []domain.IndexerHealthEvent{{ID: 1, OccurredAt: now.Add(-2 * time.Hour)}}
	if s := r.deriveStatus(old, database.HealthRecovery{}); s != "healthy" {
		t.Errorf("old failure => %q, want healthy", s)
	}
	recovered := database.HealthRecovery{ThroughEventID: 2, OccurredAt: now}
	if s := r.deriveStatus(recent, recovered); s != "healthy" {
		t.Errorf("recovered failure => %q, want healthy", s)
	}
	later := []domain.IndexerHealthEvent{{ID: 3, OccurredAt: now}}
	if s := r.deriveStatus(later, recovered); s != "unhealthy" {
		t.Errorf("failure after recovery => %q, want unhealthy", s)
	}
}
