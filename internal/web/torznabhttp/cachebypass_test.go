package torznabhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/core"
)

// TestWriteResultsSetsCacheBypass proves the Torznab feed path (writeResults)
// threads nocache=1 into the context handed to Search, and absent does not.
func TestWriteResultsSetsCacheBypass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		rawQuery string
		want     bool
	}{
		{"nocache=1", "t=search&q=x&nocache=1", true},
		{"absent", "t=search&q=x", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			idx := demoIndexer(t)
			h := newTestHandler(t, idx)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
				"/api/indexers/demo/results/torznab?"+tt.rawQuery+"&apikey="+testAPIKey, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
			}
			if got := core.CacheBypass(idx.gotCtx); got != tt.want {
				t.Errorf("CacheBypass(Search ctx) = %v, want %v", got, tt.want)
			}
		})
	}
}
