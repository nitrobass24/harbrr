package torznabhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/secrets"
)

// aggIndexer builds a member for the aggregate tests: id/name, the modes it
// advertises, and one release dated at pubDate so merge order is assertable.
func aggIndexer(t *testing.T, id string, modes loader.Modes, pubDate string) *fakeIndexer {
	t.Helper()
	caps, err := mapper.Build(&loader.Definition{
		ID: id, Links: []string{"https://" + id + ".test/"},
		Caps: loader.Caps{
			CategoryMappings: []loader.CategoryMapping{
				{ID: loader.Scalar{Value: "1", Set: true}, Cat: "Movies"},
			},
			Modes: modes,
		},
	})
	if err != nil {
		t.Fatalf("mapper.Build(%s): %v", id, err)
	}
	rel := demoRelease(id+" release", "https://"+id+".test/dl/1.torrent", []int{2000})
	rel.PublishDate = pubDate
	return &fakeIndexer{
		info:     core.IndexerInfo{ID: id, Name: strings.ToUpper(id), Description: id, SiteLink: "https://" + id + ".test/", Type: "private"},
		caps:     caps,
		releases: []*normalizer.Release{rel},
	}
}

var searchOnly = loader.Modes{Search: []string{"q"}}

// doAll issues an aggregate-feed GET against the members in p.
func doAll(t *testing.T, p fakeProvider, rawQuery string, opts ...Option) *httptest.ResponseRecorder {
	t.Helper()
	opts = append([]Option{
		WithAPIKey(testAPIKey),
		WithClock(func() time.Time { return time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC) }),
	}, opts...)
	h := NewHandler(p, opts...)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/indexers/all/results/torznab?"+rawQuery+"&apikey="+testAPIKey, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// memberElem is the ledger line for id, e.g. `<harbrr:member id="a" ... />`.
func memberElem(t *testing.T, body, id string) string {
	t.Helper()
	for line := range strings.SplitSeq(body, "<harbrr:member ") {
		if strings.HasPrefix(line, `id="`+id+`"`) {
			return "<harbrr:member " + line[:strings.Index(line, ">")+1]
		}
	}
	t.Fatalf("no ledger entry for %q in:\n%s", id, body)
	return ""
}

// TestAggregatePartialFanout is the defining property of the aggregate feed: a member
// that fails contributes nothing and NEVER fails the request.
func TestAggregatePartialFanout(t *testing.T) {
	t.Parallel()
	good := aggIndexer(t, "good", searchOnly, "2024-01-02T03:04:05Z")
	bad := aggIndexer(t, "bad", searchOnly, "2024-01-03T03:04:05Z")
	bad.searchErr = errors.New("tracker exploded at https://bad.test/x?passkey=deadbeef")

	rec := doAll(t, fakeProvider{"good": good, "bad": bad}, "t=search&q=movie")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a member failure must never fail the request)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "good release") {
		t.Errorf("the healthy member's rows must still be served:\n%s", body)
	}
	if strings.Contains(body, "bad release") {
		t.Errorf("the failed member must contribute nothing:\n%s", body)
	}
	if got := memberElem(t, body, "bad"); !strings.Contains(got, `status="skipped"`) || !strings.Contains(got, `reason="error"`) {
		t.Errorf("bad ledger entry = %s", got)
	}
	if got := memberElem(t, body, "good"); !strings.Contains(got, `status="ok"`) || !strings.Contains(got, `count="1"`) {
		t.Errorf("good ledger entry = %s", got)
	}
	// The ledger is served to the consumer: it must never carry the failure detail.
	if strings.Contains(body, "passkey") || strings.Contains(body, "exploded") {
		t.Errorf("a raw member error leaked into the served ledger:\n%s", body)
	}
	if total := `total="1"`; !strings.Contains(body, total) {
		t.Errorf("total must be the merged count actually fetched (%s):\n%s", total, body)
	}
}

// TestAggregateAllMembersFailing: every member down is still a valid, empty 200 feed.
func TestAggregateAllMembersFailing(t *testing.T) {
	t.Parallel()
	a := aggIndexer(t, "a", searchOnly, "2024-01-02T03:04:05Z")
	b := aggIndexer(t, "b", searchOnly, "2024-01-02T03:04:05Z")
	a.searchErr = errors.New("boom")
	b.searchErr = errors.New("boom")

	rec := doAll(t, fakeProvider{"a": a, "b": b}, "t=search&q=movie")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<item>") {
		t.Errorf("expected an empty feed:\n%s", body)
	}
	if !strings.Contains(body, `total="0"`) {
		t.Errorf("total must be 0:\n%s", body)
	}
	for _, id := range []string{"a", "b"} {
		if got := memberElem(t, body, id); !strings.Contains(got, `status="skipped"`) {
			t.Errorf("%s ledger entry = %s", id, got)
		}
	}
}

// TestAggregateGateSkipReasons pins the ledger's reason vocabulary for the gates that
// are the whole reason harbrr can ship an aggregate feed the incumbent cannot recommend.
func TestAggregateGateSkipReasons(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"budget", fmt.Errorf("registry: search %q: %w", "b", core.ErrBudgetExhausted), core.SkipBudget},
		{"breaker", fmt.Errorf("wrapped: %w", core.ErrCircuitOpen), core.SkipCircuit},
		{"degenerate query", fmt.Errorf("registry: search %q: %w", "b", core.ErrDegenerateQuery), core.SkipDegenerateQuery},
		{"rate limited", fmt.Errorf("wrapped: %w", search.ErrRateLimited), core.SkipRateLimited},
		{"gateway", fmt.Errorf("wrapped: %w", search.ErrGatewayStatus), core.SkipUnreachable},
		{"timeout", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), core.SkipTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			good := aggIndexer(t, "good", searchOnly, "2024-01-02T03:04:05Z")
			gated := aggIndexer(t, "gated", searchOnly, "2024-01-02T03:04:05Z")
			gated.searchErr = tt.err

			rec := doAll(t, fakeProvider{"good": good, "gated": gated}, "t=search&q=movie")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			if got := memberElem(t, body, "gated"); !strings.Contains(got, `reason="`+tt.want+`"`) {
				t.Errorf("ledger entry = %s, want reason %q", got, tt.want)
			}
			if !strings.Contains(body, "good release") {
				t.Errorf("the ungated member must still contribute:\n%s", body)
			}
		})
	}
}

// TestAggregateCapsAreTheUnion: t=caps on the aggregate advertises every mode ANY
// member offers, and the union of their categories.
func TestAggregateCapsAreTheUnion(t *testing.T) {
	t.Parallel()
	movies := aggIndexer(t, "movies", loader.Modes{Search: []string{"q"}, MovieSearch: []string{"q", "imdbid"}}, "2024-01-02T03:04:05Z")
	music := aggIndexer(t, "music", loader.Modes{Search: []string{"q"}, MusicSearch: []string{"q", "artist"}}, "2024-01-02T03:04:05Z")

	rec := doAll(t, fakeProvider{"movies": movies, "music": music}, "t=caps")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<movie-search available="yes" supportedParams="q,imdbid"`,
		`<music-search available="yes" supportedParams="q,artist"`,
		`<tv-search available="no"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("caps missing %q:\n%s", want, body)
		}
	}
}

// TestAggregateModeGating: a mode NO member supports is rejected outright, while a
// mode only SOME support fans out to those and records the rest as a capability skip.
func TestAggregateModeGating(t *testing.T) {
	t.Parallel()
	movies := aggIndexer(t, "movies", loader.Modes{Search: []string{"q"}, MovieSearch: []string{"q", "imdbid"}}, "2024-01-02T03:04:05Z")
	plain := aggIndexer(t, "plain", searchOnly, "2024-01-02T03:04:05Z")
	p := fakeProvider{"movies": movies, "plain": plain}

	rec := doAll(t, p, "t=music&q=x")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a mode no member supports must be rejected: status = %d", rec.Code)
	}

	rec = doAll(t, p, "t=movie&q=film")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "movies release") {
		t.Errorf("the supporting member must serve:\n%s", body)
	}
	if got := memberElem(t, body, "plain"); !strings.Contains(got, `reason="unsupported-mode"`) {
		t.Errorf("plain ledger entry = %s, want an unsupported-mode skip", got)
	}
	if plain.gotQuery.Keywords != "" {
		t.Errorf("a member that cannot serve the mode must not be searched at all")
	}
}

// TestAggregateMergedWindow: the merged set is publish-date descending across members
// and the request's limit/offset page THAT set, not each member's.
func TestAggregateMergedWindow(t *testing.T) {
	t.Parallel()
	older := aggIndexer(t, "older", searchOnly, "2024-01-01T00:00:00Z")
	newer := aggIndexer(t, "newer", searchOnly, "2024-06-01T00:00:00Z")
	p := fakeProvider{"older": older, "newer": newer}

	body := doAll(t, p, "t=search&q=x").Body.String()
	if i, j := strings.Index(body, "newer release"), strings.Index(body, "older release"); i < 0 || j < 0 || i > j {
		t.Errorf("merged feed must be publish-date descending across members:\n%s", body)
	}

	// offset pages the MERGED set: skipping one drops the newest, not one per member.
	body = doAll(t, p, "t=search&q=x&offset=1").Body.String()
	if strings.Contains(body, "newer release") || !strings.Contains(body, "older release") {
		t.Errorf("offset must page the merged set:\n%s", body)
	}
	if !strings.Contains(body, `offset="1"`) || !strings.Contains(body, `total="2"`) {
		t.Errorf("paging window must be reported honestly:\n%s", body)
	}
}

// TestAggregatePinsMemberWindow: however the aggregate is paged, each member is queried
// at the canonical first-page window — so an aggregate poll lands on exactly the cache
// key a per-indexer poll of the same query does, and costs the trackers nothing extra.
func TestAggregatePinsMemberWindow(t *testing.T) {
	t.Parallel()
	a := aggIndexer(t, "a", searchOnly, "2024-01-02T03:04:05Z")
	doAll(t, fakeProvider{"a": a}, "t=search&q=x&offset=40&limit=10")
	if a.gotQuery.Offset != 0 || a.gotQuery.Limit != 100 {
		t.Errorf("member window = offset %d limit %d, want 0/100", a.gotQuery.Offset, a.gotQuery.Limit)
	}
}

// TestAggregateGrabsBindToOrigin is the passkey-adjacent correctness gate: a release
// fanned out from member B carries B's OWN /dl URL and a token that resolves through B
// — never the aggregate's — and the aggregate slug itself resolves no download at all.
func TestAggregateGrabsBindToOrigin(t *testing.T) {
	t.Parallel()
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: dlTestKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	a := aggIndexer(t, "a", searchOnly, "2024-01-01T00:00:00Z")
	b := aggIndexer(t, "b", searchOnly, "2024-06-01T00:00:00Z")
	bLink := "https://b.test/download.php?id=1&passkey=" + dlTestPasskey
	for _, idx := range []*fakeIndexer{a, b} {
		idx.needsResolver = true
	}
	b.releases[0].Link = bLink
	b.releases[0].PublishDate = "2024-06-01T00:00:00Z"
	b.grabResult = &search.GrabResult{Body: []byte("d4:name1:bee"), ContentType: "application/x-bittorrent"}
	p := fakeProvider{"a": a, "b": b}

	body := doAll(t, p, "t=search&q=x", WithDLToken(kr)).Body.String()
	if strings.Contains(body, dlTestPasskey) {
		t.Fatalf("the passkey must never reach the feed:\n%s", body)
	}
	// b is the newest release, so it is the first item — and therefore the first token.
	if !strings.Contains(body, "/api/indexers/b/dl?") {
		t.Fatalf("b's release must carry b's OWN /dl base, not the aggregate's:\n%s", body)
	}
	if strings.Contains(body, "/api/indexers/all/dl") {
		t.Fatalf("no aggregate-slug /dl URL may ever be served:\n%s", body)
	}
	token := tokenOf(body)
	if token == "" {
		t.Fatal("no /dl token in the served feed")
	}

	h := NewHandler(p, WithAPIKey(testAPIKey), WithDLToken(kr),
		WithClock(func() time.Time { return time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC) }))

	// The token resolves through b, with b's own (passkey-bearing) link.
	rec := doDL(t, h, "b", "token="+url.QueryEscape(token))
	if rec.Code != http.StatusOK {
		t.Fatalf("grab through the origin member: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if b.gotGrabLink != bLink {
		t.Errorf("b.Grab got %q, want %q", b.gotGrabLink, bLink)
	}
	if a.gotGrabLink != "" {
		t.Errorf("the sibling member must never see the grab (got %q)", a.gotGrabLink)
	}

	// The same token presented to the sibling fails the AAD check...
	if rec := doDL(t, h, "a", "token="+url.QueryEscape(token)); rec.Code != http.StatusBadRequest {
		t.Errorf("a token minted for b must not resolve through a: status = %d", rec.Code)
	}
	// ...and the aggregate slug resolves no indexer, so it can never serve a download.
	if rec := doDL(t, h, "all", "token="+url.QueryEscape(token)); rec.Code != http.StatusNotFound {
		t.Errorf("all/dl status = %d, want 404", rec.Code)
	}
}

// TestPerIndexerFeedCarriesNoAggregateMarkup guards the byte-identity invariant at the
// place it could regress: the harbrr namespace and the ledger are aggregate-only, so a
// per-indexer feed must show no trace of either.
func TestPerIndexerFeedCarriesNoAggregateMarkup(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, demoIndexer(t))
	body := do(t, h, "t=search&q=movie").Body.String()
	for _, forbidden := range []string{"xmlns:harbrr", "harbrr:member"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("per-indexer feed must not carry %q:\n%s", forbidden, body)
		}
	}
}

// TestAggregateInheritsFreeleechBypass: the /full variant tags the request and every
// member's search inherits it, so the aggregate applies each member's freeleech view
// exactly as its own feed does — one of the per-indexer gates the fan-out must not skip.
func TestAggregateInheritsFreeleechBypass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"honor feed", "/api/indexers/all/results/torznab", false},
		{"bypass feed", "/api/indexers/all/results/torznab/full", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := aggIndexer(t, "a", searchOnly, "2024-01-02T03:04:05Z")
			b := aggIndexer(t, "b", searchOnly, "2024-01-02T03:04:05Z")
			h := NewHandler(fakeProvider{"a": a, "b": b}, WithAPIKey(testAPIKey),
				WithClock(func() time.Time { return time.Date(2026, time.June, 13, 12, 0, 0, 0, time.UTC) }))
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
				tt.path+"?t=search&q=x&apikey="+testAPIKey, nil)
			h.ServeHTTP(httptest.NewRecorder(), req)
			for _, m := range []*fakeIndexer{a, b} {
				if got := m.gotQuery.FreeleechBypass; got != tt.want {
					t.Errorf("%s FreeleechBypass = %v, want %v", m.info.ID, got, tt.want)
				}
			}
		})
	}
}

// TestAggregateEmptyMemberSet: an aggregate covering nothing is a valid empty feed,
// not an error — enabling the first indexer must not be the difference between a
// broken consumer and a working one.
func TestAggregateEmptyMemberSet(t *testing.T) {
	t.Parallel()
	rec := doAll(t, fakeProvider{}, "t=search&q=x")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "<item>") || !strings.Contains(body, `total="0"`) {
		t.Errorf("want an empty feed:\n%s", body)
	}
}
