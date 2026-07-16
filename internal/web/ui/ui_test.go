package ui_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/autobrr/harbrr/internal/web/ui"
)

const indexHTML = `<!doctype html><html><head><title>harbrr</title>` +
	`<script type="module" src="/assets/index-abc123.js"></script>` +
	`<link rel="stylesheet" href="/assets/index-abc123.css"></head>` +
	`<body><div id="root"></div></body></html>`

func builtFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":             {Data: []byte(indexHTML)},
		"assets/index-abc123.js": {Data: []byte("console.log('app')")},
		"assets/index-abc123.css": {
			Data: []byte("body{}"),
		},
		"favicon.svg": {Data: []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"/>")},
	}
}

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, target, nil))
	return rec
}

func TestHandlerServesBundle(t *testing.T) {
	t.Parallel()
	h := ui.NewHandler(builtFS(), "", "v1.2.3", "https://harbrr.example.com")

	tests := []struct {
		name         string
		method       string
		target       string
		wantStatus   int
		wantType     string // Content-Type substring
		wantBody     string // body substring
		wantCache    string // exact Cache-Control, "" = unset
		wantNotCache bool   // assert Cache-Control is NOT the immutable one
	}{
		{
			name: "root serves the shell with injected globals", method: http.MethodGet, target: "/",
			wantStatus: http.StatusOK, wantType: "text/html",
			wantBody:  `window.__HARBRR_BASE_URL__="";window.__HARBRR_VERSION__="v1.2.3";window.__HARBRR_EXTERNAL_URL__="https://harbrr.example.com"`,
			wantCache: "no-cache",
		},
		{
			name: "deep link falls back to the shell", method: http.MethodGet, target: "/indexers",
			wantStatus: http.StatusOK, wantType: "text/html", wantBody: `<div id="root">`,
		},
		{
			name: "nested deep link falls back", method: http.MethodGet, target: "/settings/cache",
			wantStatus: http.StatusOK, wantType: "text/html", wantBody: `<div id="root">`,
		},
		{
			name: "explicit index.html serves the processed shell", method: http.MethodGet, target: "/index.html",
			wantStatus: http.StatusOK, wantType: "text/html", wantBody: "__HARBRR_BASE_URL__",
		},
		{
			name: "hashed js asset with MIME + immutable cache", method: http.MethodGet, target: "/assets/index-abc123.js",
			wantStatus: http.StatusOK, wantType: "javascript", wantBody: "console.log",
			wantCache: "public, max-age=31536000, immutable",
		},
		{
			name: "hashed css asset", method: http.MethodGet, target: "/assets/index-abc123.css",
			wantStatus: http.StatusOK, wantType: "text/css",
			wantCache: "public, max-age=31536000, immutable",
		},
		{
			name: "non-hashed root asset is not immutable", method: http.MethodGet, target: "/favicon.svg",
			wantStatus: http.StatusOK, wantType: "svg", wantNotCache: true,
		},
		{
			name: "traversal cannot escape the bundle", method: http.MethodGet, target: "/../../etc/passwd",
			wantStatus: http.StatusOK, wantType: "text/html", wantBody: `<div id="root">`,
		},
		{
			name: "mutating method is rejected", method: http.MethodPost, target: "/",
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := get(t, h, tt.method, tt.target)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantType != "" && !strings.Contains(rec.Header().Get("Content-Type"), tt.wantType) {
				t.Errorf("Content-Type = %q, want substring %q", rec.Header().Get("Content-Type"), tt.wantType)
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body %q missing %q", rec.Body.String(), tt.wantBody)
			}
			if tt.wantCache != "" && rec.Header().Get("Cache-Control") != tt.wantCache {
				t.Errorf("Cache-Control = %q, want %q", rec.Header().Get("Cache-Control"), tt.wantCache)
			}
			if tt.wantNotCache && strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
				t.Errorf("Cache-Control = %q, want non-immutable", rec.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestHandlerBasePathRewrite(t *testing.T) {
	t.Parallel()
	h := ui.NewHandler(builtFS(), "/harbrr", "v1.2.3", "")

	body := get(t, h, http.MethodGet, "/").Body.String()
	for _, want := range []string{
		`src="/harbrr/assets/index-abc123.js"`,
		`href="/harbrr/assets/index-abc123.css"`,
		`window.__HARBRR_BASE_URL__="/harbrr"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index missing %q:\n%s", want, body)
		}
	}
}

func TestHandlerFrontendNotBuilt(t *testing.T) {
	t.Parallel()
	// A fresh checkout embeds only dist/.gitkeep — no index.html.
	h := ui.NewHandler(fstest.MapFS{".gitkeep": {Data: nil}}, "", "dev", "")

	rec := get(t, h, http.MethodGet, "/")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "make web-build") {
		t.Errorf("not-built response should point at make web-build: %q", rec.Body.String())
	}
}
