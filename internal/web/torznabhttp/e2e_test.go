package torznabhttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
)

// engineIndexer adapts a real Cardigann engine to the handler's core.Indexer
// interface for the offline end-to-end proof: capabilities come from the engine,
// and Search extracts releases from a SAVED tracker response with no network
// (ParseResponseQuery), exercising the real loader -> mapper -> selector ->
// filter -> dateparse -> normalizer pipeline behind the HTTP handler.
type engineIndexer struct {
	info   core.IndexerInfo
	engine *cardigann.Engine
	body   []byte
}

func (e *engineIndexer) Info() core.IndexerInfo             { return e.info }
func (e *engineIndexer) Capabilities() *mapper.Capabilities { return e.engine.Capabilities() }

func (e *engineIndexer) Search(_ context.Context, q search.Query) ([]*normalizer.Release, error) {
	return e.engine.ParseResponseQuery(e.body, "", q)
}

func (e *engineIndexer) NeedsResolver() bool        { return e.engine.NeedsResolver() }
func (e *engineIndexer) DownloadNeedsAuth() bool    { return e.engine.DownloadNeedsAuth() }
func (e *engineIndexer) SupportsOffsetPaging() bool { return e.engine.SupportsOffsetPaging() }

func (e *engineIndexer) Grab(ctx context.Context, l string) (*search.GrabResult, error) {
	return e.engine.Grab(ctx, l)
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // fixed test path.
	if err != nil {
		t.Fatalf("reading %q: %v", name, err)
	}
	return b
}

// e2eFixedClock pins both the engine's date context and the handler's pubDate
// fallback so the goldens are deterministic.
func e2eFixedClock() time.Time { return time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC) }

func newE2EHandler(t *testing.T) http.Handler {
	t.Helper()
	def, err := loader.Parse(readTestdata(t, "e2e/definition.yml"))
	if err != nil {
		t.Fatalf("loader.Parse: %v", err)
	}
	engine, err := cardigann.NewEngine(def, cardigann.WithClock(e2eFixedClock))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	idx := &engineIndexer{
		info: core.IndexerInfo{
			ID: def.ID, Name: def.Name, Description: def.Description,
			SiteLink: def.Links[0], Type: def.Type,
		},
		engine: engine,
		body:   readTestdata(t, "e2e/response.html"),
	}
	return NewHandler(
		fakeProvider{def.ID: idx},
		WithAPIKey(testAPIKey),
		WithClock(e2eFixedClock),
	)
}

// e2eGet drives the handler with a fixed-host request (so the atom:link self URL
// is deterministic) and returns the response body, asserting HTTP 200.
func e2eGet(t *testing.T, h http.Handler, rawQuery string) []byte {
	t.Helper()
	url := "http://harbrr.test/api/indexers/e2edemo/results/torznab?" + rawQuery + "&apikey=" + testAPIKey
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body:\n%s", res.StatusCode, rec.Body.String())
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return body
}

// TestEndToEndOffline is the offline pipeline proof: a Sonarr/Radarr
// request sequence (t=caps then t=search) is driven through the *arr HTTP handler
// over the real Cardigann engine + a saved tracker response, with NO network and
// NO live tracker, producing the golden Torznab XML. The live "5 real trackers"
// half is covered by the live smoke test; this is the offline half.
func TestEndToEndOffline(t *testing.T) {
	t.Parallel()
	h := newE2EHandler(t)

	caps := e2eGet(t, h, "t=caps")
	assertGolden(t, "e2e/caps.xml", caps)

	results := e2eGet(t, h, "t=search&q=example")
	assertGolden(t, "e2e/search.xml", results)
}
