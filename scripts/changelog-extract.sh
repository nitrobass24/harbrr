#!/usr/bin/env bash
# Print the "highlights" block for a version from CHANGELOG.md — the text between
# the version heading (## [x.y.z]) and either a <!-- release-header-end --> marker
# or the next version heading. Used by CI to populate the GitHub Release header
# (goreleaser then appends the auto-generated feat/fix commit list below it).
#
# Accepts a tag with or without a leading "v" (v0.1.0-alpha -> 0.1.0-alpha).
# Prints nothing (exit 0) if the version has no section, so releases degrade to
# the auto commit list rather than failing.
set -euo pipefail

ver="${1:?usage: changelog-extract.sh <version>}"
ver="${ver#v}"
file="${CHANGELOG_FILE:-CHANGELOG.md}"
[ -f "$file" ] || exit 0

awk -v ver="$ver" '
  index($0, "## [" ver "]") == 1 { grab = 1; next }
  grab && (index($0, "## [") == 1 || $0 ~ /<!-- *release-header-end *-->/) { exit }
  grab { print }
' "$file"
