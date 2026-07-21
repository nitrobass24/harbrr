package api_test

import (
	"errors"
	"testing"

	"github.com/autobrr/harbrr/internal/backup"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/proxy"
	"github.com/autobrr/harbrr/internal/solver"
)

// TestSentinelParity locks the invariant writeServiceError's collapsed switch
// (internal/web/api/encode.go) depends on: every service's ErrInvalid/ErrConflict
// wraps the matching domain sentinel (so errors.Is against the domain sentinel
// alone classifies it), and every sentinel's wire text (400/409 bodies expose
// err.Error()) is byte-identical to its pre-collapse string.
func TestSentinelParity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sentinel error
		wantIs   error
		wantText string
	}{
		{"proxy.ErrInvalid", proxy.ErrInvalid, domain.ErrInvalid, "proxy: invalid input"},
		{"solver.ErrInvalid", solver.ErrInvalid, domain.ErrInvalid, "solver: invalid input"},
		{"backup.ErrInvalid", backup.ErrInvalid, domain.ErrInvalid, "backup: invalid input"},
		{"backup.ErrConflict", backup.ErrConflict, domain.ErrConflict, "backup: target instance is not empty"},
		{"registry.ErrInvalid", registry.ErrInvalid, domain.ErrInvalid, "registry: invalid request"},
		{"registry.ErrConflict", registry.ErrConflict, domain.ErrConflict, "registry: already exists"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !errors.Is(tt.sentinel, tt.wantIs) {
				t.Errorf("errors.Is(%s, %v) = false, want true", tt.name, tt.wantIs)
			}
			if got := tt.sentinel.Error(); got != tt.wantText {
				t.Errorf("%s.Error() = %q, want %q", tt.name, got, tt.wantText)
			}
		})
	}
}
