package appsync

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

var update = flag.Bool("update", false, "regenerate golden files")

// servarrStub is an in-memory Sonarr/Radarr v3 indexer API for driver tests. It
// records the last request body and auth header, assigns ids on create, and serves
// list/update/delete/test with the real status codes.
type servarrStub struct {
	mu       sync.Mutex
	indexers map[int]servarrIndexer
	nextID   int
	lastBody []byte
	lastAuth string
	testFail bool
}

func newServarrStub() *servarrStub {
	return &servarrStub{indexers: map[int]servarrIndexer{}}
}

func (s *servarrStub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/indexer", s.list)
	mux.HandleFunc("POST /api/v3/indexer", s.create)
	mux.HandleFunc("POST /api/v3/indexer/test", s.test)
	mux.HandleFunc("PUT /api/v3/indexer/{id}", s.put)
	mux.HandleFunc("DELETE /api/v3/indexer/{id}", s.delete)
	return mux
}

func (s *servarrStub) record(r *http.Request) servarrIndexer {
	s.lastAuth = r.Header.Get("X-Api-Key")
	body, _ := readAll(r)
	s.lastBody = body
	var idx servarrIndexer
	_ = json.Unmarshal(body, &idx)
	return idx
}

func (s *servarrStub) list(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]servarrIndexer, 0, len(s.indexers))
	for _, idx := range s.indexers {
		out = append(out, idx)
	}
	writeJSONTest(w, http.StatusOK, out)
}

func (s *servarrStub) create(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.record(r)
	s.nextID++
	idx.ID = s.nextID
	s.indexers[idx.ID] = idx
	writeJSONTest(w, http.StatusCreated, idx)
}

func (s *servarrStub) put(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.record(r)
	id, _ := strconv.Atoi(r.PathValue("id"))
	idx.ID = id
	s.indexers[id] = idx
	writeJSONTest(w, http.StatusOK, idx)
}

func (s *servarrStub) delete(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.indexers, atoi(r.PathValue("id")))
	w.WriteHeader(http.StatusOK)
}

func (s *servarrStub) test(w http.ResponseWriter, r *http.Request) {
	s.record(r)
	if s.testFail {
		writeJSONTest(w, http.StatusBadRequest, map[string]string{"message": "Unable to connect to indexer"})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func TestServarrLifecycle(t *testing.T) {
	t.Parallel()
	stub := newServarrStub()
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	ctx := context.Background()

	drv := NewSonarr(srv.URL, "app-key-123", srv.Client())

	// Create.
	id, err := drv.Create(ctx, desired("show-tracker", true))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "1" {
		t.Fatalf("Create id = %q, want 1", id)
	}
	if stub.lastAuth != "app-key-123" {
		t.Errorf("X-Api-Key = %q, want app-key-123", stub.lastAuth)
	}

	// List recovers the harbrr slug from the pushed feed URL.
	remote, err := drv.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(remote) != 1 || remote[0].ManagedBySlug != "show-tracker" || remote[0].RemoteID != "1" {
		t.Fatalf("List = %+v, want one managed row slug=show-tracker id=1", remote)
	}

	// Update sends the id in body + path.
	if err := drv.Update(ctx, "1", desired("show-tracker", false)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var sent servarrIndexer
	_ = json.Unmarshal(stub.lastBody, &sent)
	if sent.ID != 1 || sent.EnableRss {
		t.Errorf("Update body id=%d enableRss=%v, want id=1 disabled", sent.ID, sent.EnableRss)
	}

	// Test posts to /test and reports success.
	if err := drv.Test(ctx, desired("show-tracker", true)); err != nil {
		t.Fatalf("Test: %v", err)
	}
	stub.testFail = true
	if err := drv.Test(ctx, desired("show-tracker", true)); err == nil {
		t.Error("Test should surface a 4xx as an error")
	}

	// Delete.
	if err := drv.Delete(ctx, "1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if remote, _ := drv.List(ctx); len(remote) != 0 {
		t.Errorf("indexer survived Delete: %+v", remote)
	}
}

func TestSonarrBuildIndexerGolden(t *testing.T) {
	t.Parallel()
	drv := newServarr("sonarr", "http://sonarr:8989", "app-key", nil, true)
	d := DesiredIndexer{
		Slug: "anime-tracker", Name: "Anime Tracker", Priority: 25, Enabled: true,
		FeedURL:    "http://harbrr:8787/api/v2.0/indexers/anime-tracker/results/torznab",
		APIKey:     "harbrr-feed-key",
		Categories: []Category{{5000, "TV"}, {5040, "TV/HD"}, {5070, "TV/Anime"}, {2000, "Movies"}},
	}
	assertGolden(t, "sonarr_create.golden.json", drv.buildIndexer(d))
}

// --- shared test helpers ---

func assertGolden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run -update to create): %v", name, err)
	}
	if string(got) != string(want) {
		t.Errorf("golden %s mismatch:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func writeJSONTest(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readAll(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
