// Package version holds build/version metadata for harbrr.
package version

// These are overridden at build time via -ldflags (see the Makefile).
var (
	Version = "0.0.0-dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human-readable version string.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}
