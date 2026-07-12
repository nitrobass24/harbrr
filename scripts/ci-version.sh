#!/usr/bin/env bash
# Print a human-friendly version string for build-time stamping
# (internal/version.Version), mirroring qui's display:
#
#   tag build   -> the tag           e.g. v0.1.0-alpha
#   PR build    -> pr-<number>       e.g. pr-89
#   main build  -> develop
#   otherwise   -> git describe      e.g. v0.1.0-alpha-3-gabc1234 (or 0.0.0-dev)
#
# The commit SHA is stamped separately (internal/version.Commit) and surfaced in
# the web UI footer's Settings pane and the startup log — so releases show a
# meaningful version while the exact commit stays one click away.
set -euo pipefail

if [ "${GITHUB_EVENT_NAME:-}" = "pull_request" ] && [ -n "${PR_NUMBER:-}" ]; then
  echo "pr-${PR_NUMBER}"
  exit 0
fi

case "${GITHUB_REF:-}" in
  refs/tags/*)
    echo "${GITHUB_REF#refs/tags/}"
    ;;
  refs/heads/main)
    echo "develop"
    ;;
  *)
    git -c safe.directory="${GITHUB_WORKSPACE:-$PWD}" describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev"
    ;;
esac
