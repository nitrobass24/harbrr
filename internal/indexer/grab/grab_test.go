package grab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/core"
	"github.com/autobrr/harbrr/internal/secrets"
)

// fakeIndexer is a canned core.Indexer for the grab tests: it records the link it was
// asked to grab and returns whatever result or error the test set.
type fakeIndexer struct {
	info              core.IndexerInfo
	needsResolver     bool
	downloadNeedsAuth bool
	grabResult        *search.GrabResult // when set, Grab returns it
	grabErr           error
	gotGrabLink       string
}

func (f *fakeIndexer) Info() core.IndexerInfo             { return f.info }
func (f *fakeIndexer) Capabilities() *mapper.Capabilities { return nil }

func (f *fakeIndexer) Search(context.Context, search.Query) ([]*normalizer.Release, error) {
	return nil, nil
}

func (f *fakeIndexer) NeedsResolver() bool        { return f.needsResolver }
func (f *fakeIndexer) DownloadNeedsAuth() bool    { return f.downloadNeedsAuth }
func (f *fakeIndexer) SupportsOffsetPaging() bool { return false }
func (f *fakeIndexer) ConsumesSearchMode() bool   { return false }

func (f *fakeIndexer) Grab(_ context.Context, link string) (*search.GrabResult, error) {
	f.gotGrabLink = link
	if f.grabErr != nil {
		return nil, f.grabErr
	}
	if f.grabResult != nil {
		return f.grabResult, nil
	}
	return &search.GrabResult{Body: []byte("d0:e"), ContentType: torrentContentType}, nil
}

// testGrabError stands in for a caller's ErrorWriter. The feed's Torznab XML writer and
// the management API's JSON envelope both live in their own packages; what grab owns is
// the status and message it hands them, which is what these tests assert.
func testGrabError(w http.ResponseWriter, status int, msg string) {
	http.Error(w, msg, status)
}

// TestServeGrab exercises the shared resolve/stream core directly. Both the apikey-gated
// feed /dl proxy (the feed package's serveDL) and the session-authed management download
// route delegate to it; authorization is the caller's job, so it is called ungated here.
func TestServeGrab(t *testing.T) {
	t.Parallel()
	kr, err := secrets.OpenKeyring(secrets.KeyringOptions{EncryptionKey: dlTestKey}, zerolog.Nop())
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	tokenFor := func(t *testing.T, indexerID, title, link string) string {
		t.Helper()
		tok, err := encodeDLToken(kr, indexerID, 0, title, link)
		if err != nil {
			t.Fatalf("encode token: %v", err)
		}
		return tok
	}
	serve := func(t *testing.T, idx core.Indexer, dlToken *secrets.Keyring, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/download/"+token, nil)
		rec := httptest.NewRecorder()
		ServeGrab(rec, req, idx, dlToken, zerolog.Nop(), token, testGrabError)
		return rec
	}
	demo := &fakeIndexer{info: core.IndexerInfo{ID: "demo"}}

	t.Run("streams named attachments", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name        string
			title       string
			contentType string
			body        string
			wantCD      string
		}{
			{
				name:        "ordinary torrent title",
				title:       "Release.Name.2026",
				contentType: torrentContentType,
				body:        "d0:e",
				wantCD:      `attachment; filename="Release.Name.2026 [demo].torrent"`,
			},
			{
				name:        "NZB extension",
				title:       "Usenet Release",
				contentType: "application/x-nzb",
				body:        "<nzb></nzb>",
				wantCD:      `attachment; filename="Usenet Release [demo].nzb"`,
			},
			{
				// A plain-ASCII fallback rides along with the RFC 5987 form: a
				// consumer that predates filename* would otherwise see no
				// filename at all and save under the opaque token.
				name:        "Unicode dual filename",
				title:       "猫と犬",
				contentType: torrentContentType,
				body:        "d0:e",
				wantCD:      `attachment; filename="___ [demo].torrent"; filename*=utf-8''%E7%8C%AB%E3%81%A8%E7%8A%AC%20%5Bdemo%5D.torrent`,
			},
			{
				name:        "unsafe characters",
				title:       "Bad/Name\\With:Chars*?\"<>|\x00",
				contentType: torrentContentType,
				body:        "d0:e",
				wantCD:      `attachment; filename="BadNameWithChars [demo].torrent"`,
			},
			{
				name:        "reserved name",
				title:       "CON",
				contentType: torrentContentType,
				body:        "d0:e",
				wantCD:      `attachment; filename="CON_ [demo].torrent"`,
			},
			{
				// A title that sanitizes to nothing seals an empty stem, so the
				// indexer-ID fallback names the download — never pathologize's
				// generic "file" sentinel.
				name:        "traversal-like title falls back to indexer ID",
				title:       "..",
				contentType: torrentContentType,
				body:        "d0:e",
				wantCD:      `attachment; filename="demo.torrent"`,
			},
			{
				name:        "long UTF-8 title",
				title:       strings.Repeat("界", 100),
				contentType: torrentContentType,
				body:        "d0:e",
				wantCD: `attachment; filename="` + strings.Repeat("_", 80) + ` [demo].torrent"` +
					"; filename*=utf-8''" + strings.Repeat("%E7%95%8C", 80) + "%20%5Bdemo%5D.torrent",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				idx := &fakeIndexer{
					info:       core.IndexerInfo{ID: "demo"},
					grabResult: &search.GrabResult{Body: []byte(tt.body), ContentType: tt.contentType},
				}
				rec := serve(t, idx, kr, tokenFor(t, "demo", tt.title, "https://demo.test/x"))
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200", rec.Code)
				}
				if got := rec.Header().Get("Content-Type"); got != tt.contentType {
					t.Errorf("Content-Type = %q, want %q", got, tt.contentType)
				}
				if rec.Body.String() != tt.body {
					t.Errorf("body = %q, want %q", rec.Body.String(), tt.body)
				}
				if got := rec.Header().Get("Content-Disposition"); got != tt.wantCD {
					t.Errorf("Content-Disposition = %q, want %q", got, tt.wantCD)
				}
			})
		}
	})

	t.Run("same title from different indexers gets distinct filenames", func(t *testing.T) {
		t.Parallel()
		for _, indexerID := range []string{"alpha", "beta"} {
			idx := &fakeIndexer{
				info:       core.IndexerInfo{ID: indexerID},
				grabResult: &search.GrabResult{Body: []byte("d0:e"), ContentType: torrentContentType},
			}
			rec := serve(t, idx, kr, tokenFor(t, indexerID, "Release.Name.2026", "https://tracker.test/x"))
			want := `attachment; filename="Release.Name.2026 [` + indexerID + `].torrent"`
			if got := rec.Header().Get("Content-Disposition"); got != want {
				t.Errorf("%s Content-Disposition = %q, want %q", indexerID, got, want)
			}
		}
	})

	t.Run("strips DEL before writing attachment headers", func(t *testing.T) {
		t.Parallel()
		idx := &fakeIndexer{
			info:       core.IndexerInfo{ID: "demo"},
			grabResult: &search.GrabResult{Body: []byte("d0:e"), ContentType: torrentContentType},
		}
		token := tokenFor(t, "demo", "Bad\x7fName", "https://demo.test/x")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ServeGrab(w, r, idx, kr, zerolog.Nop(), token, testGrabError)
		}))
		t.Cleanup(server.Close)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatalf("download: %v", err)
		}
		defer resp.Body.Close()

		if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename="BadName [demo].torrent"` {
			t.Errorf("Content-Disposition = %q, want DEL-free filename", got)
		}
	})

	t.Run("falls back to the indexer for empty and legacy metadata", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name  string
			token string
		}{
			{name: "empty v2 metadata", token: tokenFor(t, "demo", "", "https://demo.test/x")},
			{name: "category-link legacy payload", token: sealedDLTestPayload(t, kr, "demo", "2000;https://demo.test/x")},
			{name: "bare-link legacy payload", token: sealedDLTestPayload(t, kr, "demo", "https://demo.test/x")},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				idx := &fakeIndexer{info: core.IndexerInfo{ID: "demo"}, grabResult: &search.GrabResult{Body: []byte("d0:e"), ContentType: torrentContentType}}
				rec := serve(t, idx, kr, tt.token)
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200", rec.Code)
				}
				if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="demo.torrent"` {
					t.Errorf("Content-Disposition = %q, want indexer fallback", got)
				}
			})
		}
	})

	t.Run("redirects a magnet (302)", func(t *testing.T) {
		t.Parallel()
		idx := &fakeIndexer{info: core.IndexerInfo{ID: "demo"}, grabResult: &search.GrabResult{Redirect: "magnet:?xt=urn:btih:abc"}}
		rec := serve(t, idx, kr, tokenFor(t, "demo", "Release.Name.2026", "https://demo.test/x"))
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "magnet:?xt=urn:btih:abc" {
			t.Errorf("Location = %q, want the magnet", loc)
		}
	})

	t.Run("rejects a malformed token (400)", func(t *testing.T) {
		t.Parallel()
		if rec := serve(t, demo, kr, "not-a-valid-token"); rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("failures answer through the caller's ErrorWriter", func(t *testing.T) {
		t.Parallel()
		// The management route injects the api package's JSON writer; this guards the
		// seam — a failure must reach the caller-supplied writer, not a hard-coded
		// Torznab XML document.
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/download/bad", nil)
		rec := httptest.NewRecorder()
		var gotStatus int
		var gotMsg string
		ServeGrab(rec, req, demo, kr, zerolog.Nop(), "not-a-valid-token", func(w http.ResponseWriter, status int, msg string) {
			gotStatus, gotMsg = status, msg
			w.WriteHeader(status)
		})
		if gotStatus != http.StatusBadRequest || gotMsg != "invalid download token" {
			t.Errorf("ErrorWriter got (%d, %q), want (400, \"invalid download token\")", gotStatus, gotMsg)
		}
	})

	t.Run("rejects a token minted for another indexer (400)", func(t *testing.T) {
		t.Parallel()
		// demo's ID is "demo"; a token bound to "other" fails the AAD check, not replayable.
		if rec := serve(t, demo, kr, tokenFor(t, "other", "Release.Name.2026", "https://demo.test/x")); rec.Code != http.StatusBadRequest {
			t.Errorf("cross-indexer token: status = %d, want 400", rec.Code)
		}
	})

	// A Grab failure is the same 900/500 shape as the search path (#307): status and
	// message-passing to the caller's ErrorWriter never change, but a wrapped
	// search.ErrGatewayStatus surfaces its sentinel text instead of the generic
	// InternalErrorMsg.
	t.Run("grab failure surfaces the gateway sentinel", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name    string
			err     error
			wantMsg string
		}{
			{"unrelated error stays generic", errors.New("grab failed: passkey=topsecret12345"), InternalErrorMsg},
			{"wrapped gateway status surfaces the sentinel", fmt.Errorf("tracker.test GET: %w", search.ErrGatewayStatus), search.ErrGatewayStatus.Error()},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				idx := &fakeIndexer{info: core.IndexerInfo{ID: "demo"}, grabErr: tt.err}
				req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/download/tok", nil)
				rec := httptest.NewRecorder()
				var gotStatus int
				var gotMsg string
				ServeGrab(rec, req, idx, kr, zerolog.Nop(), tokenFor(t, "demo", "Release.Name.2026", "https://demo.test/x"), func(w http.ResponseWriter, status int, msg string) {
					gotStatus, gotMsg = status, msg
					w.WriteHeader(status)
				})
				if gotStatus != http.StatusInternalServerError || gotMsg != tt.wantMsg {
					t.Errorf("ErrorWriter got (%d, %q), want (500, %q)", gotStatus, gotMsg, tt.wantMsg)
				}
			})
		}
	})

	t.Run("nil keyring is unavailable (503)", func(t *testing.T) {
		t.Parallel()
		if rec := serve(t, demo, nil, "tok"); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", rec.Code)
		}
	})

	t.Run("refuses a non-bencode torrent body (404)", func(t *testing.T) {
		t.Parallel()
		idx := &fakeIndexer{info: core.IndexerInfo{ID: "demo"}, grabResult: &search.GrabResult{Body: []byte("<html>login</html>"), ContentType: torrentContentType}}
		if rec := serve(t, idx, kr, tokenFor(t, "demo", "Release.Name.2026", "https://demo.test/x")); rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})
}

// TestLogInternalErrorLevel pins the one thing that keeps a live error visible: an open
// circuit is a self-imposed gate, so it must not be logged at error alongside genuine
// tracker failures. A tracker that stays down for days would otherwise emit an error
// every poll — 48 a day at a 30 minute interval — burying the failures worth acting on.
func TestLogInternalErrorLevel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			// The exact shape adapter.go returns when the gate refuses a dispatch.
			name: "circuit open in the adapter's until form is debug",
			err:  fmt.Errorf("%w until 2026-08-21T04:19:00Z", core.ErrCircuitOpen),
			want: "debug",
		},
		{
			name: "circuit open stays debug when wrapped by the search stack",
			err:  fmt.Errorf("torznab: search: registry: search %q: %w", "aura4k", core.ErrCircuitOpen),
			want: "debug",
		},
		{
			name: "a real tracker failure is still an error",
			err:  errors.New("tracker returned HTTP 530: gateway reported the origin unreachable"),
			want: "error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			LogInternalError(zerolog.New(&buf).Level(zerolog.DebugLevel), "search", "aura4k", tt.err)
			var rec struct {
				Level   string `json:"level"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("unmarshal log line %q: %v", buf.String(), err)
			}
			if rec.Level != tt.want {
				t.Errorf("level = %q, want %q (line: %s)", rec.Level, tt.want, buf.String())
			}
			if rec.Message != "indexer request failed" {
				t.Errorf("message = %q, want %q", rec.Message, "indexer request failed")
			}
		})
	}
}
