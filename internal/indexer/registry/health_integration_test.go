package registry_test

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
)

// statusDoer returns a fixed status for every request (no network).
type statusDoer struct{ status int }

func (d statusDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	return &stdhttp.Response{
		StatusCode: d.status,
		Header:     stdhttp.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

// TestSearchRecordsHealthEvent proves a classified search failure is recorded as a
// health event and surfaced by Status: a 503 from the tracker -> rate_limited.
func TestSearchRecordsHealthEvent(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t, statusDoer{status: stdhttp.StatusServiceUnavailable})
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	if _, err := idx.Search(ctx, search.Query{Keywords: "bunny"}); !errors.Is(err, search.ErrRateLimited) {
		t.Fatalf("Search err = %v, want ErrRateLimited", err)
	}

	st, err := reg.Status(ctx, "tt")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Status != "unhealthy" {
		t.Errorf("status = %q, want unhealthy", st.Status)
	}
	if len(st.Events) != 1 || st.Events[0].Kind != domain.HealthRateLimited {
		t.Fatalf("events = %+v, want exactly one rate_limited", st.Events)
	}
}

// TestGrabRecordsHealthEvent proves a classified GRAB failure is health-recorded too
// (not just search): a 503 at grab time -> rate_limited, surfaced by Status. Before the
// fix, Grab never classified its errors, so this event was dropped.
func TestGrabRecordsHealthEvent(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t, statusDoer{status: stdhttp.StatusServiceUnavailable})
	ctx := context.Background()
	if _, err := reg.Add(ctx, registry.AddParams{
		Slug: "tt", DefinitionID: "testtracker", Settings: map[string]string{"apikey": "x"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	idx, ok := reg.Indexer(ctx, "tt")
	if !ok {
		t.Fatal("Indexer(tt) not resolved")
	}
	if _, err := idx.Grab(ctx, "https://testtracker.example/download/1"); !errors.Is(err, search.ErrRateLimited) {
		t.Fatalf("Grab err = %v, want ErrRateLimited", err)
	}

	st, err := reg.Status(ctx, "tt")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(st.Events) != 1 || st.Events[0].Kind != domain.HealthRateLimited {
		t.Fatalf("events = %+v, want exactly one rate_limited from the grab", st.Events)
	}
}

// TestStatusUnknownSlug: Status for a missing indexer is ErrNotFound (404 at the API).
func TestStatusUnknownSlug(t *testing.T) {
	t.Parallel()
	reg, _ := newRegistry(t, statusDoer{status: stdhttp.StatusOK})
	if _, err := reg.Status(context.Background(), "nope"); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("err = %v, want database.ErrNotFound", err)
	}
}
