package torznabhttp

import (
	"context"
	"net/http"
)

// freeleechBypassKey is the unexported context key under which the freeleech-bypass
// feed variant signals that the serve-time freeleech view must be skipped (the full
// catalog is served). It lives here, not in search.Query at request time, so the route
// — not a query param — selects the variant; searchReleases copies it onto the engine
// query for the registry adapter's freeleech serve-time view to read.
type freeleechBypassKey struct{}

// WithFreeleechBypass marks ctx as a freeleech-bypass request (the `/results/torznab/full`
// variant), so the downstream freeleech view returns the full catalog instead of
// freeleech-only.
func WithFreeleechBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, freeleechBypassKey{}, true)
}

// freeleechBypass reports whether ctx carries the freeleech-bypass marker.
func freeleechBypass(ctx context.Context) bool {
	v, _ := ctx.Value(freeleechBypassKey{}).(bool)
	return v
}

// withFreeleechBypass wraps a handler so every request it serves carries the
// freeleech-bypass marker. The bypass feed routes are registered through it, so the
// same serve/caps code path drives both variants — only the marker differs.
func withFreeleechBypass(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next(w, r.WithContext(WithFreeleechBypass(r.Context())))
	}
}
