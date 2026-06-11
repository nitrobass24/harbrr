#!/usr/bin/env bash
# Pre-commit backstop: refuse to commit obvious tracker secrets. Runs alongside
# gitleaks; this is a cheap, targeted net for the credential shapes seekbrr
# handles (passkeys in URLs, Bearer tokens). See AGENTS.md.
set -euo pipefail

pattern='(passkey|torrent_pass|rsskey|api_?key|auth_?key)=[A-Za-z0-9]{16,}|[Aa]uthorization:[[:space:]]*[Bb]earer[[:space:]]+[A-Za-z0-9._-]{16,}'

# Only inspect added lines in the staged diff.
hits="$(git diff --cached -U0 -- ':(exclude)*_test.go' ':(exclude)testdata/**' ':(exclude)internal/indexer/definitions/vendor/**' \
  | grep -E '^\+' \
  | grep -nEi "$pattern" || true)"

if [ -n "$hits" ]; then
  echo "Refusing commit: possible secret(s) detected in staged changes:" >&2
  echo "$hits" >&2
  echo "Redact before committing (see AGENTS.md). If this is a false positive, scrub the literal value." >&2
  exit 1
fi

exit 0
