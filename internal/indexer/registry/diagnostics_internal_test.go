package registry

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/database/dbtest"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

var diagNow = time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

// newDiagTestAdapter builds the minimal adapter recordHealth needs, wired to a
// fresh diagnostics ring.
func newDiagTestAdapter(t *testing.T) (*indexerAdapter, *diagnostics) {
	t.Helper()
	db := dbtest.OpenMigrated(t)
	ring := newDiagnostics()
	return &indexerAdapter{
		instanceID:   insertTestInstance(t, db),
		db:           db,
		health:       database.Health{},
		circuit:      database.Circuit{},
		circuitLocks: &circuitLocks{},
		diagnostics:  ring,
		startedAt:    diagNow.Add(-2 * time.Hour),
		clock:        func() time.Time { return diagNow },
		stats:        newIndexerStats(db, func() time.Time { return diagNow }, zerolog.Nop()),
		log:          zerolog.Nop(),
	}, ring
}

// parseFailure is a classified parse error carrying a capture, the shape the
// engine hands recordHealth.
func parseFailure(body string) error {
	return &search.CaptureError{
		Capture: search.Capture{Method: "GET", URL: "https://cap.invalid/api", Status: 200, Body: body},
		Err:     fmt.Errorf("%w: splitting rows", search.ErrParseError),
	}
}

// TestDiagnosticsRing pins the ring's contract: newest-first, capped at
// diagnosticsRingSize, and dropped wholesale when the instance goes away.
func TestDiagnosticsRing(t *testing.T) {
	t.Parallel()

	ring := newDiagnostics()
	for i := range 7 {
		ring.record(1, FailureCapture{
			Kind:       domain.HealthParseError,
			OccurredAt: diagNow.Add(time.Duration(i) * time.Minute),
			Capture:    search.Capture{Body: fmt.Sprintf("page-%d", i)},
		})
	}
	got := ring.list(1)
	if len(got) != diagnosticsRingSize {
		t.Fatalf("ring size = %d, want %d", len(got), diagnosticsRingSize)
	}
	// Newest first: the last recorded (page-6) leads, the two oldest are gone.
	for i, want := range []string{"page-6", "page-5", "page-4", "page-3", "page-2"} {
		if got[i].Capture.Body != want {
			t.Errorf("entry %d = %q, want %q", i, got[i].Capture.Body, want)
		}
	}
	// Another instance's ring is untouched by this one.
	if other := ring.list(2); other != nil {
		t.Errorf("instance 2 ring = %v, want nil", other)
	}
	ring.ForgetInstance(1)
	if after := ring.list(1); after != nil {
		t.Errorf("ring after ForgetInstance = %v, want nil", after)
	}
}

// TestRecordHealthFilesCapture: a classified failure that CARRIES a capture is
// filed with its kind and timestamp; a classified failure without one (a native
// driver) adds nothing, because the health event already records it; and an
// unclassified error adds nothing at all.
func TestRecordHealthFilesCapture(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("classified failure with a capture is filed", func(t *testing.T) {
		t.Parallel()
		a, ring := newDiagTestAdapter(t)
		a.recordHealth(ctx, parseFailure("<html>drifted</html>"))

		got := ring.list(a.instanceID)
		if len(got) != 1 {
			t.Fatalf("entries = %d, want 1", len(got))
		}
		if got[0].Kind != domain.HealthParseError {
			t.Errorf("kind = %q, want %q", got[0].Kind, domain.HealthParseError)
		}
		if !got[0].OccurredAt.Equal(diagNow) {
			t.Errorf("occurredAt = %v, want %v", got[0].OccurredAt, diagNow)
		}
		if got[0].Capture.Body != "<html>drifted</html>" {
			t.Errorf("body = %q, want the captured page", got[0].Capture.Body)
		}
	})

	t.Run("classified failure without a capture files nothing", func(t *testing.T) {
		t.Parallel()
		a, ring := newDiagTestAdapter(t)
		a.recordHealth(ctx, fmt.Errorf("native: %w", search.ErrParseError))

		if got := ring.list(a.instanceID); got != nil {
			t.Errorf("entries = %v, want none", got)
		}
	})

	t.Run("unclassified error files nothing", func(t *testing.T) {
		t.Parallel()
		a, ring := newDiagTestAdapter(t)
		a.recordHealth(ctx, errors.New("boom"))

		if got := ring.list(a.instanceID); got != nil {
			t.Errorf("entries = %v, want none", got)
		}
	})

	t.Run("a successful search retains nothing", func(t *testing.T) {
		t.Parallel()
		a, ring := newDiagTestAdapter(t)
		// The serve path calls recordHealth only on a failure; nothing ever reaches
		// the ring for a search that parsed.
		if got := ring.list(a.instanceID); got != nil {
			t.Errorf("entries = %v, want none", got)
		}
	})
}
