package torznabhttp

import (
	"net/http"

	"github.com/autobrr/harbrr/internal/indexer/core"
)

// withFreeleechBypass wraps a handler so every request it serves carries the
// freeleech-bypass marker (core.WithFreeleechBypass). The bypass feed routes are
// registered through it, so the same serve/caps code path drives both variants —
// only the marker differs.
func withFreeleechBypass(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next(w, r.WithContext(core.WithFreeleechBypass(r.Context())))
	}
}
