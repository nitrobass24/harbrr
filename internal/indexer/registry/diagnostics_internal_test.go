package registry

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
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

// TestDiagnosticsLifecycle walks one instance's ring in order — nothing retained
// until something fails, then the failure, then gone with the instance. Sequential
// on purpose: the steps share the ring and only mean anything in this order.
func TestDiagnosticsLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, ring := newDiagTestAdapter(t)

	// A search that parsed never calls recordHealth, so the ring stays empty.
	if got := ring.list(a.instanceID); got != nil {
		t.Fatalf("ring before any failure = %v, want none", got)
	}
	a.recordHealth(ctx, parseFailure("<html>drifted</html>"))
	if got := ring.list(a.instanceID); len(got) != 1 {
		t.Fatalf("entries after a failure = %d, want 1", len(got))
	}
	// Deleting the instance takes its captures with it.
	ring.ForgetInstance(a.instanceID)
	if got := ring.list(a.instanceID); got != nil {
		t.Errorf("ring after the instance is forgotten = %v, want none", got)
	}
}

// TestRecordHealthFilesCapture: only a failure that CARRIES a capture is filed. A
// classified failure without one (a native driver) adds nothing — the health event
// already records it and a bare kind+time entry would just duplicate the events
// list — and an unclassified error adds nothing at all.
func TestRecordHealthFilesCapture(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// wantCapture is compared WHOLE rather than field by field: the ring stores the
	// engine's capture verbatim, so the method, the redacted URL, the status and the
	// body all have to survive the trip — a request-summary entry is only useful if
	// its method and URL come back out.
	tests := []struct {
		name        string
		err         error
		wantKind    string // "" = nothing filed
		wantCapture search.Capture
	}{
		{
			name:     "classified failure with a capture is filed",
			err:      parseFailure("<html>drifted</html>"),
			wantKind: domain.HealthParseError,
			wantCapture: search.Capture{
				Method: "GET", URL: "https://cap.invalid/api", Status: 200, Body: "<html>drifted</html>",
			},
		},
		{
			name: "transport failure with a request-summary capture is filed",
			err: &search.CaptureError{
				Capture: search.Capture{Method: "GET", URL: "https://cap.invalid/api"},
				Err:     &url.Error{Op: "Get", URL: "https://cap.invalid", Err: errors.New("connection refused")},
			},
			wantKind: domain.HealthTransport,
			// No response was received, so only the request summary is retained.
			wantCapture: search.Capture{Method: "GET", URL: "https://cap.invalid/api"},
		},
		{
			name: "classified failure without a capture files nothing",
			err:  fmt.Errorf("native: %w", search.ErrParseError),
		},
		{
			name: "unclassified error files nothing",
			err:  errors.New("boom"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, ring := newDiagTestAdapter(t)
			a.recordHealth(ctx, tt.err)
			got := ring.list(a.instanceID)

			if tt.wantKind == "" {
				if got != nil {
					t.Fatalf("entries = %v, want none", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("entries = %d, want 1", len(got))
			}
			if got[0].Kind != tt.wantKind {
				t.Errorf("kind = %q, want %q", got[0].Kind, tt.wantKind)
			}
			if !got[0].OccurredAt.Equal(diagNow) {
				t.Errorf("occurredAt = %v, want %v", got[0].OccurredAt, diagNow)
			}
			if !reflect.DeepEqual(got[0].Capture, tt.wantCapture) {
				t.Errorf("capture = %+v, want %+v", got[0].Capture, tt.wantCapture)
			}
		})
	}
}
