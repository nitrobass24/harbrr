// Package definitions provides access to tracker definitions: the embedded,
// read-only Jackett snapshot (vendor/) and user drop-in overrides (dropin/),
// which take precedence. The vendored snapshot is NEVER edited by hand
// (see AGENTS.md); it is refreshed by scripts/vendor-definitions.sh.
package definitions

import "embed"

// Vendored is the embedded Jackett definition snapshot. Do not edit files under
// vendor/ — they are vendored byte-for-byte. See docs/architecture.md (invariant
// 1) and AGENTS.md. The `all:` prefix ensures dotfiles (e.g. .jackett-ref) are
// included so provenance ships with the binary.
//
//go:embed all:vendor
var Vendored embed.FS

// DropInDir is the on-disk directory (relative to the configured data dir) where
// users place override definitions that take precedence over Vendored. The
// loader resolves precedence as: dropin > vendored, keyed by definition id.
const DropInDir = "definitions"
