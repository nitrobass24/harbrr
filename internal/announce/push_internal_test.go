package announce

import (
	"testing"
	"time"
)

// TestConnPushBudget pins the per-connection deadline math: base + PerReleaseTimeout
// per release, uncapped here — the sink's outer context carries the hard cap.
func TestConnPushBudget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		releases int
		want     time.Duration
	}{
		{0, pushBudgetBase},
		{1, pushBudgetBase + PerReleaseTimeout},
		{94, pushBudgetBase + 94*PerReleaseTimeout},
	}
	for _, tt := range tests {
		if got := connPushBudget(tt.releases); got != tt.want {
			t.Errorf("connPushBudget(%d) = %v, want %v", tt.releases, got, tt.want)
		}
	}
}
