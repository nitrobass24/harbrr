package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/web/api"
)

// TestCrossSeedSnippet proves the endpoint returns the freeleech-bypass /full feed URL
// (no embedded key) plus a config.js entry with the apikey placeholder, and 404s an
// unknown slug.
func TestCrossSeedSnippet(t *testing.T) {
	t.Parallel()
	e := newEnv(t, api.Config{})
	base, c := serve(t, e)
	setupAndLogin(t, base, c)
	addTestIndexer(t, e, base, c, "tt")

	resp, body := do(t, c, http.MethodGet, base+"/api/indexers/tt/crossseed-snippet", nil, nil)
	mustStatus(t, resp, body, http.StatusOK)

	var got struct {
		Indexer  string `json:"indexer"`
		FeedURL  string `json:"feedUrl"`
		ConfigJs string `json:"configJs"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v (body: %s)", err, body)
	}

	if got.Indexer != "tt" {
		t.Errorf("indexer = %q, want tt", got.Indexer)
	}
	if !strings.HasSuffix(got.FeedURL, "/api/indexers/tt/results/torznab/full") {
		t.Errorf("feedUrl = %q, want the /full bypass variant", got.FeedURL)
	}
	if strings.Contains(got.FeedURL, "apikey") {
		t.Errorf("feedUrl must not embed an apikey: %q", got.FeedURL)
	}
	if !strings.Contains(got.ConfigJs, "/results/torznab/full?apikey=<YOUR_HARBRR_API_KEY>") {
		t.Errorf("configJs missing the /full URL + apikey placeholder: %q", got.ConfigJs)
	}

	// Unknown slug 404s rather than emitting a dead URL.
	resp, body = do(t, c, http.MethodGet, base+"/api/indexers/does-not-exist/crossseed-snippet", nil, nil)
	mustStatus(t, resp, body, http.StatusNotFound)
}
