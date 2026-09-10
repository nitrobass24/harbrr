package newznab

import (
	"context"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// TestCapsBodyReadErrorSurfacesCause mirrors the search-path test for the caps fetch: a
// mid-body read failure carries ErrBodyRead (transport, #234) and the real cause.
func TestCapsBodyReadErrorSurfacesCause(t *testing.T) {
	t.Parallel()
	d, err := New(native.Params{
		Def:     genericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey},
		Doer:    &bodyErrDoer{readErr: errors.New("connection reset by peer")},
		BaseURL: "https://news.example.test",
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	capsErr := d.Test(context.Background())
	if errors.Is(capsErr, search.ErrParseError) {
		t.Fatalf("err = %v, must NOT be ErrParseError (mid-body reads are transport, #234)", capsErr)
	}
	if !errors.Is(capsErr, native.ErrBodyRead) {
		t.Fatalf("err = %v, want errors.Is(native.ErrBodyRead)", capsErr)
	}
	if !strings.Contains(capsErr.Error(), "connection reset by peer") {
		t.Fatalf("err = %q, want the real read cause included (not a bare parse_error)", capsErr.Error())
	}
	assertNoApikey(t, "caps body-read error", capsErr.Error())
}

// TestCapsTransportErrorRedactsApikey mirrors the search-path redaction for the caps fetch:
// a real *url.Error whose URL hides the apikey in a path segment and query param surfaces
// only the endpoint's scheme://host through getCaps' wrap, never the apikey.
func TestCapsTransportErrorRedactsApikey(t *testing.T) {
	t.Parallel()
	const baseURL = "https://news.example.test"
	uerr := &url.Error{
		Op:  "Get",
		URL: baseURL + "/dl/" + testAPIKey + "?apikey=" + testAPIKey,
		Err: errors.New("dial tcp: connection refused"),
	}
	d, err := New(native.Params{
		Def:     genericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey},
		Doer:    &errorDoer{err: uerr},
		BaseURL: baseURL,
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	capsErr := d.Test(context.Background())
	if capsErr == nil {
		t.Fatal("Test err = nil, want a transport error")
	}
	got := capsErr.Error()
	if !strings.Contains(got, baseURL) {
		t.Errorf("error dropped the endpoint host; got %q, want it to contain %q", got, baseURL)
	}
	if strings.Contains(got, "/dl/"+testAPIKey) || strings.Contains(got, "apikey="+testAPIKey) {
		t.Errorf("error leaked the secret path/query: %q", got)
	}
	assertNoApikey(t, "caps transport error", got)
}

// TestCapsFetchCachesAndPrimesAtTest proves Test() fetches caps once, caches them, and a
// subsequent Capabilities()/Search() within the TTL serves from cache without a refetch.
func TestCapsFetchCachesAndPrimesAtTest(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	d, _ := capsServerDriver(t, &hits, nil)

	if err := d.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("caps fetches after Test = %d, want 1", hits.Load())
	}
	// Capabilities() within the TTL is served from cache: no extra fetch.
	if caps := d.Capabilities(); caps.Modes["movie-search"] == nil {
		t.Error("Capabilities() did not return the fetched caps")
	}
	if hits.Load() != 1 {
		t.Errorf("caps fetches after cached Capabilities() = %d, want still 1", hits.Load())
	}
}

// TestCapsLazyFetchOnCapabilities proves a cold Capabilities() (no Test() first) lazily
// fetches caps.
func TestCapsLazyFetchOnCapabilities(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	d, _ := capsServerDriver(t, &hits, nil)

	caps := d.Capabilities()
	if caps.Modes["tv-search"] == nil {
		t.Error("lazy Capabilities() did not fetch the live caps")
	}
	if hits.Load() != 1 {
		t.Errorf("caps fetches = %d, want 1 (lazy on first Capabilities)", hits.Load())
	}
}

// TestCapsTTLRefresh proves the cache refreshes once the fetched-at is older than the TTL,
// driven by the (test-controlled) clock.
func TestCapsTTLRefresh(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	now := time.Unix(1_700_000_000, 0).UTC()
	clock := func() time.Time { return now }
	d, _ := capsServerDriverClock(t, &hits, nil, func() time.Time { return clock() })

	d.Capabilities() // fetch #1 at t0
	if hits.Load() != 1 {
		t.Fatalf("after first Capabilities, hits = %d, want 1", hits.Load())
	}
	now = now.Add(native.CapsTTL - time.Hour) // still fresh
	d.Capabilities()
	if hits.Load() != 1 {
		t.Errorf("within TTL hits = %d, want still 1", hits.Load())
	}
	now = now.Add(2 * time.Hour) // now past the TTL
	d.Capabilities()
	if hits.Load() != 2 {
		t.Errorf("past TTL hits = %d, want a refresh to 2", hits.Load())
	}
}

// TestCapsFallbackToPlaceholderOnFetchError proves a cold-cache fetch failure does not strand
// Capabilities(): it falls back to the placeholder standard table (non-nil), so the indexer
// stays addable.
func TestCapsFallbackToPlaceholderOnFetchError(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	d, _ := capsServerDriver(t, &hits, func(w stdhttp.ResponseWriter) {
		w.WriteHeader(stdhttp.StatusInternalServerError)
	})
	caps := d.Capabilities()
	if caps == nil {
		t.Fatal("Capabilities() = nil on fetch error, want the placeholder fallback")
		return // unreachable after Fatal; makes the non-nil guarantee explicit for staticcheck
	}
	// The placeholder advertises every standard parent (e.g. Books) the live caps did not.
	if len(caps.CategoryMap.MapTrackerCatToNewznab("7000")) == 0 {
		t.Error("fallback caps missing the placeholder standard table")
	}
}

// TestCapsAuthErrorEnvelope proves a Newznab <error> envelope (HTTP 200) during the caps
// fetch surfaces as a login failure (so Test() reports bad credentials).
func TestCapsAuthErrorEnvelope(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	d, _ := capsServerDriver(t, &hits, func(w stdhttp.ResponseWriter) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><error code="100" description="Incorrect user credentials"/>`))
	})
	if err := d.Test(context.Background()); !errors.Is(err, login.ErrLoginFailed) {
		t.Fatalf("Test on auth error = %v, want login.ErrLoginFailed", err)
	}
}

// TestCapsURLCarriesApikeyButRedacts proves the caps URL carries the apikey (so the server
// authenticates) but a transport error redacts it.
func TestCapsURLCarriesApikeyButRedacts(t *testing.T) {
	t.Parallel()
	d := urlDriver(t)
	raw := d.buildAPIURL("caps")
	if !strings.Contains(raw, "t=caps") || !strings.Contains(raw, "apikey="+testAPIKey) {
		t.Fatalf("caps URL = %q, want t=caps and the apikey", redact(raw))
	}
	assertNoApikey(t, "redacted caps URL", redact(raw))
}

// TestCapsPersistAndRehydrate proves the fetched caps XML + fetched-at are written back
// through PersistSetting, and that a fresh driver constructed with those persisted settings
// serves the caps from cache without a network fetch (the cross-restart path).
func TestCapsPersistAndRehydrate(t *testing.T) {
	t.Parallel()
	stored := map[string]string{}
	var hits atomic.Int64
	golden := readGolden(t, "caps.xml")
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.URL.Query().Get("t") == "caps" {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(golden)
	}))
	t.Cleanup(srv.Close)

	persist := func(_ context.Context, name, value string) error {
		stored[name] = value
		return nil
	}
	d1, err := New(native.Params{
		Def:            genericDefinition(),
		Cfg:            map[string]string{"apikey": testAPIKey, "apiPath": "/api"},
		Doer:           srv.Client(),
		BaseURL:        srv.URL,
		Clock:          fixedClock,
		PersistSetting: persist,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d1.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if stored[native.SettingCapsCache] == "" || stored[native.SettingCapsFetchedAt] == "" {
		t.Fatalf("persist did not store caps cache: %+v", stored)
	}

	// A new driver constructed from the persisted settings serves caps from the cache with
	// NO further fetch.
	cfg := map[string]string{
		"apikey":                    testAPIKey,
		"apiPath":                   "/api",
		native.SettingCapsCache:     stored[native.SettingCapsCache],
		native.SettingCapsFetchedAt: stored[native.SettingCapsFetchedAt],
	}
	hits.Store(0)
	d2, err := New(native.Params{
		Def:     genericDefinition(),
		Cfg:     cfg,
		Doer:    srv.Client(),
		BaseURL: srv.URL,
		Clock:   fixedClock,
	})
	if err != nil {
		t.Fatalf("New (rehydrate): %v", err)
	}
	if caps := d2.Capabilities(); caps.Modes["movie-search"] == nil {
		t.Error("rehydrated Capabilities() missing the persisted caps")
	}
	if hits.Load() != 0 {
		t.Errorf("rehydrated driver fetched caps %d times, want 0 (served from persisted cache)", hits.Load())
	}
}

// capsServerDriver wires a driver to an offline server that serves the caps golden (counting
// hits) unless override writes a custom response.
func capsServerDriver(t *testing.T, hits *atomic.Int64, override func(stdhttp.ResponseWriter)) (*driver, *httptest.Server) {
	t.Helper()
	return capsServerDriverClock(t, hits, override, fixedClock)
}

func capsServerDriverClock(t *testing.T, hits *atomic.Int64, override func(stdhttp.ResponseWriter), clock func() time.Time) (*driver, *httptest.Server) {
	t.Helper()
	golden := readGolden(t, "caps.xml")
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.URL.Query().Get("t") == "caps" {
			hits.Add(1)
		}
		if override != nil {
			override(w)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(golden)
	}))
	t.Cleanup(srv.Close)
	d, err := New(native.Params{
		Def:     genericDefinition(),
		Cfg:     map[string]string{"apikey": testAPIKey, "apiPath": "/api"},
		Doer:    srv.Client(),
		BaseURL: srv.URL,
		Clock:   clock,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d.(*driver), srv
}
