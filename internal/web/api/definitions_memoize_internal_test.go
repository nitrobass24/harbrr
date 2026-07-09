package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
)

// newDefsRouter builds a bare router carrying only the fields listDefinitions
// touches, with an injected load func. It bypasses NewRouter so the memoize
// behavior can be exercised without wiring the full dependency graph.
func newDefsRouter(load func() ([]definitionSummary, error)) *router {
	return &router{log: zerolog.Nop(), loadDefs: load}
}

func callListDefinitions(t *testing.T, rt *router) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/definitions", nil)
	rt.listDefinitions(rec, req)
	return rec
}

// TestListDefinitionsRetriesAfterTransientError is the fail-before/pass-after
// core: a first-call load error is surfaced (500) but NOT cached, so the next
// call retries and succeeds (200). Under the old sync.Once memoization the
// cached error made the second call 500 forever — this asserts 200, so it fails
// before the fix and passes after.
func TestListDefinitionsRetriesAfterTransientError(t *testing.T) {
	t.Parallel()
	var calls int
	rt := newDefsRouter(func() ([]definitionSummary, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("transient load failure")
		}
		return []definitionSummary{{ID: "acme", Name: "Acme"}}, nil
	})

	if got := callListDefinitions(t, rt).Code; got != http.StatusInternalServerError {
		t.Fatalf("first call status = %d, want 500", got)
	}
	rec := callListDefinitions(t, rt)
	if rec.Code != http.StatusOK {
		t.Fatalf("second call status = %d, want 200 (error must not be cached)", rec.Code)
	}
	if calls != 2 {
		t.Fatalf("loadDefs calls = %d, want 2 (retry after the failure)", calls)
	}
}

// TestListDefinitionsMemoizesSuccess proves a successful load is cached: two
// consecutive 200s invoke loadDefs exactly once.
func TestListDefinitionsMemoizesSuccess(t *testing.T) {
	t.Parallel()
	var calls int
	rt := newDefsRouter(func() ([]definitionSummary, error) {
		calls++
		return []definitionSummary{{ID: "acme", Name: "Acme"}}, nil
	})

	for i := range 2 {
		if got := callListDefinitions(t, rt).Code; got != http.StatusOK {
			t.Fatalf("call %d status = %d, want 200", i, got)
		}
	}
	if calls != 1 {
		t.Fatalf("loadDefs calls = %d, want 1 (success memoized)", calls)
	}
}

// TestListDefinitionsConcurrentFirstCallLoadsOnce proves the mutex serializes
// concurrent first-calls: with a success load, loadDefs runs exactly once and
// every caller gets 200. Run under -race to catch a data race on the cache.
func TestListDefinitionsConcurrentFirstCallLoadsOnce(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	rt := newDefsRouter(func() ([]definitionSummary, error) {
		calls.Add(1)
		return []definitionSummary{{ID: "acme", Name: "Acme"}}, nil
	})

	const n = 16
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = callListDefinitions(t, rt).Code
		}()
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("loadDefs calls = %d, want 1 (mutex must serialize first-calls)", got)
	}
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("goroutine %d status = %d, want 200", i, code)
		}
	}
}
