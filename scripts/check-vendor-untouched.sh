#!/usr/bin/env bash
# Pre-commit + CI guard: refuse hand-edits to the vendored Jackett definition
# snapshot under internal/indexer/definitions/vendor/. Those files are consumed
# byte-for-byte from Jackett; behavioral differences belong in the engine, upstream
# in Jackett, or in internal/indexer/definitions/dropin/ — never here. Refresh the
# whole snapshot with `make vendor-defs` (the scheduled vendor-definitions workflow).
# See AGENTS.md.
#
# This is the tool-agnostic enforcement: it runs in CI (authoritative, non-bypassable
# once required in branch protection) and as a pre-commit hook, so the rule holds no
# matter which editor/agent a contributor uses.
#
#   (no args)  scan the staged diff                  — pre-commit
#   --ci       scan the PR diff vs its base branch    — CI (pre-commit can be skipped)
set -euo pipefail

vendor_re='^internal/indexer/definitions/vendor/'

# The scheduled refresh opens its PR on this branch; that is the one sanctioned way the
# snapshot changes, so let it through (in CI GITHUB_HEAD_REF is the PR's source branch).
if [ "${GITHUB_HEAD_REF:-}" = "chore/vendor-definitions" ]; then
  echo "vendor-guard: skipped (scheduled definitions refresh)"
  exit 0
fi

if [ "${1:-}" = "--ci" ]; then
  base_ref="${GITHUB_BASE_REF:-main}"
  base="refs/remotes/origin/${base_ref}"
  git fetch --quiet --depth=1 origin "${base_ref}:${base}" 2>/dev/null || true

  if ! git rev-parse --verify --quiet "${base}^{commit}" >/dev/null; then
    git fetch --quiet --depth=1 origin "${base_ref}"
    base="FETCH_HEAD"
  fi

  changed="$(git diff --name-only "${base}...HEAD" 2>/dev/null || git diff --name-only "${base}" HEAD)"
else
  changed="$(git diff --cached --name-only)"
fi

offenders="$(printf '%s\n' "${changed}" | grep -E "${vendor_re}" || true)"

if [ -n "${offenders}" ]; then
  {
    echo "BLOCKED: this change modifies vendored Jackett definitions (consumed byte-for-byte):"
    printf '  %s\n' ${offenders}
    echo
    echo "Do not hand-edit these. Refresh the whole snapshot with 'make vendor-defs', or add an"
    echo "override under internal/indexer/definitions/dropin/. See AGENTS.md."
  } >&2
  exit 1
fi

echo "vendor-guard: no vendored-definition changes"
