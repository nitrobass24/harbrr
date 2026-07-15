package core

import "context"

// freeleechBypassKey is the unexported context key under which the freeleech-bypass
// feed variant signals that the serve-time freeleech view must be skipped (the full
// catalog is served). It lives here, not in search.Query at request time, so the route
// — not a query param — selects the variant; SearchReleases copies it onto the engine
// query for the registry adapter's freeleech serve-time view to read.
type freeleechBypassKey struct{}

// WithFreeleechBypass marks ctx as a freeleech-bypass request (the `/results/torznab/full`
// variant), so the downstream freeleech view returns the full catalog instead of
// freeleech-only. The Torznab handler's bypass route sets this; SearchReleases and the
// handler's revalidator both read it via FreeleechBypass.
func WithFreeleechBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, freeleechBypassKey{}, true)
}

// FreeleechBypass reports whether ctx carries the freeleech-bypass marker.
func FreeleechBypass(ctx context.Context) bool {
	v, _ := ctx.Value(freeleechBypassKey{}).(bool)
	return v
}
