// Package web embeds the built single-page management UI (the Vite bundle in
// dist/) into the harbrr binary. dist/.gitkeep is committed so `go build`
// always succeeds without a frontend build; a gitkeep-only dist makes the ui
// handler answer "frontend not built" (internal/web/ui).
package web

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the embedded dist directory with the "dist/" prefix stripped,
// so files resolve as "index.html", "assets/…".
func Dist() (fs.FS, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("web: embedded dist: %w", err)
	}
	return sub, nil
}
