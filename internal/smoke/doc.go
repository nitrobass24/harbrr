// Package smoke holds the Phase 5 LIVE smoke harness: a real Sonarr/Radarr-style
// driver that points at a running harbrr daemon, searches a handful of real
// trackers, and diffs the results against Prowlarr as a differential oracle.
//
// The harness itself lives entirely in build-tagged files (//go:build smoke) so
// it NEVER compiles or runs under a normal `go test ./...` / CI invocation — it
// requires live credentials supplied via environment variables and reaches real
// trackers, which must never happen in CI. This untagged file exists only so the
// package is non-empty for `go build ./...`; it carries no logic.
//
// Run it manually with: make smoke-test (see docs/phase5-setup.md).
package smoke
