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
	t        *testing.T
	mu       sync.Mutex
	indexers map[int]servarrIndexer
	nextID   int
	lastBody []byte
	lastAuth string
	testFail bool
}

func newServarrStub(t *testing.T) *servarrStub {
	t.Helper()
	return &servarrStub{t: t, indexers: map[int]servarrIndexer{}}
}

func (s *servarrStub) handler(base string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+base, s.list)
	mux.HandleFunc("POST "+base, s.create)
	mux.HandleFunc("POST "+base+"/test", s.test)
	mux.HandleFunc("PUT "+base+"/{id}", s.put)
	mux.HandleFunc("DELETE "+base+"/{id}", s.delete)
	return mux
}

func (s *servarrStub) record(r *http.Request) servarrIndexer {
	s.lastAuth = r.Header.Get("X-Api-Key")
	body, _ := readAll(r)
	s.lastBody = body
	var idx servarrIndexer
	if err := json.Unmarshal(body, &idx); err != nil {
		s.t.Errorf("stub: decode request body: %v", err)
	}
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
	stub := newServarrStub(t)
	srv := httptest.NewServer(stub.handler("/api/v3/indexer"))
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
	if err := json.Unmarshal(stub.lastBody, &sent); err != nil {
		t.Fatalf("decode update body: %v", err)
	}
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
	remote, err = drv.List(ctx)
	if err != nil {
		t.Fatalf("List after Delete: %v", err)
	}
	if len(remote) != 0 {
		t.Errorf("indexer survived Delete: %+v", remote)
	}
}

func TestSonarrBuildIndexerGolden(t *testing.T) {
	t.Parallel()
	drv := asServarr(t, NewSonarr("http://sonarr:8989", "app-key", nil))
	d := DesiredIndexer{
		Slug: "anime-tracker", Name: "Anime Tracker", Priority: 25, Enabled: true,
		FeedURL:    "http://harbrr:8787/api/v2.0/indexers/anime-tracker/results/torznab",
		APIKey:     "harbrr-feed-key",
		Categories: []Category{{5000, "TV"}, {5040, "TV/HD"}, {5070, "TV/Anime"}, {2000, "Movies"}},
	}
	assertGolden(t, "sonarr_create.golden.json", drv.buildIndexer(d))
}

func TestSonarrBuildIndexerUsenetGolden(t *testing.T) {
	t.Parallel()
	drv := asServarr(t, NewSonarr("http://sonarr:8989", "app-key", nil))
	d := DesiredIndexer{
		Slug: "anime-tracker", Name: "Anime Tracker", Priority: 25, Enabled: true,
		FeedURL:    "http://harbrr:8787/api/v2.0/indexers/anime-tracker/results/torznab",
		APIKey:     "harbrr-feed-key",
		Categories: []Category{{5000, "TV"}, {5040, "TV/HD"}, {5070, "TV/Anime"}, {2000, "Movies"}},
		Protocol:   "usenet",
	}
	got := drv.buildIndexer(d)
	// Only the four header fields flip for usenet; fields[] stays identical to torrent.
	if got.Implementation != "Newznab" || got.ImplementationName != "Newznab" ||
		got.ConfigContract != "NewznabSettings" || got.Protocol != "usenet" {
		t.Errorf("usenet header wrong: impl=%q implName=%q cfg=%q proto=%q",
			got.Implementation, got.ImplementationName, got.ConfigContract, got.Protocol)
	}
	assertGolden(t, "sonarr_create_usenet.golden.json", got)
}

func TestServarrBuildIndexerTorrentUnchanged(t *testing.T) {
	t.Parallel()
	drv := asServarr(t, NewRadarr("http://radarr:7878", "app-key", nil))
	// Empty Protocol and explicit "torrent" both yield the unchanged Torznab body.
	for _, proto := range []string{"", "torrent"} {
		got := drv.buildIndexer(DesiredIndexer{Slug: "s", Name: "s", Protocol: proto})
		if got.Implementation != "Torznab" || got.ImplementationName != "Torznab" ||
			got.ConfigContract != "TorznabSettings" || got.Protocol != "torrent" {
			t.Errorf("protocol %q: torrent body changed: %+v", proto, got)
		}
	}
}

func TestServarrListRecognizesNewznab(t *testing.T) {
	t.Parallel()
	stub := newServarrStub(t)
	srv := httptest.NewServer(stub.handler("/api/v3/indexer"))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	drv := NewSonarr(srv.URL, "app-key", srv.Client())
	// Push a usenet (Newznab) indexer, then confirm List tags it harbrr-managed — a
	// missing Newznab case here would orphan it on the next full sync.
	if _, err := drv.Create(ctx, DesiredIndexer{
		Slug: "usenet-tracker", Name: "Usenet Tracker", Protocol: "usenet",
		FeedURL: "http://harbrr:8787/api/v2.0/indexers/usenet-tracker/results/torznab",
		APIKey:  "k",
	}); err != nil {
		t.Fatalf("Create usenet: %v", err)
	}
	remote, err := drv.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(remote) != 1 || remote[0].ManagedBySlug != "usenet-tracker" {
		t.Fatalf("List = %+v, want one managed Newznab row slug=usenet-tracker", remote)
	}
}

// --- shared test helpers ---

// asServarr unwraps a Target built by an exported NewX constructor back to the
// concrete *servarrDriver, so buildIndexer (and the app's anime/indexerPath wiring)
// is exercised through the real constructor rather than re-specified by the test.
func asServarr(t *testing.T, tgt Target) *servarrDriver {
	t.Helper()
	drv, ok := tgt.(*servarrDriver)
	if !ok {
		t.Fatalf("want *servarrDriver, got %T", tgt)
	}
	return drv
}

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
