#!/usr/bin/env bash
# Pre-commit + CI backstop: refuse obvious tracker secrets. Runs alongside
# gitleaks; a cheap, targeted net for the credential shapes harbrr handles
# (passkeys in URLs, Bearer tokens). See AGENTS.md.
#
#   (no args)  scan the staged diff     — pre-commit
#   --all      scan all tracked files   — CI (pre-commit can be skipped)
set -euo pipefail

pattern='(passkey|torrent_pass|rsskey|api_?key|auth_?key)=[A-Za-z0-9]{16,}|[Aa]uthorization:[[:space:]]*[Bb]earer[[:space:]]+[A-Za-z0-9._-]{16,}'

# Synthetic fixture secrets (exist only to prove redaction) live here; keep in
# sync with .gitleaks.toml (which excludes `(^|/)testdata/` — testdata at ANY
# depth). A bare `testdata/**` git pathspec is anchored to the repo root and would
# miss nested fixtures (e.g. internal/torznab/testdata/), so exclude both the root
# `testdata/**` and any nested `*/testdata/**`. `*_test.go` already matches at any
# depth because a git pathspec `*` spans `/`.
excludes=(':(exclude)*_test.go' ':(exclude)testdata/**' ':(exclude)*/testdata/**' ':(exclude)internal/indexer/definitions/vendor/**')

if [ "${1:-}" = "--all" ]; then
  hits="$(git grep -nEi "$pattern" -- "${excludes[@]}" || true)"
else
  # Only inspect added lines in the staged diff.
  hits="$(git diff --cached -U0 -- "${excludes[@]}" \
    | grep -E '^\+' \
    | grep -nEi "$pattern" || true)"
fi

if [ -n "$hits" ]; then
  echo "Refusing: possible secret(s) detected outside test fixtures:" >&2
  echo "$hits" >&2
  echo "Redact before committing (see AGENTS.md). If this is a false positive, scrub the literal value." >&2
  exit 1
fi

exit 0
