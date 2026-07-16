// Package ui serves the embedded single-page management UI: static files from
// the built web/dist bundle plus an index.html fallback for client-side routes
// (deep links like /indexers must load the SPA shell). It is mounted on the
// root catch-all, behind the explicit /healthz, /api and feed mounts
// (internal/server).
package ui

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// Handler serves the SPA bundle. Construct with NewHandler.
type Handler struct {
	fsys  fs.FS
	index []byte // processed index.html; nil when the frontend is not built
}

// NewHandler prepares the SPA handler: it loads index.html once, rewrites
// Vite's absolute asset URLs to live under basePath (the same serve-time
// rewrite qui uses), and injects the base path + version + external URL globals
// the client bootstraps from. A dist without index.html (fresh checkout,
// .gitkeep only) yields a handler that answers "frontend not built".
func NewHandler(fsys fs.FS, basePath, version, externalURL string) *Handler {
	raw, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		return &Handler{fsys: fsys}
	}
	return &Handler{fsys: fsys, index: processIndex(raw, basePath, version, externalURL)}
}

// processIndex rewrites root-absolute src/href URLs under basePath and injects
// the runtime globals before </head>.
func processIndex(index []byte, basePath, version, externalURL string) []byte {
	// Normalize away a trailing slash so a basePath like "/harbrr/" cannot produce
	// a doubled slash ("//assets/…") in the rewritten URLs or the injected global.
	basePath = strings.TrimRight(basePath, "/")
	if basePath != "" {
		index = bytes.ReplaceAll(index, []byte(`src="/`), []byte(`src="`+basePath+`/`))
		index = bytes.ReplaceAll(index, []byte(`href="/`), []byte(`href="`+basePath+`/`))
	}
	inject := fmt.Sprintf(
		"<script>window.__HARBRR_BASE_URL__=%q;window.__HARBRR_VERSION__=%q;window.__HARBRR_EXTERNAL_URL__=%q;</script></head>",
		basePath, version, externalURL,
	)
	return bytes.Replace(index, []byte("</head>"), []byte(inject), 1)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.index == nil {
		http.Error(w, "Frontend not built — run `make web-build` (release binaries and images ship it prebuilt).",
			http.StatusNotFound)
		return
	}
	name := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if name != "." && name != "index.html" && h.serveFile(w, r, name) {
		return
	}
	h.serveIndex(w)
}

// serveFile serves an existing bundle file and reports whether it did; a path
// that is not a bundle file falls through to the index fallback. Files under
// assets/ are content-hashed by Vite, so they are cacheable forever.
func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, name string) bool {
	data, err := fs.ReadFile(h.fsys, name)
	if err != nil {
		return false
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	// ServeContent picks the Content-Type from the extension; the zero modtime
	// skips Last-Modified, which hashed filenames make redundant anyway.
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	return true
}

// serveIndex serves the processed SPA shell. It must revalidate on every load
// so a redeploy takes effect immediately.
func (h *Handler) serveIndex(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(h.index)
}
