package registry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/database"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
	"github.com/autobrr/harbrr/internal/web/torznabhttp"
)

// dlRegPasskey is a synthetic passkey-shaped value (built by concatenation so the
// literal never appears in source — allowlisted for *_test.go) that must never
// reach the served feed, on a miss OR a hit.
var dlRegPasskey = strings.Repeat("7c6d", 8)

const dlRegAPIKey = "harbrr-reg-key" //nolint:gosec // G101: synthetic test API key.

// resolverFakeIndexer is a minimal core.Indexer whose download needs resolving,
// so its passkey-bearing link must be sealed behind the /dl proxy. It counts Search
// calls so the test can prove the second poll is a cache hit (inner not re-called).
type resolverFakeIndexer struct {
	caps     *mapper.Capabilities
	releases []*normalizer.Release
	calls    int
}

func (f *resolverFakeIndexer) Info() core.IndexerInfo {
	return core.IndexerInfo{ID: "demo", Name: "Demo", Type: "private"}
}
func (f *resolverFakeIndexer) Capabilities() *mapper.Capabilities { return f.caps }
func (f *resolverFakeIndexer) NeedsResolver() bool                { return true }
func (f *resolverFakeIndexer) DownloadNeedsAuth() bool            { return false }
func (f *resolverFakeIndexer) SupportsOffsetPaging() bool         { return false }

func (f *resolverFakeIndexer) Search(context.Context, search.Query) ([]*normalizer.Release, error) {
	f.calls++
	return f.releases, nil
}

func (f *resolverFakeIndexer) Grab(context.Context, string) (*search.GrabResult, error) {
	return &search.GrabResult{Body: []byte("d0:e"), ContentType: "application/x-bittorrent"}, nil
}

// regCaps builds capabilities the handler needs to map the query + serialize.
func regCaps(t *testing.T) *mapper.Capabilities {
	t.Helper()
	caps, err := mapper.Build(&loader.Definition{
		ID:    "demo",
		Links: []string{"https://demo.test/"},
		Caps: loader.Caps{
			CategoryMappings: []loader.CategoryMapping{
				{ID: loader.Scalar{Value: "1", Set: true}, Cat: "Movies"},
			},
			Modes: loader.Modes{Search: []string{"q"}, MovieSearch: []string{"q", "imdbid"}},
		},
	})
	if err != nil {
		t.Fatalf("mapper.Build: %v", err)
	}
	return caps
}

// TestCachedResolverLinkSealedOnHit is the regression guard: a resolver-needing
// indexer wrapped by the search cache must still seal its passkey behind a /dl URL
// on a cache HIT (the cached value is the PRE-/dl slice, and rewriting runs
// downstream on every hit). Without that, a cached entry could serve the raw
// passkey-bearing link.
func TestCachedResolverLinkSealedOnHit(t *testing.T) {
	t.Parallel()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	instID := insertRegInstance(t, db)

	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	sc := registry.NewSearchCacheForTest(db, func() time.Time { return now })

	inner := &resolverFakeIndexer{
		caps:     regCaps(t),
		releases: []*normalizer.Release{demoRegRelease()},
	}
	cached := registry.WrapForTest(sc, inner, instID)

	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: dlRegKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	h := torznabhttp.NewHandler(
		regProvider{"demo": cached},
		torznabhttp.WithAPIKey(dlRegAPIKey),
		torznabhttp.WithClock(func() time.Time { return now }),
		torznabhttp.WithDLToken(kr),
	)

	// First poll: cache MISS (inner called once), links sealed via /dl.
	firstBody := regDo(t, h)
	assertSealed(t, firstBody, "miss")

	// Second poll: cache HIT (inner NOT re-called), links STILL sealed via /dl.
	secondBody := regDo(t, h)
	assertSealed(t, secondBody, "hit")

	if inner.calls != 1 {
		t.Fatalf("inner Search called %d times, want 1 (second poll served from cache)", inner.calls)
	}
}

// assertSealed verifies the served feed routes the link through /dl with a token
// and never leaks the passkey.
func assertSealed(t *testing.T, body, label string) {
	t.Helper()
	if strings.Contains(body, dlRegPasskey) {
		t.Fatalf("[%s] passkey leaked into the served feed:\n%s", label, body)
	}
	if !strings.Contains(body, "/api/indexers/demo/dl?") || !strings.Contains(body, "token=") {
		t.Fatalf("[%s] resolver-needing link not routed through /dl:\n%s", label, body)
	}
}

func demoRegRelease() *normalizer.Release {
	return &normalizer.Release{
		Title:                "Movie A",
		Link:                 "https://demo.test/download.php?id=1&passkey=" + dlRegPasskey,
		Size:                 1024,
		Categories:           []int{2000},
		Seeders:              1,
		Peers:                1,
		PublishDate:          "2024-01-02T03:04:05Z",
		DownloadVolumeFactor: 1,
		UploadVolumeFactor:   1,
	}
}

type regProvider map[string]core.Indexer

func (p regProvider) Indexer(_ context.Context, id string) (core.Indexer, bool) {
	i, ok := p[id]
	return i, ok
}

func regDo(t *testing.T, h http.Handler) string {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/indexers/demo/results/torznab?t=search&q=movie&apikey="+dlRegAPIKey, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

func insertRegInstance(t *testing.T, db *database.DB) int64 {
	t.Helper()
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z07:00")
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO indexer_instances (slug, definition_id, name, base_url, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 1, ?, ?)`,
		"demo", "demodef", "Demo", "", now, now)
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

// dlRegKey is a synthetic 32-byte AES key for this test only.
const dlRegKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
