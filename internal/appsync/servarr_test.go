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
	"strings"
	"sync"
	"testing"
)

var update = flag.Bool("update", false, "regenerate golden files")

// servarrStub is an in-memory Sonarr/Radarr v3 indexer API for driver tests. It
// records the last request body and auth header, assigns ids on create, and serves
// list/update/delete/test with the real status codes.
type servarrStub struct {
	t          *testing.T
	mu         sync.Mutex
	indexers   map[int]servarrIndexer
	nextID     int
	lastBody   []byte
	lastAuth   string
	lastQuery  string
	testFail   bool
	createFail any // when non-nil, create returns 400 with this JSON body
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
	s.lastQuery = r.URL.RawQuery
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
	if s.createFail != nil {
		writeJSONTest(w, http.StatusBadRequest, s.createFail)
		return
	}
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

// TestServarrForceSave proves Create/Update pass ?forceSave=true (Prowlarr parity) so
// Servarr's add-time validation test doesn't hard-fail a marginal indexer.
func TestServarrForceSave(t *testing.T) {
	t.Parallel()
	stub := newServarrStub(t)
	srv := httptest.NewServer(stub.handler("/api/v3/indexer"))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	drv := NewSonarr(srv.URL, "app-key-123", srv.Client())

	id, err := drv.Create(ctx, desired("show-tracker", true))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if stub.lastQuery != "forceSave=true" {
		t.Errorf("create query = %q, want forceSave=true", stub.lastQuery)
	}
	if err := drv.Update(ctx, id, desired("show-tracker", true)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if stub.lastQuery != "forceSave=true" {
		t.Errorf("update query = %q, want forceSave=true", stub.lastQuery)
	}
}

// TestServarrSurfacesRedactedReason proves a 400 now carries Servarr's own validation
// message (so the operator can see *why*), with any echoed feed key scrubbed.
func TestServarrSurfacesRedactedReason(t *testing.T) {
	t.Parallel()
	stub := newServarrStub(t)
	// Servarr's validation-failure array shape, whose errorMessage echoes the submitted
	// feed URL carrying the harbrr key — exactly what must not leak.
	stub.createFail = []map[string]any{{
		"propertyName": "",
		"errorMessage": "Query successful, but no results in the configured categories; probed https://harbrr.local/feed?apikey=SUPERSECRETKEY",
		"severity":     "warning",
	}}
	srv := httptest.NewServer(stub.handler("/api/v3/indexer"))
	t.Cleanup(srv.Close)
	drv := NewSonarr(srv.URL, "app-key-123", srv.Client())

	_, err := drv.Create(context.Background(), desired("show-tracker", true))
	if err == nil {
		t.Fatal("Create should surface a 400 as an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no results in the configured categories") {
		t.Errorf("error should carry Servarr's reason, got: %s", msg)
	}
	if !strings.Contains(msg, "status 400") {
		t.Errorf("error should keep the status, got: %s", msg)
	}
	if strings.Contains(msg, "SUPERSECRETKEY") {
		t.Errorf("feed key leaked into the surfaced error: %s", msg)
	}
}

func TestSonarrBuildIndexerGolden(t *testing.T) {
	t.Parallel()
	drv := asServarr(t, NewSonarr("http://sonarr:8989", "app-key", nil))
	d := DesiredIndexer{
		Slug: "anime-tracker", Name: "Anime Tracker", Priority: 25, Enabled: true,
		EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
		FeedURL:    "http://harbrr:8787/api/indexers/anime-tracker/results/torznab",
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
		EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
		FeedURL:    "http://harbrr:8787/api/indexers/anime-tracker/results/torznab",
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

// TestSonarrBuildIndexerProfileGolden freezes a sync-profile body: mixed search-mode
// toggles (rss off, automatic on, interactive off) and a minimumSeeders floor on a
// torrent indexer.
func TestSonarrBuildIndexerProfileGolden(t *testing.T) {
	t.Parallel()
	drv := asServarr(t, NewSonarr("http://sonarr:8989", "app-key", nil))
	d := DesiredIndexer{
		Slug: "anime-tracker", Name: "Anime Tracker", Priority: 25, Enabled: true,
		EnableRss: false, EnableAutomaticSearch: true, EnableInteractiveSearch: false,
		MinSeeders: 3,
		FeedURL:    "http://harbrr:8787/api/indexers/anime-tracker/results/torznab",
		APIKey:     "harbrr-feed-key",
		Categories: []Category{{5000, "TV"}, {5040, "TV/HD"}, {5070, "TV/Anime"}},
	}
	assertGolden(t, "sonarr_create_profile.golden.json", drv.buildIndexer(d))
}

// TestServarrMinSeedersTorrentOnly proves minimumSeeders rides only the torrent branch:
// present (with the profile's value) on Torznab, absent on Newznab/usenet, and absent
// when unset (0 = the app default).
func TestServarrMinSeedersTorrentOnly(t *testing.T) {
	t.Parallel()
	drv := asServarr(t, NewRadarr("http://radarr:7878", "k", nil))
	base := DesiredIndexer{Slug: "s", Name: "s", MinSeeders: 7, Categories: []Category{{2000, "Movies"}}}

	if got := fieldInt(drv.buildIndexer(base).Fields, "minimumSeeders"); got != 7 {
		t.Errorf("torrent minimumSeeders = %d, want 7", got)
	}

	usenet := base
	usenet.Protocol = "usenet"
	if hasField(drv.buildIndexer(usenet).Fields, "minimumSeeders") {
		t.Error("usenet (Newznab) indexer must not carry minimumSeeders even when MinSeeders>0")
	}

	base.MinSeeders = 0
	if hasField(drv.buildIndexer(base).Fields, "minimumSeeders") {
		t.Error("MinSeeders 0 must omit minimumSeeders (the app default)")
	}
}

// fieldInt reads a named int field's value, or -1 when absent.
func fieldInt(fields []servarrField, name string) int {
	for _, f := range fields {
		if f.Name == name {
			var v int
			if err := json.Unmarshal(f.Value, &v); err != nil {
				return -1
			}
			return v
		}
	}
	return -1
}

func hasField(fields []servarrField, name string) bool {
	for _, f := range fields {
		if f.Name == name {
			return true
		}
	}
	return false
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
		FeedURL: "http://harbrr:8787/api/indexers/usenet-tracker/results/torznab",
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
