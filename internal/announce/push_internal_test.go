package announce

import (
	"testing"
	"time"
)

// TestConnPushBudget pins the per-connection deadline math, and specifically that pacing
// and ceiling stay separate concepts (#527): the batch is paced at perReleasePacing per
// release, but the budget must still fit ONE worst-case release, so a tiny batch against a
// long-ceilinged target (qui) is not cut short by its own pacing.
func TestConnPushBudget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		releases int
		ceiling  time.Duration
		want     time.Duration
	}{
		{"empty batch still fits one worst-case release", 0, quiAnnounceTimeout, pushBudgetBase + quiAnnounceTimeout},
		{"single qui release: the ceiling dominates the pacing", 1, quiAnnounceTimeout, pushBudgetBase + quiAnnounceTimeout},
		{"qui batch large enough that pacing dominates", 31, quiAnnounceTimeout, pushBudgetBase + 31*perReleasePacing},
		{"cross-seed v6 single release is unchanged", 1, csv6AnnounceTimeout, pushBudgetBase + perReleasePacing},
		{"cross-seed v6 large batch is unchanged", 94, csv6AnnounceTimeout, pushBudgetBase + 94*perReleasePacing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := connPushBudget(tt.releases, tt.ceiling); got != tt.want {
				t.Errorf("connPushBudget(%d, %v) = %v, want %v", tt.releases, tt.ceiling, got, tt.want)
			}
		})
	}
}

// TestConnPushBudgetStaysUnderOuterCap guards the starvation the budget exists to prevent:
// raising qui's ceiling must not let one connection eat the sink's whole 10-minute outer
// cap (internal/app: announcePushTimeoutMax). The ceiling only dominates while the batch is
// small, so the budget it produces has to stay comfortably inside that cap.
func TestConnPushBudgetStaysUnderOuterCap(t *testing.T) {
	t.Parallel()
	// Mirrors internal/app.announcePushTimeoutMax; it is unexported there and this package
	// must not import the composition root, so the value is restated.
	const outerCap = 10 * time.Minute
	for n := range 12 { // every batch size where the ceiling, not the pacing, sets the budget
		got := connPushBudget(n, quiAnnounceTimeout)
		if got != pushBudgetBase+quiAnnounceTimeout {
			t.Fatalf("connPushBudget(%d, qui) = %v, want the ceiling to dominate", n, got)
		}
		if got >= outerCap {
			t.Errorf("connPushBudget(%d, qui) = %v, must stay under the %v outer cap", n, got, outerCap)
		}
	}
	// From 12 releases up the pacing dominates, so the budget is exactly what it was
	// before qui got its own ceiling — the large-batch behaviour is untouched.
	for _, n := range []int{12, 31, 94} {
		if got, want := connPushBudget(n, quiAnnounceTimeout), pushBudgetBase+time.Duration(n)*perReleasePacing; got != want {
			t.Errorf("connPushBudget(%d, qui) = %v, want %v (unchanged from the pre-#527 formula)", n, got, want)
		}
	}
}

// TestTargetAnnounceTimeouts pins each driver's declared ceiling: qui may be waited on for
// far longer because it finishes the apply after harbrr hangs up (context.WithoutCancel),
// while cross-seed v6 makes no such promise and keeps the original short bound.
func TestTargetAnnounceTimeouts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		target Target
		want   time.Duration
	}{
		{"qui", NewQui("http://qui:7476", "k", nil, nil, nil), 120 * time.Second},
		{"crossseed-v6", NewCrossSeedV6("http://cs:2468", "k", nil), 10 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.target.AnnounceTimeout(); got != tt.want {
				t.Errorf("AnnounceTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestQuiWidensClientTimeout guards the wall underneath the fix: http.Client.Timeout is a
// hard cut-off no request context can lift, so leaving the shared 30s client on the
// announce path would abort a slow qui apply at 30s and make the longer ceiling inert.
// Probe must keep the client it was handed, so a Test against a hung qui is still bounded
// the way it always was.
func TestQuiWidensClientTimeout(t *testing.T) {
	t.Parallel()
	shared := defaultHTTPClient() // the production shape: a 30s wall
	q, ok := NewQui("http://qui:7476", "k", shared, nil, nil).(*quiAnnouncer)
	if !ok {
		t.Fatal("NewQui did not return a *quiAnnouncer")
	}
	if q.client.Timeout < quiAnnounceTimeout {
		t.Errorf("announce client Timeout = %v, want at least the announce ceiling %v", q.client.Timeout, quiAnnounceTimeout)
	}
	if q.probePoster.client != shared {
		t.Error("Probe must run on the injected client, unchanged")
	}
	if shared.Timeout != httpClientTimeout {
		t.Errorf("the shared client was mutated: Timeout = %v, want %v", shared.Timeout, httpClientTimeout)
	}
}
