package appsync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// quiStub is an in-memory qui Torznab-indexer API for driver tests.
type quiStub struct {
	mu       sync.Mutex
	indexers map[int]quiIndexer
	nextID   int
	lastBody []byte
	lastAuth string
	tested   []string
}

func newQuiStub() *quiStub { return &quiStub{indexers: map[int]quiIndexer{}} }

func (s *quiStub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/torznab/indexers", s.list)
	mux.HandleFunc("POST /api/torznab/indexers", s.create)
	mux.HandleFunc("PUT /api/torznab/indexers/{id}", s.put)
	mux.HandleFunc("DELETE /api/torznab/indexers/{id}", s.delete)
	mux.HandleFunc("POST /api/torznab/indexers/{id}/test", s.test)
	return mux
}

func (s *quiStub) record(r *http.Request) quiIndexer {
	s.lastAuth = r.Header.Get("X-API-Key")
	body, _ := readAll(r)
	s.lastBody = body
	var idx quiIndexer
	_ = json.Unmarshal(body, &idx)
	return idx
}

func (s *quiStub) list(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]quiIndexer, 0, len(s.indexers))
	for _, idx := range s.indexers {
		idx.APIKey = "" // qui never echoes the key back
		out = append(out, idx)
	}
	writeJSONTest(w, http.StatusOK, out)
}

func (s *quiStub) create(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.record(r)
	s.nextID++
	idx.ID = s.nextID
	s.indexers[idx.ID] = idx
	writeJSONTest(w, http.StatusCreated, idx)
}

func (s *quiStub) put(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.record(r)
	id := atoi(r.PathValue("id"))
	idx.ID = id
	s.indexers[id] = idx
	writeJSONTest(w, http.StatusOK, idx)
}

func (s *quiStub) delete(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.indexers, atoi(r.PathValue("id")))
	w.WriteHeader(http.StatusNoContent)
}

func (s *quiStub) test(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tested = append(s.tested, r.PathValue("id"))
	writeJSONTest(w, http.StatusOK, map[string]string{"status": "ok"})
}

func TestQuiLifecycle(t *testing.T) {
	t.Parallel()
	stub := newQuiStub()
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	ctx := context.Background()

	drv := NewQui(srv.URL, "qui-key", srv.Client())

	id, err := drv.Create(ctx, desired("native-tracker", true))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "1" || stub.lastAuth != "qui-key" {
		t.Fatalf("Create id=%q auth=%q, want 1 / qui-key", id, stub.lastAuth)
	}
	var sent quiIndexer
	_ = json.Unmarshal(stub.lastBody, &sent)
	if sent.Backend != quiBackendNative || sent.APIKey == "" {
		t.Errorf("create body backend=%q apiKey-empty=%v, want native + key present", sent.Backend, sent.APIKey == "")
	}

	remote, err := drv.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(remote) != 1 || remote[0].ManagedBySlug != "native-tracker" {
		t.Fatalf("List = %+v, want one managed row slug=native-tracker", remote)
	}

	if err := drv.Update(ctx, "1", desired("native-tracker", false)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := drv.Test(ctx, desired("native-tracker", true)); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(stub.tested) != 1 || stub.tested[0] != "1" {
		t.Errorf("Test hit ids %v, want [1] (resolved from slug)", stub.tested)
	}

	if err := drv.Delete(ctx, "1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if remote, _ := drv.List(ctx); len(remote) != 0 {
		t.Errorf("indexer survived Delete: %+v", remote)
	}
}

func TestQuiBuildIndexerGolden(t *testing.T) {
	t.Parallel()
	drv := &quiDriver{baseURL: "http://qui:7476", apiKey: "k"}
	d := DesiredIndexer{
		Slug: "native-tracker", Name: "Native Tracker", Priority: 10, Enabled: true,
		FeedURL:      "http://harbrr:8787/api/v2.0/indexers/native-tracker/results/torznab",
		APIKey:       "harbrr-feed-key",
		Categories:   []Category{{5000, "TV"}, {2000, "Movies"}},
		Capabilities: []string{"search", "search-q", "tv-search", "tv-search-q", "tv-search-season", "tv-search-ep", "movie-search", "movie-search-q", "movie-search-imdbid"},
	}
	assertGolden(t, "qui_create.golden.json", drv.buildIndexer(d))
}
