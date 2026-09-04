#!/usr/bin/env bash
# Pre-commit + CI guard: an edit to internal/web/swagger/openapi.yaml is a WEB change,
# because web/src/types/api.gen.ts is generated from that spec. Editing the spec without
# regenerating leaves the committed types stale.
#
# `make test-openapi` does NOT catch this — it compares method/path sets, so any change to
# responses, schemas or descriptions passes it cleanly. The only authoritative check is
# regenerate-and-diff, which is what CI's `web` job does (its bundle cache key includes the
# spec, so a spec edit always busts the cache and runs it). This guard runs that same check
# locally, so the mistake is caught before the push rather than in a job the contributor had
# no reason to associate with a Go-side spec edit.
#
# Deliberately regenerate-and-diff, not "was api.gen.ts staged too?": openapi-typescript
# emits @description JSDoc into the generated file, so MOST spec edits change it but not all
# (a summary:-only edit may not). Co-staging would demand a regeneration that produces
# nothing, and would miss nothing CI catches.
#
#   (no args)  scan the staged diff                — pre-commit
#   --ci       scan the PR diff vs its base branch  — CI (pre-commit can be skipped)
set -euo pipefail

spec='internal/web/swagger/openapi.yaml'
generated='web/src/types/api.gen.ts'

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

if ! printf '%s\n' "${changed}" | grep -qxF "${spec}"; then
  exit 0
fi

# bun is not on a non-interactive shell's PATH even when installed, so look where the
# installer puts it before giving up.
if ! command -v bun >/dev/null 2>&1 && [ -x "${HOME}/.bun/bin/bun" ]; then
  PATH="${HOME}/.bun/bin:${PATH}"
  export PATH
fi

# A Go-only contributor may not have the JS toolchain at all. Warn rather than block —
# CI's web job is the authoritative gate and always runs this on a spec change.
if ! command -v bun >/dev/null 2>&1; then
  echo "openapi-types-guard: ${spec} changed but bun is not installed — SKIPPING the local check." >&2
  echo "  CI will regenerate ${generated} and fail if it drifts." >&2
  echo "  To check here: install bun, then run 'bun run generate:api' in web/ and commit ${generated}." >&2
  exit 0
fi

before="$(git hash-object "${generated}")"
( cd web && bun run generate:api >/dev/null 2>&1 ) || {
  echo "openapi-types-guard: 'bun run generate:api' failed — cannot verify ${generated}." >&2
  exit 1
}
after="$(git hash-object "${generated}")"

if [ "${before}" != "${after}" ]; then
  cat >&2 <<EOF
openapi-types-guard: ${spec} changed but ${generated} is stale.

  It has now been regenerated in your working tree. Stage it:

      git add ${generated}

  make test-openapi does not catch this (it compares method/path sets only); CI's web job
  regenerates and diffs, which is what this guard mirrors.
EOF
  exit 1
fi

echo "openapi-types-guard: ${generated} is in sync with ${spec}"
