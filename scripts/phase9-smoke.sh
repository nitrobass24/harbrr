#!/usr/bin/env bash
#
# phase9-smoke.sh — one repeatable command for the Phase 9 live pass: pull every
# tracker's credentials out of Prowlarr, build the harness env, and run the smoke
# harness against all of them. No tracker-picking, no typing creds/cookies.
#
# Secrets never touch disk or chat: the extractor's `export SMOKE_*` lines are eval'd
# into this process's env (shell-safe via jq @sh) and the harness reads them; the
# harness writes only secret-free evidence to internal/smoke/testdata/.
#
# Required env (set once):
#   SMOKE_HARBRR_URL      e.g. http://127.0.0.1:7474
#   SMOKE_HARBRR_APIKEY   a harbrr API key (POST /api/apikeys)
#   SMOKE_PROWLARR_URL    e.g. http://prowlarr:9696
#   PROWLARR_DB           path to prowlarr.db (copy it first if Prowlarr is running)
# Optional:
#   SMOKE_PROWLARR_APIKEY (auto-extracted from PROWLARR_DB when unset)
#   SMOKE_QUERY / SMOKE_QUERY_FALLBACK / SMOKE_GRAB (see the harness doc comment)
#
# Usage:
#   SMOKE_HARBRR_URL=… SMOKE_HARBRR_APIKEY=… SMOKE_PROWLARR_URL=… PROWLARR_DB=/tmp/prowlarr.db \
#     scripts/phase9-smoke.sh

set -euo pipefail

: "${SMOKE_HARBRR_URL:?set SMOKE_HARBRR_URL=http://host:7474}"
: "${SMOKE_HARBRR_APIKEY:?set SMOKE_HARBRR_APIKEY=<a harbrr API key>}"
: "${SMOKE_PROWLARR_URL:?set SMOKE_PROWLARR_URL=http://host:9696}"
: "${PROWLARR_DB:?set PROWLARR_DB=/path/to/prowlarr.db}"

repo="$(cd "$(dirname "$0")/.." && pwd)"

# Pull SMOKE_PROWLARR_APIKEY + SMOKE_TRACKERS + every SMOKE_SETTINGS_<SLUG> from the DB.
creds="$("$repo/scripts/prowlarr-extract-creds.sh" --env "$PROWLARR_DB")"
eval "$creds"

if [[ -z "${SMOKE_TRACKERS:-}" ]]; then
  echo "phase9-smoke: no indexers extracted from $PROWLARR_DB" >&2
  exit 1
fi

# Print the (non-secret) tracker mapping only — never the SMOKE_SETTINGS_* values.
echo "phase9-smoke: running against:" >&2
tr ',' '\n' <<<"$SMOKE_TRACKERS" | sed 's/^/  - /' >&2

cd "$repo"
exec go test -tags smoke ./internal/smoke/ -run TestSmoke -v
